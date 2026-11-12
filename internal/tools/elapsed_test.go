package tools

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

// TestElapsedText: formatação curta da duração, sem dormir (o caso de 2 minutos é medido aqui, não no relógio).
func TestElapsedText(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0.0s"},
		{-time.Second, "0.0s"},
		{1400 * time.Millisecond, "1.4s"},
		{50*time.Second + 300*time.Millisecond, "50.3s"},
		{59949 * time.Millisecond, "59.9s"},
		{2 * time.Minute, "2m00s"},
		{125 * time.Second, "2m05s"},
		{3725 * time.Second, "62m05s"},
	}
	for _, c := range cases {
		if got := elapsedText(c.in); got != c.want {
			t.Errorf("elapsedText(%v) = %q, queria %q", c.in, got, c.want)
		}
	}
}

// TestElapsedFooter: rodapé só a partir do limiar e nunca no timeout (que já cita o próprio tempo).
func TestElapsedFooter(t *testing.T) {
	if got := elapsedFooter(2999*time.Millisecond, false); got != "" {
		t.Errorf("abaixo do limiar deveria ser vazio, veio %q", got)
	}
	if got := elapsedFooter(3*time.Second, false); got != "[elapsed 3.0s]" {
		t.Errorf("no limiar: %q", got)
	}
	if got := elapsedFooter(50*time.Second+300*time.Millisecond, false); got != "[elapsed 50.3s]" {
		t.Errorf("50,3 s: %q", got)
	}
	if got := elapsedFooter(125*time.Second, false); got != "[elapsed 2m05s]" {
		t.Errorf("2m05s: %q", got)
	}
	if got := elapsedFooter(90*time.Second, true); got != "" {
		t.Errorf("timeout não leva rodapé, veio %q", got)
	}
}

var reFooter = regexp.MustCompile(`\[elapsed \d+(\.\d+s|m\d\ds)\]`)

// lastLine devolve a última linha não vazia da saída.
func lastLine(s string) string {
	ls := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	return strings.TrimRight(ls[len(ls)-1], "\r")
}

// TestExecElapsedFooterRealSleep roda um comando que demora ~3,2 s de verdade e confere o rodapé no fim.
func TestExecElapsedFooterRealSleep(t *testing.T) {
	if testing.Short() {
		t.Skip("dorme ~3,2 s")
	}
	out, _ := HandleExec(ExecArgs{Host: "local", Command: "cmd /c ping -n 4 127.0.0.1 >nul"})
	last := lastLine(out)
	if !reFooter.MatchString(last) {
		t.Fatalf("esperava o rodapé como última linha, recebeu: %q (saída: %s)", last, out)
	}
	// o comando não imprime nada: a resposta é só o rodapé, e o número tem de ser ≥ 3 s
	if strings.TrimSpace(out) != last {
		t.Errorf("comando silencioso deveria responder só o rodapé, recebeu: %q", out)
	}
	if s := reFooter.FindString(last); s == "" || strings.HasPrefix(last, "[elapsed 0") || strings.HasPrefix(last, "[elapsed 1") || strings.HasPrefix(last, "[elapsed 2") {
		t.Errorf("duração fora do esperado no rodapé: %q", s)
	}
}

// TestExecNoFooterWhenFast: execução rápida continua byte a byte como antes — nem um byte novo.
func TestExecNoFooterWhenFast(t *testing.T) {
	out, err := HandleExec(ExecArgs{Host: "local", Command: "cmd /c echo ok"})
	if err != nil {
		t.Fatalf("HandleExec: %v", err)
	}
	if strings.Contains(out, "[elapsed") {
		t.Errorf("comando rápido não pode ter rodapé: %s", out)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("saída de comando rápido mudou: %q", out)
	}
}

// TestExecElapsedFooterAfterExit: comando longo que falha — o rodapé vem DEPOIS do "[exit N]".
func TestExecElapsedFooterAfterExit(t *testing.T) {
	if testing.Short() {
		t.Skip("dorme ~3,2 s")
	}
	out, err := HandleExec(ExecArgs{Host: "local", Command: `cmd /c "ping -n 4 127.0.0.1 >nul & exit /b 7"`})
	if err == nil {
		t.Log("exit != 0 sem erro devolvido (código benigno): segue valendo o teste do rodapé")
	}
	iExit := strings.Index(out, "[exit 7]")
	if iExit < 0 {
		t.Fatalf("esperava [exit 7] na saída: %s", out)
	}
	if i := strings.Index(out, "[elapsed"); i < iExit {
		t.Errorf("o rodapé tem de vir depois do [exit 7]: %s", out)
	}
	if !reFooter.MatchString(lastLine(out)) {
		t.Errorf("rodapé não está no fim: %q", lastLine(out))
	}
}

// TestJobRanText: job terminado troca "up <desde a criação>" por "ran <vida>", e o cabeçalho curto ganha a vida.
func TestJobRanText(t *testing.T) {
	end := time.Now()
	j := &transport.LocalJobInfo{
		State: jobStateExited,
		Meta:  transport.LocalJobMeta{CreatedAt: end.Add(-50*time.Second - 300*time.Millisecond), EndedAt: &end},
	}
	if got := jobAgeText(j); got != "ran 50.3s" {
		t.Errorf("jobAgeText = %q, queria \"ran 50.3s\"", got)
	}
	if got := ranSuffix(j); got != " | ran 50.3s" {
		t.Errorf("ranSuffix = %q", got)
	}
	j.State = jobStateRunning
	if got := jobAgeText(j); !strings.HasPrefix(got, "up ") {
		t.Errorf("job rodando continua com 'up': %q", got)
	}
	if got := ranSuffix(j); got != "" {
		t.Errorf("job rodando não tem vida: %q", got)
	}
}
