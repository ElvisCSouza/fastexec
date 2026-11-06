package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"homelab/fastexec/internal/logger"
)

// Conexões persistentes com os daemons server-exec (:9142).
//
// O daemon aceita vários pedidos na mesma conexão. Reaproveitar a conexão tira o handshake TCP, o accept e a
// thread nova do servidor de cada chamada. Regras de segurança:
//   - uma conexão ociosa tem uma goroutine vigia lendo 1 byte: se o servidor fechou (FIN/RST) ou mandou lixo,
//     a conexão é descartada antes de ser usada; ao pegar, a vigia é acordada com um deadline no passado;
//   - um comando enviado nunca é reenviado: só se a escrita do pedido falhar numa conexão reaproveitada
//     (o servidor não executa pedido incompleto) refaz-se uma vez com conexão nova;
//   - qualquer erro, timeout ou resposta incompleta fecha a conexão (o servidor mata o filho ao ver o fechamento).

const (
	MagicX uint16 = 0x5358 // 'SX': argv direto (sem shell), cwd e stdin como campos

	flagDirect uint16 = 1

	poolMaxIdle  = 4
	poolIdleMax  = 9 * time.Minute // o servidor fecha conexões paradas por 10 min
	maxStdinLen  = 16 * 1024 * 1024
	xHeaderReq   = 16
	xHeaderResp  = 16
	maxCwdLen    = 4096
	probeTimeout = 5 * time.Second
)

type idleConn struct {
	c     net.Conn
	since time.Time
	dead  atomic.Bool
	done  chan struct{}
}

func newIdle(c net.Conn) *idleConn {
	ic := &idleConn{c: c, since: time.Now(), done: make(chan struct{})}
	go func() {
		var b [1]byte
		n, err := c.Read(b[:])
		var ne net.Error
		if n > 0 || !(errors.As(err, &ne) && ne.Timeout()) {
			ic.dead.Store(true)
		}
		close(ic.done)
	}()
	return ic
}

// claim acorda a vigia e diz se a conexão ainda serve.
func (ic *idleConn) claim() (net.Conn, bool) {
	_ = ic.c.SetReadDeadline(time.Now().Add(-time.Second))
	<-ic.done
	if ic.dead.Load() || time.Since(ic.since) > poolIdleMax {
		_ = ic.c.Close()
		return nil, false
	}
	_ = ic.c.SetReadDeadline(time.Time{})
	return ic.c, true
}

var (
	poolMu   sync.Mutex
	poolIdle = map[string][]*idleConn{}
	xSupport = map[string]bool{} // addr -> o daemon entende SX (só entra depois de uma sonda conclusiva)
	xKnown   = map[string]bool{}
)

