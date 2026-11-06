package transport

// auth.go: token por host (config 'token'), conferido pelo daemon uma vez por conexão (mensagem 'SA').
// O SA sai colado no primeiro pedido, sem esperar resposta: abrir conexão com token custa os mesmos round trips
// que sem token, e as conexões do pool não pagam nada. Sem token na config, nada muda no fio.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"homelab/fastexec/internal/config"
)

const (
	MagicAuth   uint16 = 0x5341 // 'SA'
	maxTokenLen        = 256
)

// ErrAuth marca falha de token: nunca é confundida com daemon antigo sem SX (a sonda não guarda o resultado).
var ErrAuth = errors.New("auth failed")

// tokenFor devolve o token configurado para o endereço do daemon ("" = sem auth).
func tokenFor(addr string) string {
	cfg := config.Get()
	for _, name := range cfg.Remote(false) {
		_, h, _ := cfg.Lookup(name)
		if h.Token != "" && h.Addr != "" && withPort(h.Addr) == addr {
			return h.Token
		}
	}
	return ""
}

// withAuth prepara o SA (se o host tem token): ele sai no mesmo pacote do primeiro pedido (primeiro Write) e a
// resposta é conferida no primeiro Read.
func withAuth(conn net.Conn, addr string) (net.Conn, error) {
	tok := tokenFor(addr)
	if tok == "" {
		return conn, nil
	}
	if len(tok) > maxTokenLen {
		_ = conn.Close()
		return nil, fmt.Errorf("token for %s is longer than %d bytes", addr, maxTokenLen)
	}
	msg := make([]byte, 4+len(tok))
	binary.LittleEndian.PutUint16(msg[0:2], MagicAuth)
	binary.LittleEndian.PutUint16(msg[2:4], uint16(len(tok)))
	copy(msg[4:], tok)
	return &authConn{Conn: conn, addr: addr, hello: msg, pending: true}, nil
}

// authConn cola o SA no primeiro Write e consome a resposta dele antes do primeiro byte da resposta do pedido.
type authConn struct {
	net.Conn
	addr    string
	hello   []byte // SA ainda não enviado
	pending bool   // resposta do SA ainda não lida
}

func (c *authConn) Write(p []byte) (int, error) {
	if c.hello == nil {
		return c.Conn.Write(p)
	}
	buf := append(c.hello, p...)
	c.hello = nil
	n, err := c.Conn.Write(buf)
	n -= 4 + int(binary.LittleEndian.Uint16(buf[2:4]))
	if n < 0 {
		n = 0
	}
	return n, err
}

func (c *authConn) Read(p []byte) (int, error) {
	if c.pending {
		var r [4]byte
		if _, err := io.ReadFull(c.Conn, r[:]); err != nil {
			return 0, fmt.Errorf("%w: %s closed the connection at auth (token rejected, or server-exec too old for tokens): %w", ErrAuth, c.addr, err)
		}
		if binary.LittleEndian.Uint16(r[0:2]) != MagicAuth || binary.LittleEndian.Uint16(r[2:4]) != 0 {
			return 0, fmt.Errorf("%w: %s refused the token (wrong token)", ErrAuth, c.addr)
		}
		c.pending = false
	}
	return c.Conn.Read(p)
}

// authRequired: resposta SA no lugar da resposta do pedido = o daemon exige token e o cliente não mandou.
func authRequired(addr string, magic uint16) error {
	if magic != MagicAuth {
		return nil
	}
	return fmt.Errorf("%w: %s requires a token: set 'token' for this host in the FastExec config", ErrAuth, addr)
}

// NewToken gera um token aleatório de 32 bytes em hex (64 caracteres), para o daemon e a config do cliente.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(hex.EncodeToString(b))
}
