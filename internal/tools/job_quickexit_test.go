package tools

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

// shortProbe deixa os testes rápidos: a espera automática do start vale 12 s no código de produção.
func shortProbe(t *testing.T) {
	t.Helper()
	old := jobStartBudget
	jobStartBudget = 800 * time.Millisecond
	t.Cleanup(func() { jobStartBudget = old })
}

// jobIDFrom extrai o id da resposta do 'start' local ("id:      job-x").
func jobIDFrom(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "id:"); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("resposta sem id: %q", out)
	return ""
}

// (A1) comando que morre com rc != 0 logo depois de começar: a resposta do start já traz o rc e o log.
func TestJobStartFalhaRapidaTrazRcELog(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	shortProbe(t)

	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", "console.log('morri agora');process.exit(3)"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "[job] EXITED rc=3") {
		t.Errorf("start deveria trazer o rc na mesma resposta: %q", out)
	}
	if !strings.Contains(out, "morri agora") {
		t.Errorf("start deveria trazer o log: %q", out)
	}
	if !strings.Contains(out, "Next: job(action='tail'") {
		t.Errorf("start deveria dizer como ver o log inteiro: %q", out)
	}
	if n := len(out); n > 900 {
		t.Errorf("resposta de falha rápida longa demais (%d B): %q", n, out)
	}
}

// (A1) job que continua rodando: resposta idêntica à de antes (nada de rc nem de log).
func TestJobStartRodandoMantemRespostaAntiga(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	shortProbe(t)

	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", "setTimeout(function(){},60000)"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := jobIDFrom(t, out)
	t.Cleanup(func() { _, _ = transport.LocalJobStop(id) })
	if !strings.HasPrefix(out, "[job] started (local)\nid:      ") {
		t.Errorf("job vivo deveria manter a resposta de sempre: %q", out)
	}
	if strings.Contains(out, "EXITED") || strings.Contains(out, "rc=") {
		t.Errorf("job vivo não deveria falar de rc: %q", out)
	}
}

// (A2) status de um job que morreu rápido e deixou log curto: rc, vida e o log na própria resposta.
func TestJobStatusFalhaRapidaTrazRcELog(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())

	m, _, err := transport.StartLocalJob("", []string{"node", "-e", "console.log('status curto');process.exit(2)"}, "", "")
	if err != nil {
		t.Fatalf("StartLocalJob: %v", err)
	}
	j, finished, werr := transport.LocalJobWait(m.ID, 5*time.Second)
	if werr != nil || !finished {
		t.Fatalf("job não terminou: %v %v", werr, finished)
	}
	_ = j

	st, err := HandleJob(JobArgs{Host: "local", Action: "status", JobID: m.ID})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(st, "EXITED rc=2") || !strings.Contains(st, "status curto") {
		t.Errorf("status de falha rápida deveria trazer rc e log: %q", st)
	}

	tail, err := HandleJob(JobArgs{Host: "local", Action: "tail", JobID: m.ID})
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(tail, "EXITED rc=2") {
		t.Errorf("tail deveria dizer o rc do job que morreu: %q", tail)
	}
}

// (A3) interpretador que não existe no host: além do rc e do log, o mesmo FastHint que o exec já dá.
func TestJobFerramentaAusenteTrazFastHint(t *testing.T) {
	if p, err := exec.LookPath("python"); err == nil {
		t.Skipf("python existe neste host (%s): o caso do hint não acontece", p)
	}
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	shortProbe(t)

	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Command: "python -I server.py"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "[job] EXITED rc=9009") {
		t.Errorf("start deveria trazer o rc 9009 do cmd.exe: %q", out)
	}
	if !strings.Contains(out, pythonMissingHint) {
		t.Errorf("start deveria repetir o FastHint do exec: %q", out)
	}
}

// (B) linha que começa com atribuição POSIX não pode ficar no cmd.exe: 'start=$(date +%s%N); ...' virava o builtin
// START, que engole a linha inteira e só termina no timeout (300 s para um script de 50 s no P3 do bench ciclo1b).
func TestUnixStyleLineAtribuicaoPosix(t *testing.T) {
	p3 := `start=$(date +%s%N); pwsh -NoProfile -File processar.ps1; rc=$?; end=$(date +%s%N); echo "levou $(( (end-start)/1000000 )) ms"`
	if !unixStyleLine(p3) {
		t.Errorf("linha do P3 deveria ir para o bash: %q", p3)
	}
	if !unixStyleLine(`resp=$(curl -s http://127.0.0.1:1/x); echo "$resp"`) {
		t.Errorf("atribuição POSIX com $() deveria ir para o bash")
	}
	// sem nenhum outro sinal Unix a linha continua no cmd.exe, como antes
	if unixStyleLine(`TEMP=C:\tmp`) {
		t.Errorf("atribuição solta, sem sinal Unix, deveria ficar no cmd.exe")
	}
	// linha de cmd de verdade continua no cmd.exe
	if unixStyleLine(`echo %TEMP%`) || unixStyleLine(`dir /s`) {
		t.Errorf("linha de cmd.exe não pode ser desviada para o bash")
	}
}