func dialDaemon(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, DialTimeout)
	if err != nil {
		logger.Error("Falha de conexão com %s: %v", addr, err)
		return nil, fmt.Errorf("host inacessível (%s): %w", addr, err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	return conn, nil
}

func getConn(addr string) (net.Conn, bool, error) {
	for {
		poolMu.Lock()
		l := poolIdle[addr]
		if len(l) == 0 {
			poolMu.Unlock()
			break
		}
		ic := l[len(l)-1]
		poolIdle[addr] = l[:len(l)-1]
		poolMu.Unlock()
		if c, ok := ic.claim(); ok {
			return c, true, nil
		}
	}
	c, err := dialDaemon(addr)
	return c, false, err
}

func putConn(addr string, c net.Conn) {
	_ = c.SetDeadline(time.Time{})
	poolMu.Lock()
	if len(poolIdle[addr]) >= poolMaxIdle {
		poolMu.Unlock()
		_ = c.Close()
		return
	}
	poolIdle[addr] = append(poolIdle[addr], newIdle(c))
	poolMu.Unlock()
}

// countingReader conta os bytes lidos: resposta com 0 bytes + erro quer dizer que o servidor nem respondeu.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

type respParser func(r io.Reader) (*ExecResult, error)

// roundTrip envia req e lê a resposta. gotBytes diz se chegou algum byte de resposta.
func roundTrip(addr string, req []byte, timeout time.Duration, parse respParser) (res *ExecResult, gotBytes bool, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		conn, reused, derr := getConn(addr)
		if derr != nil {
			return nil, false, derr
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, werr := conn.Write(req); werr != nil {
			_ = conn.Close()
			if reused && attempt == 0 {
				continue // pedido incompleto não executa: refaz com conexão nova
			}
			return nil, false, fmt.Errorf("falha ao enviar para %s: %w", addr, werr)
		}
		cr := &countingReader{r: conn}
		res, perr := parse(cr)
		if perr != nil {
			_ = conn.Close()
			return nil, cr.n > 0, perr
		}
		putConn(addr, conn)
		return res, true, nil
	}
	return nil, false, fmt.Errorf("falha ao enviar para %s", addr)
}

// ---------------------------------------------------------------------------------------------
// SE (linha de shell, formato legado)

func parseSE(addr string) respParser {
	return func(r io.Reader) (*ExecResult, error) {
		h := make([]byte, 10)
		if _, err := io.ReadFull(r, h); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, fmt.Errorf("timeout aguardando resposta de %s: o comando excedeu o limite", addr)
			}
			return nil, fmt.Errorf("falha ao ler cabeçalho da resposta de %s: %w", addr, err)
		}
		if m := binary.LittleEndian.Uint16(h[0:2]); m != MagicExec {
			return nil, fmt.Errorf("protocolo inválido de %s: magic esperado 0x%x, recebido 0x%x", addr, MagicExec, m)
		}
		exit := int(int32(binary.LittleEndian.Uint32(h[2:6])))
		outLen := binary.LittleEndian.Uint32(h[6:10])
		if outLen > MaxOutputLen {
			return nil, fmt.Errorf("stdout excedeu limite físico do daemon (%d bytes)", outLen)
		}
		stdout := make([]byte, outLen)
		if _, err := io.ReadFull(r, stdout); err != nil {
			return nil, fmt.Errorf("falha ao ler stdout de %s: %w", addr, err)
		}
		var l [4]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return nil, fmt.Errorf("falha ao ler tamanho de stderr de %s: %w", addr, err)
		}
		errLen := binary.LittleEndian.Uint32(l[:])
		if errLen > MaxOutputLen {
			return nil, fmt.Errorf("stderr excedeu limite físico do daemon (%d bytes)", errLen)
		}
		stderr := make([]byte, errLen)
		if _, err := io.ReadFull(r, stderr); err != nil {
			return nil, fmt.Errorf("falha ao ler stderr de %s: %w", addr, err)
		}
		return &ExecResult{ExitCode: exit, Stdout: string(stdout), Stderr: string(stderr)}, nil
	}
}

// ExecDaemon executa uma linha de shell (SE) no daemon, reaproveitando conexão.
func ExecDaemon(tcpAddr string, cmd string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cmdBytes := []byte(cmd)
	if len(cmdBytes) > MaxCmdLen {
		return nil, fmt.Errorf("comando excede tamanho máximo permitido (1MB)")
	}
	req := make([]byte, 6+len(cmdBytes))
	binary.LittleEndian.PutUint16(req[0:2], MagicExec)
	binary.LittleEndian.PutUint32(req[2:6], uint32(len(cmdBytes)))
	copy(req[6:], cmdBytes)

	start := time.Now()
	res, _, err := roundTrip(tcpAddr, req, timeout, parseSE(tcpAddr))
	if err != nil {
		return nil, err
	}
	res.Duration = time.Since(start).Milliseconds()
	logger.Debug("[%s] cmd: %.40s... -> exit %d (%dms)", tcpAddr, cmd, res.ExitCode, res.Duration)
	return res, nil
}

// ---------------------------------------------------------------------------------------------
// SX (argv direto, cwd e stdin como campos)

// XRequest: Argv != nil executa sem shell; senão Shell é uma linha de bash -c.
type XRequest struct {
	Argv  []string
	Shell string
	Cwd   string
	Stdin string
}

func encodeX(q XRequest) ([]byte, error) {
	var body []byte
	var flags uint16
	argc := 0
	if q.Argv != nil {
		flags = flagDirect
		argc = len(q.Argv)
		if argc == 0 || argc > 0xFFFF {
			return nil, fmt.Errorf("argv inválido")
		}
		for _, a := range q.Argv {
			if strings.IndexByte(a, 0) >= 0 {
				return nil, fmt.Errorf("argumento com byte NUL não é suportado no modo direto")
			}
			body = append(body, a...)
			body = append(body, 0)
		}
	} else {
		body = []byte(q.Shell)
	}
	if len(body) > MaxCmdLen {
		return nil, fmt.Errorf("comando excede tamanho máximo permitido (1MB)")
	}
	if len(q.Cwd) > maxCwdLen {
		return nil, fmt.Errorf("cwd excede 4096 bytes")
	}
	if len(q.Stdin) > maxStdinLen {
		return nil, fmt.Errorf("stdin excede 16MB")
	}
	req := make([]byte, xHeaderReq+len(q.Cwd)+len(body)+len(q.Stdin))
	binary.LittleEndian.PutUint16(req[0:2], MagicX)
	binary.LittleEndian.PutUint16(req[2:4], flags)
	binary.LittleEndian.PutUint16(req[4:6], uint16(len(q.Cwd)))
	binary.LittleEndian.PutUint16(req[6:8], uint16(argc))
	binary.LittleEndian.PutUint32(req[8:12], uint32(len(body)))
	binary.LittleEndian.PutUint32(req[12:16], uint32(len(q.Stdin)))
	o := xHeaderReq
	o += copy(req[o:], q.Cwd)
	o += copy(req[o:], body)
	copy(req[o:], q.Stdin)
	return req, nil
}

