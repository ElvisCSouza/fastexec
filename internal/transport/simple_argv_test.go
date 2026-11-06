package transport

import (
	"encoding/binary"
	"net"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSimpleArgvAceita(t *testing.T) {
	casos := map[string][]string{
		"ls -la /tmp":                       {"ls", "-la", "/tmp"},
		"git status":                        {"git", "status"},
		"docker ps -a":                      {"docker", "ps", "-a"},
		"systemctl --user is-active x":      {"systemctl", "--user", "is-active", "x"},
		"curl -s -m3 127.0.0.1:8910/health": {"curl", "-s", "-m3", "127.0.0.1:8910/health"},
		"python3 script.py --porta=9142":    {"python3", "script.py", "--porta=9142"},
		"ls   -d    /":                      {"ls", "-d", "/"},
		"/usr/bin/env":                      {"/usr/bin/env"},
		"cat a.txt b.txt":                   {"cat", "a.txt", "b.txt"},
		"tail -n 5 /var/log/x.log":          {"tail", "-n", "5", "/var/log/x.log"},
	}
	for cmd, want := range casos {
		got, ok := SimpleArgv(cmd)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v ok=%v, want %v", cmd, got, ok, want)
		}
	}
}

func TestSimpleArgvRecusa(t *testing.T) {
	// qualquer coisa que o bash interpretaria, ou que seja interna/reservada, tem de ir pelo bash
	casos := []string{
		"", "   ", "ls | wc", "ls && pwd", "ls; pwd", "ls > x", "ls < x", "ls &", "echo $HOME", "echo `date`",
		"echo \"a b\"", "echo 'a b'", "ls *.go", "ls ?.go", "ls [a-c]", "ls {a,b}", "ls ~", "ls ~/x", "ls !x",
		"ls #c", "ls \\x", "ls\npwd", "ls\tpwd", "(ls)", "FOO=1 ls", "FOO=1", "cd /tmp", "export A=1", "echo oi",
		"printf x", "pwd", "true", "false", "test -f x", "kill 1", "source x", ". x", "exit 1", "exec ls", "time ls",
		"if true", "for x", "while", "! ls", "-x", "ls ç", "set -e", "type ls", "command ls", "read x", "ulimit -a",
		strings.Repeat("a", 9000),
	}
	for _, cmd := range casos {
		if got, ok := SimpleArgv(cmd); ok {
			t.Errorf("%q devia ir pelo bash, mas virou argv %v", cmd, got)
		}
	}
}

func TestSimpleArgvDesligavel(t *testing.T) {
	t.Setenv("FASTEXEC_NO_DIRECT", "1")
	if _, ok := SimpleArgv("ls -la"); ok {
		t.Fatal("FASTEXEC_NO_DIRECT=1 devia desligar o modo direto")
	}
}

func TestEncodeX(t *testing.T) {
	req, err := encodeX(XRequest{Argv: []string{"cat", "a b"}, Cwd: "/tmp", Stdin: "xyz"})
	if err != nil {
		t.Fatal(err)
	}
	want := 16 + len("/tmp") + len("cat\x00a b\x00") + len("xyz")
	if len(req) != want {
		t.Fatalf("tamanho %d, esperado %d", len(req), want)
	}
	if req[0] != 0x58 || req[1] != 0x53 { // 'SX' little-endian: 0x5358
		t.Fatalf("magic errado: %x %x", req[0], req[1])
	}
	if _, err := encodeX(XRequest{Argv: []string{"a\x00b"}}); err == nil {
		t.Fatal("NUL no argumento devia ser recusado")
	}
	if _, err := encodeX(XRequest{Argv: []string{}}); err == nil {
		t.Fatal("argv vazio devia ser recusado")
	}
}

