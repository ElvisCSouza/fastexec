package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

func TestFormatExecResultAvisaFilhoSolto(t *testing.T) {
	out, err := formatExecResult("pwsh -File worker.ps1 run", transport.ExecResult{Stdout: "iniciado\n", Leftover: 2})
	if err != nil {
		t.Fatalf("sucesso não deveria virar erro: %v", err)
	}
	for _, want := range []string{"iniciado", "[warning] 2 child process(es)", "tool 'job' (action 'start')"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em: %q", want, out)
		}
	}
}

func TestFormatExecResultSemFilhoSoltoNaoAvisa(t *testing.T) {
	out, _ := formatExecResult("echo ok", transport.ExecResult{Stdout: "ok\n"})
	if strings.Contains(out, "[warning]") {
		t.Errorf("aviso indevido: %q", out)
	}
}
