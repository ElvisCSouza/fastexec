package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"homelab/fastexec/internal/golden"
)

// cliChildArg marca a re-execução do binário de teste como CLI: main() chama os.Exit, então rodamos em processo filho.
const cliChildArg = "__fastexec_cli_golden__"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == cliChildArg {
		os.Args = append([]string{"fastexec"}, os.Args[2:]...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runCLI executa o CLI com os argumentos dados e devolve exit code, stdout e stderr num bloco legível.
// HOME/USERPROFILE apontam para um diretório temporário: o logger não grava no perfil real.
func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	home := t.TempDir()

	cmd := exec.Command(os.Args[0], append([]string{cliChildArg}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "FASTEXEC_DEFAULT_HOST=noteserver")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return fmt.Sprintf("$ fastexec %s\nexit: %d\n--- stdout\n%s--- stderr\n%s\n",
		strings.Join(args, " "), code, stdout.String(), stderr.String())
}

// TestGoldenCLIUsage trava a mensagem de uso, a listagem de comandos e os erros de argumento do CLI.
// Só casos que saem antes de tocar daemon, rede, banco ou comando real.
// Regenerar: go test ./cmd/fastexec -run TestGoldenCLIUsage -update
func TestGoldenCLIUsage(t *testing.T) {
	cases := [][]string{
		{"help"},
		{"frobnicate"},
		{"exec"},
		{"get"},
		{"put", "local.txt"},
		{"sql"},
		{"job", "start"},
		{"daemon", "foo"},
	}

	var b strings.Builder
	for _, args := range cases {
		b.WriteString(runCLI(t, args...))
		b.WriteString("\n")
	}
	golden.Check(t, "cli_usage.txt", []byte(b.String()))
}
