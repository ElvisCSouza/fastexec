package transport

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/config"
)

// Integration against a real Linux server-exec with a token (FASTEXEC_TEST_AUTH_ADDR +
// FASTEXEC_TEST_AUTH_TOKEN; skipped without them):
//   - a timeout kills the whole process tree, not only the shell;
//   - a command that leaves something in the background (`&`, nohup) returns when the
//     main process exits, and the daemon says so on stderr.
func TestDaemonTreeKillAndBackground(t *testing.T) {
	addr, tok := os.Getenv("FASTEXEC_TEST_AUTH_ADDR"), os.Getenv("FASTEXEC_TEST_AUTH_TOKEN")
	if addr == "" || tok == "" {
		t.Skip("FASTEXEC_TEST_AUTH_ADDR and FASTEXEC_TEST_AUTH_TOKEN not set")
	}
	t.Cleanup(config.Set(&config.Config{Hosts: map[string]config.Host{"t": {Addr: addr, Token: tok}}}))
	tag := fmt.Sprintf("fxtree%d", time.Now().UnixNano()%1e9)

	// 0. o básico continua igual: código de saída, stdin, stderr, comando inexistente, saída grande
	r, _, err := ExecDaemonX(addr, XRequest{Shell: "echo out; echo err >&2; exit 3"}, 10*time.Second)
	if err != nil || r.ExitCode != 3 || r.Stdout != "out\n" || r.Stderr != "err\n" {
		t.Fatalf("basic: %+v %v", r, err)
	}
	r, _, err = ExecDaemonX(addr, XRequest{Argv: []string{"cat"}, Stdin: "abc"}, 10*time.Second)
	if err != nil || r.Stdout != "abc" {
		t.Fatalf("stdin: %+v %v", r, err)
	}
	r, _, err = ExecDaemonX(addr, XRequest{Argv: []string{"no-such-program-fx"}}, 10*time.Second)
	if err != nil || r.ExitCode != 127 {
		t.Fatalf("not found: %+v %v", r, err)
	}
	r, _, err = ExecDaemonX(addr, XRequest{Shell: "head -c 3000000 /dev/zero | tr '\\0' x"}, 20*time.Second)
	if err != nil || len(r.Stdout) != 3000000 {
		t.Fatalf("big output: len=%d %v", len(r.Stdout), err)
	}

	// 1. background: must return fast, with the [server-exec] note
	t0 := time.Now()
	r, _, err = ExecDaemonX(addr, XRequest{Shell: "exec -a " + tag + "bg tail -f /dev/null & echo pronto"}, 10*time.Second)
	if err != nil {
		t.Fatalf("background: %v", err)
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Fatalf("background command held the call for %v", d)
	}
	if !strings.Contains(r.Stdout, "pronto") || !strings.Contains(r.Stderr, "[server-exec] The command exited, but") {
		t.Fatalf("background result: %+v", r)
	}
	// limpa o processo em segundo plano do teste
	_, _, _ = ExecDaemonX(addr, XRequest{Shell: "pkill -f '^" + tag + "bg' ; true"}, 10*time.Second)

	// 2. timeout: the grandchild must die with the shell
	_, _, err = ExecDaemonX(addr, XRequest{Shell: "exec -a " + tag + "fg tail -f /dev/null | cat"}, 2*time.Second)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	time.Sleep(500 * time.Millisecond)
	r, _, err = ExecDaemonX(addr, XRequest{Shell: "pgrep -f '^" + tag + "fg' || echo none"}, 10*time.Second)
	if err != nil || !strings.Contains(r.Stdout, "none") {
		t.Fatalf("orphan left after timeout: %+v %v", r, err)
	}
}
