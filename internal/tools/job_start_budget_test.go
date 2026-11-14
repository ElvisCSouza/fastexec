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
	if !strings.Contains(out, "[FastExec auto-wait]") {
		t.Errorf("a espera automática deve avisar com a linha [FastExec ...]: %q", out)
	}
	if strings.Contains(out, "[job] started") {
		t.Errorf("comando que já terminou não deveria voltar como 'started': %q", out)
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