// Integração com um daemon Linux real (FASTEXEC_TEST_ADDR=ip:porta; pula sem ela ou se inacessível): a via nova (SX direto e SX shell, com pool)
// tem de dar o mesmo exit code e a mesma saída que a via antiga (SE com bash e o prefixo de ambiente).
func TestEquivalenciaSEeSX(t *testing.T) {
	addr := testDaemonAddr(t)
	if ok, _ := DaemonSupportsX(addr); !ok {
		t.Skip("daemon sem SX")
	}
	prefixo := "export CI=1 DEBIAN_FRONTEND=noninteractive PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb NO_COLOR=1; "
	casos := []string{
		"ls -d /", "ls -d /nao/existe", "cat /etc/hostname", "uname -s", "id -un", "ls /etc/hostname /nao/existe",
		"git --version", "docker --version", "python3 -c 1", "grep -c root /etc/passwd", "grep naoexiste /etc/passwd",
		"head -n 2 /etc/os-release", "wc -l /etc/hostname", "date +%Y", "stat -c %s /etc/hostname", "df -h /",
		"echo a | tr a b", "echo $HOME", "echo oi; echo erro >&2; exit 3", "true && echo ok", "pwd",
		"printf 'x y'", "ls *.md | head -n 1", "cd /tmp && pwd", "false", "test -d /tmp",
		"systemctl is-active server-exec", "systemctl --user is-active nao-existe-xyz", "cat /etc/passwd | wc -l",
	}
	for _, cmd := range casos {
		antigo, err := ExecDaemon(addr, prefixo+"cd \"/tmp\" && "+cmd, 20*time.Second)
		if err != nil {
			t.Fatalf("%q via SE: %v", cmd, err)
		}
		q := XRequest{Shell: cmd, Cwd: "/tmp"}
		if argv, ok := SimpleArgv(cmd); ok {
			q = XRequest{Argv: argv, Cwd: "/tmp"}
		}
		novo, ok, err := ExecDaemonX(addr, q, 20*time.Second)
		if err != nil || !ok {
			t.Fatalf("%q via SX: ok=%v err=%v", cmd, ok, err)
		}
		if antigo.ExitCode != novo.ExitCode || antigo.Stdout != novo.Stdout {
			t.Errorf("%q: SE exit=%d out=%q | SX exit=%d out=%q", cmd, antigo.ExitCode, antigo.Stdout, novo.ExitCode, novo.Stdout)
		}
		// stderr só difere em mensagem de "não achou" (bash x servidor); o resto tem de ser igual
		if antigo.Stderr != novo.Stderr && antigo.ExitCode != 127 && !strings.Contains(antigo.Stderr, "No such file") {
			t.Errorf("%q: stderr SE=%q SX=%q", cmd, antigo.Stderr, novo.Stderr)
		}
	}
	// argv com caracteres perigosos viaja intacto
	r, ok, err := ExecDaemonX(addr, XRequest{Argv: []string{"/bin/echo", "a b", "$HOME", "x;y", "'q'"}}, 10*time.Second)
	if err != nil || !ok || r.Stdout != "a b $HOME x;y 'q'\n" {
		t.Errorf("argv literal: %v %v %+v", ok, err, r)
	}
	// stdin sem printf|base64
	r, ok, err = ExecDaemonX(addr, XRequest{Argv: []string{"wc", "-c"}, Stdin: "12345"}, 10*time.Second)
	if err != nil || !ok || strings.TrimSpace(r.Stdout) != "5" {
		t.Errorf("stdin: %v %v %+v", ok, err, r)
	}
}

// Conexão reaproveitada: 20 chamadas seguidas funcionam e o servidor reiniciado é detectado antes do envio.
func TestPoolReaproveita(t *testing.T) {
	addr := testDaemonAddr(t)
	for i := 0; i < 20; i++ {
		r, err := ExecDaemon(addr, "echo pool", 10*time.Second)
		if err != nil || r.Stdout != "pool\n" {
			t.Fatalf("chamada %d: %v %+v", i, err, r)
		}
	}
	poolMu.Lock()
	n := len(poolIdle[addr])
	poolMu.Unlock()
	if n != 1 {
		t.Errorf("esperava 1 conexão ociosa no pool, achei %d", n)
	}
}

// Medição PC -> daemon (só com FASTEXEC_BENCH=1 e FASTEXEC_TEST_ADDR): via antiga x via nova, mediana e p95 em ms.
func TestMedicaoViaRede(t *testing.T) {
	if os.Getenv("FASTEXEC_BENCH") == "" {
		t.Skip("defina FASTEXEC_BENCH=1")
	}
	addr := testDaemonAddr(t)
	prefixo := "export CI=1 DEBIAN_FRONTEND=noninteractive PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb NO_COLOR=1; cd \"/tmp\" && "
	med := func(nome string, n int, f func()) {
		ts := make([]float64, n)
		for i := range ts {
			t0 := time.Now()
			f()
			ts[i] = float64(time.Since(t0).Microseconds()) / 1000
		}
		sort.Float64s(ts)
		t.Logf("%-58s mediana %.2f ms  p95 %.2f ms", nome, ts[n/2], ts[n*95/100])
	}
	nova := func(cmd string) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		b := []byte(prefixo + cmd)
		req := make([]byte, 6+len(b))
		binary.LittleEndian.PutUint16(req, MagicExec)
		binary.LittleEndian.PutUint32(req[2:], uint32(len(b)))
		copy(req[6:], b)
		_, _ = c.Write(req)
		_, _ = parseSE(addr)(c)
	}
	for _, cmd := range []string{"ls -d /", "git status --short | head -n 1"} {
		t.Logf("--- %s", cmd)
		med("antiga: conexao nova, SE, bash, cd &&, export", 100, func() { nova(cmd) })
		med("SE com pool (so a conexao reaproveitada)", 100, func() { _, _ = ExecDaemon(addr, prefixo+cmd, 10*time.Second) })
		med("nova: SX (direto se simples, senao bash) com pool", 100, func() {
			q := XRequest{Shell: cmd, Cwd: "/tmp"}
			if a, ok := SimpleArgv(cmd); ok {
				q = XRequest{Argv: a, Cwd: "/tmp"}
			}
			_, _, _ = ExecDaemonX(addr, q, 10*time.Second)
		})
	}
}
