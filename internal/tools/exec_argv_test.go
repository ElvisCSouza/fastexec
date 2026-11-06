package tools

import (
	"strings"
	"testing"
	"time"
)

func TestExecArgvLiteral(t *testing.T) {
	lit := "a > b && %TEMP%"
	out, err := HandleExec(ExecArgs{Host: "local", Argv: []string{"node", "-e", "console.log(process.argv[1])", lit}})
	if err != nil {
		t.Fatalf("falhou: %v (%s)", err, out)
	}
	if strings.TrimSpace(out) != lit {
		t.Errorf("argumento alterado: %q", out)
	}
}

func TestExecCommandEArgvJuntosSaoRejeitados(t *testing.T) {
	_, err := HandleExec(ExecArgs{Host: "local", Command: "echo x", Argv: []string{"node"}})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Errorf("esperava erro de exclusividade, veio %v", err)
	}
	_, err = HandleExec(ExecArgs{Host: "local"})
	if err == nil {
		t.Errorf("esperava erro quando nenhum dos dois é informado")
	}
}

func TestExecArgvSleepGuard(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(d time.Duration) {}
	defer func() { sleepFn = origSleep }()

	// Sleep puro <= 15s é auto-remediado
	for _, argv := range [][]string{
		{"sleep", "5"},
		{"timeout", "/t", "5"},
	} {
		out, err := HandleExec(ExecArgs{Host: "local", Argv: argv})
		if err != nil || !strings.Contains(out, "FastExec Auto-Remediation") {
			t.Errorf("argv %v deveria ser auto-remediado, veio erro %v, out %q", argv, err, out)
		}
	}

	// Comandos com encadeamento ou shells que usam sleep continuam barrados pelo guard
	for _, argv := range [][]string{
		{"bash", "-c", "sleep 3 && ls"},
		{"cmd", "/c", "timeout /t 5"},
	} {
		if _, err := HandleExec(ExecArgs{Host: "local", Argv: argv}); err == nil || !strings.Contains(err.Error(), "FastExec Guard") {
			t.Errorf("argv %v deveria ser barrado pelo guard, veio %v", argv, err)
		}
	}
	// texto de código com a palavra "timeout 5" num argv para node NÃO é shell: não pode dar falso positivo
	out, err := HandleExec(ExecArgs{Host: "local", Argv: []string{"node", "-e", "console.log('config: timeout 5')"}})
	if err != nil || !strings.Contains(out, "timeout 5") {
		t.Errorf("falso positivo do guard em argv de node: %v %q", err, out)
	}
}

func TestExecTimeoutFormatado(t *testing.T) {
	out, err := HandleExec(ExecArgs{
		Host:      "local",
		Argv:      []string{"node", "-e", "console.log('antes do corte');setTimeout(function(){},30000)"},
		TimeoutMs: 1500,
	})
	if err == nil {
		t.Fatalf("timeout deveria ser erro, veio %q", out)
	}
	if !strings.HasPrefix(out, "[timeout after ") || !strings.Contains(out, "process tree killed") {
		t.Errorf("formato do timeout inesperado: %q", out)
	}
	if !strings.Contains(out, "Partial output before the cut:") || !strings.Contains(out, "antes do corte") {
		t.Errorf("faltou a saída parcial: %q", out)
	}
}

func TestExecHintPowerShellComMensagemLegivel(t *testing.T) {
	out, err := HandleExec(ExecArgs{Host: "local", Command: "Get-ChildItem comando_x_fastexec"})
	if err == nil {
		t.Fatalf("deveria falhar: %q", out)
	}
	if strings.ContainsRune(out, '\ufffd') {
		t.Errorf("mensagem corrompida: %q", out)
	}
	if !strings.Contains(out, "[FastExec Hint]") {
		t.Errorf("o hint do PowerShell deveria disparar: %q", out)
	}
}

func TestExecCwdInvalido(t *testing.T) {
	_, err := HandleExec(ExecArgs{Host: "local", Command: "echo x", Cwd: `C:\nao\existe\fx`})
	if err == nil || !strings.Contains(err.Error(), "cwd does not exist") {
		t.Errorf("esperava erro claro de cwd: %v", err)
	}
}
