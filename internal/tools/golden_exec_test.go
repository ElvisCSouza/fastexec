package tools

import (
	"fmt"
	"strings"
	"testing"

	"homelab/fastexec/internal/golden"
	"homelab/fastexec/internal/transport"
)

// TestGoldenExecFormat trava o texto que formatExecResult devolve ao LLM (sucesso, erro, truncamento, dicas).
// Entradas fixas, sem rede e sem executar comandos reais.
// Regenerar: go test ./internal/tools -run TestGoldenExecFormat -update
func TestGoldenExecFormat(t *testing.T) {
	cases := []struct {
		name    string
		command string
		res     transport.ExecResult
	}{
		{"success_stdout", "echo hola", transport.ExecResult{ExitCode: 0, Stdout: "hola\n", Duration: 12}},
		{"success_stderr_only", "tool --check", transport.ExecResult{ExitCode: 0, Stderr: "aviso: cache frio\n", Duration: 5}},
		{"success_empty", "true", transport.ExecResult{ExitCode: 0, Duration: 3}},
		{"exit1_no_output", "findstr x y.txt", transport.ExecResult{ExitCode: 1, Duration: 4}},
		{"exit1_search_empty", "grep -r foo .", transport.ExecResult{ExitCode: 1, Duration: 4}},
		{"exit2_stderr", "ls /nope", transport.ExecResult{ExitCode: 2, Stderr: "ls: cannot access '/nope': No such file or directory\n", Duration: 9}},
		{"exit3_no_output", "false-cmd", transport.ExecResult{ExitCode: 3, Duration: 2}},
		{"cmdlet_mismatch_hint", "Get-ChildItem C:\\", transport.ExecResult{ExitCode: 1, Stderr: "Get-ChildItem : o termo 'Get-ChildItem' não é reconhecido como nome de cmdlet", Duration: 7, Local: true}},
		{"cmdlet_mismatch_remote_no_hint", "Get-ChildItem C:\\", transport.ExecResult{ExitCode: 1, Stderr: "Get-ChildItem : o termo 'Get-ChildItem' não é reconhecido como nome de cmdlet", Duration: 7}},
		{"benign_grep_exit1_with_output", "grep foo bar.txt | head", transport.ExecResult{ExitCode: 1, Stdout: "linha foo\n", Duration: 4}},
		{"timeout_partial", "sleep-like-loop", transport.ExecResult{ExitCode: -1, Stdout: "parcial\n", Stderr: "erro\n", Duration: 45000, TimedOut: true}},
		{"timeout_empty", "long-build", transport.ExecResult{ExitCode: -1, Duration: 50000, TimedOut: true}},
		{"truncated_stdout", "dump-big", transport.ExecResult{ExitCode: 0, Stdout: strings.Repeat("linha de teste 0123456789\n", 3000), Duration: 80}},
	}

	var b strings.Builder
	for _, tc := range cases {
		out, err := formatExecResult(tc.command, tc.res)
		fmt.Fprintf(&b, "### %s\ncomando: %s\nexit: %d\n--- saída (%d bytes)\n%s\n--- erro: %v\n\n",
			tc.name, tc.command, tc.res.ExitCode, len(out), out, err)
	}

	golden.Check(t, "exec_format.txt", []byte(b.String()))
}
