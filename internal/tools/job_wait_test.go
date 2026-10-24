package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// wait no host Linux (noteserver, systemd-run --user): com o job ainda ativo no prazo, devolve a orientação em vez de
// deixar o transporte estourar; com o job terminado, devolve o log final.

func TestJobWaitLinuxLimitadoDevolveJobAtivo(t *testing.T) {
	old := jobWaitMax
	jobWaitMax = 2 * time.Second
	t.Cleanup(func() { jobWaitMax = old })

	id := fmt.Sprintf("fxtest_wait_%d", time.Now().UnixNano())
	if _, err := HandleJob(JobArgs{Host: "noteserver", Action: "start", JobID: id, Command: "echo inicio; sleep 60"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = HandleJob(JobArgs{Host: "noteserver", Action: "stop", JobID: id}) })

	start := time.Now()
	out, err := HandleJob(JobArgs{Host: "noteserver", Action: "wait", JobID: id})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if el := time.Since(start); el > 15*time.Second {
		t.Errorf("wait deveria voltar no prazo de 2 s, levou %v", el)
	}
	if !strings.Contains(out, "ainda em execução") || !strings.Contains(out, "'wait' de novo") || strings.Contains(out, waitTimeoutMarker) {
		t.Errorf("mensagem de job ativo inesperada: %q", out)
	}
}

func TestJobWaitLinuxJobTerminadoDevolveLog(t *testing.T) {
	id := fmt.Sprintf("fxtest_fim_%d", time.Now().UnixNano())
	if _, err := HandleJob(JobArgs{Host: "noteserver", Action: "start", JobID: id, Command: "echo saida-do-job-fx"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = HandleJob(JobArgs{Host: "noteserver", Action: "stop", JobID: id}) })

	out, err := HandleJob(JobArgs{Host: "noteserver", Action: "wait", JobID: id})
	if err != nil || !strings.Contains(out, "finalizado") || !strings.Contains(out, "saida-do-job-fx") {
		t.Errorf("wait de job terminado: %v %q", err, out)
	}
}