func parseX(addr string) respParser {
	return func(r io.Reader) (*ExecResult, error) {
		h := make([]byte, xHeaderResp)
		if _, err := io.ReadFull(r, h); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, fmt.Errorf("timeout aguardando resposta de %s: o comando excedeu o limite", addr)
			}
			return nil, fmt.Errorf("falha ao ler cabeçalho da resposta de %s: %w", addr, err)
		}
		if m := binary.LittleEndian.Uint16(h[0:2]); m != MagicX {
			return nil, fmt.Errorf("protocolo inválido de %s: magic esperado 0x%x, recebido 0x%x", addr, MagicX, m)
		}
		exit := int(int32(binary.LittleEndian.Uint32(h[4:8])))
		outLen := binary.LittleEndian.Uint32(h[8:12])
		errLen := binary.LittleEndian.Uint32(h[12:16])
		if outLen > MaxOutputLen || errLen > MaxOutputLen {
			return nil, fmt.Errorf("saída excedeu limite físico do daemon (%d/%d bytes)", outLen, errLen)
		}
		body := make([]byte, int(outLen)+int(errLen))
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("falha ao ler saída de %s: %w", addr, err)
		}
		return &ExecResult{ExitCode: exit, Stdout: string(body[:outLen]), Stderr: string(body[outLen:])}, nil
	}
}

// DaemonSupportsX descobre (e guarda) se o daemon entende SX, com uma sonda inócua (`true` direto).
// Resposta conclusiva é guardada; host inacessível não é guardado. Daemon antigo fecha a conexão ao ver magic
// desconhecido sem executar nada, por isso a sonda é segura.
func DaemonSupportsX(addr string) (bool, error) {
	poolMu.Lock()
	if xKnown[addr] {
		ok := xSupport[addr]
		poolMu.Unlock()
		return ok, nil
	}
	poolMu.Unlock()

	req, _ := encodeX(XRequest{Argv: []string{"true"}})
	_, got, err := roundTrip(addr, req, probeTimeout, parseX(addr))
	if err != nil {
		if strings.Contains(err.Error(), "host inacessível") {
			return false, err
		}
		if got {
			return false, err // resposta estranha: não conclui
		}
		poolMu.Lock()
		xKnown[addr], xSupport[addr] = true, false // sem nenhum byte de volta: daemon antigo
		poolMu.Unlock()
		logger.Debug("[%s] daemon sem SX; usando SE", addr)
		return false, nil
	}
	poolMu.Lock()
	xKnown[addr], xSupport[addr] = true, true
	poolMu.Unlock()
	return true, nil
}

// ExecDaemonX executa um pedido SX. Se o daemon não suportar (descoberto por sonda), devolve ok=false e nada
// foi executado: o chamador cai para SE.
func ExecDaemonX(tcpAddr string, q XRequest, timeout time.Duration) (res *ExecResult, ok bool, err error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	sup, err := DaemonSupportsX(tcpAddr)
	if err != nil {
		return nil, false, err
	}
	if !sup {
		return nil, false, nil
	}
	req, err := encodeX(q)
	if err != nil {
		return nil, true, err
	}
	start := time.Now()
	res, _, err = roundTrip(tcpAddr, req, timeout, parseX(tcpAddr))
	if err != nil {
		return nil, true, err
	}
	res.Duration = time.Since(start).Milliseconds()
	return res, true, nil
}

// tryDaemonX tenta SX no host. ok=false (e err=nil) quer dizer que o host não usa SX (SSH, daemon antigo ou
// FASTEXEC_NO_SX=1) e nada foi executado: o chamador usa o caminho SE.
func tryDaemonX(target HostTarget, q XRequest, timeout time.Duration) (*ExecResult, bool, error) {
	if target.IsSSH || target.TCPAddr == "" || os.Getenv("FASTEXEC_NO_SX") != "" {
		return nil, false, nil
	}
	return ExecDaemonX(target.TCPAddr, q, timeout)
}
