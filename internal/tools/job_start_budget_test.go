package tools

import (
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

// (a) comando que termina logo, sem wait e sem await: a saída e o rc voltam no mesmo start.
func TestJobStartSemWaitTrazSaidaERc(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())

	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", "console.log('saida-curta')"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "EXITED rc=0") || !strings.Contains(out, "saida-curta") {
		t.Errorf("start curto deveria trazer rc e saída: %q", out)
	}
	if !strings.Contains(out, "[FastExec auto-wait] start waited up to") {
		t.Errorf("a espera automática deve avisar em inglês com a linha [FastExec ...]: %q", out)
	}
	if strings.Contains(out, "esperou") {
		t.Errorf("o texto automático não pode sair em português (AGENTS.md): %q", out)
	}
	if strings.Contains(out, "(showing") {
		t.Errorf("saída curta não deveria avisar corte de linhas: %q", out)
	}
	if strings.Contains(out, "[job] started") {
		t.Errorf("comando que já terminou não deveria voltar como 'started': %q", out)
	}
}

// (a2) comando que termina na janela com mais de 30 linhas: a resposta diz quantas linhas mostrou de quantas existem
// e a chamada que traz o resto.
func TestJobStartMaisDeTrintaLinhasAvisaCorte(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	script := "for(let i=1;i<=60;i++)console.log('linha '+i+' '+'x'.repeat(60))"
	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", script}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "(showing 30 of 60 lines; job(action='tail', job_id='") {
		t.Errorf("corte em 30 linhas deveria ser anunciado com o total: %q", out)
	}
	if !strings.Contains(out, "lines=500) for the rest)") {
		t.Errorf("a resposta deveria dizer a chamada que traz o resto: %q", out)
	}
	if !strings.Contains(out, "linha 60") {
		t.Errorf("o tail mostra as ÚLTIMAS linhas: %q", out)
	}
	if strings.Contains(out, "linha 30\nlinha 31") || strings.Contains(out, "linha 1 ") {
		t.Errorf("só as últimas 30 linhas deveriam aparecer: %q", out)
	}
}

// (b) comando que continua depois do orçamento: o start volta no fim da espera com o id, sem esperar mais.
func TestJobStartLongoVoltaComId(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	old := jobStartBudget
	jobStartBudget = 1 * time.Second
	t.Cleanup(func() { jobStartBudget = old })

	began := time.Now()
	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", "setTimeout(function(){},60000)"}})
	elapsed := time.Since(began)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := jobIDFrom(t, out)
	t.Cleanup(func() { _, _ = transport.LocalJobStop(id) })
	if !strings.HasPrefix(out, "[job] started (local)\nid:      ") {
		t.Errorf("job longo deveria voltar com o id: %q", out)
	}
	if strings.Contains(out, "[FastExec auto-wait]") || strings.Contains(out, "rc=") {
		t.Errorf("job que continua não deveria trazer rc nem aviso de espera resolvida: %q", out)
	}
	if elapsed < jobStartBudget || elapsed > jobStartBudget+5*time.Second {
		t.Errorf("start deveria esperar o orçamento (%s) e não mais: levou %s", jobStartBudget, elapsed)
	}
}
