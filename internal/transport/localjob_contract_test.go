//go:build windows

package transport

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Mesmo job_id e mesmo pedido: replay, sem processo novo (mesmo PID, sem segundo job).
func TestJobIDReplayNaoDuplica(t *testing.T) {
	useTempJobs(t)
	id := fmt.Sprintf("fxc_replay_%d", time.Now().UnixNano())
	marker := id + "_m"

	m, replayed, err := StartLocalJob("", tickJob(marker), "", id)
	if err != nil || replayed {
		t.Fatalf("primeiro start: replayed=%v err=%v", replayed, err)
	}
	m2, replayed, err := StartLocalJob("", tickJob(marker), "", id)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || m2.ID != m.ID || m2.PID != m.PID {
		t.Fatalf("replay deveria devolver o mesmo job: replayed=%v id=%s/%s pid=%d/%d", replayed, m2.ID, m.ID, m2.PID, m.PID)
	}
	jobs, _ := LocalJobList()
	n := 0
	for _, j := range jobs {
		if j.Meta.ID == id {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("esperava 1 registro com o job_id, veio %d", n)
	}

	// mesmo job_id com pedido diferente: conflito, nada é iniciado
	if _, _, err := StartLocalJob("", tickJob(marker+"_x"), "", id); err == nil {
		t.Fatal("job_id reutilizado com pedido diferente deveria ser conflito")
	}
	_, _ = LocalJobStop(id)
}

// Jobs idênticos sem job_id continuam sendo jobs distintos (nunca fundidos por argumentos).
func TestJobsIdenticosSemIDSaoDistintos(t *testing.T) {
	useTempJobs(t)
	marker := fmt.Sprintf("FXTEST_TWIN_%d", time.Now().UnixNano())
	a, ra, err := StartLocalJob("", tickJob(marker), "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, rb, err := StartLocalJob("", tickJob(marker), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if ra || rb || a.ID == b.ID {
		t.Fatalf("dois starts sem job_id devem ser jobs distintos: %s %s", a.ID, b.ID)
	}
	_, _ = LocalJobStop(a.ID)
	_, _ = LocalJobStop(b.ID)
}

// Perda do FastExec (restart) com RUNNING gravado e sem árvore nem rc: OUTCOME_UNKNOWN, nunca "não executou" nem EXITED sem rc.
func TestRestartSemRcViraOutcomeUnknown(t *testing.T) {
	useTempJobs(t)
	dir, err := LocalJobsDir()
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("fxc_lost_%d", time.Now().UnixNano())
	lost := LocalJobMeta{
		ID: id, Command: "x", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(localJobTTL),
		JobObjectName: `Local\FastExec_Job_` + id, LogPath: filepath.Join(dir, id+".log"), State: jobStateRunning,
	}
	if err := saveMeta(dir, lost); err != nil {
		t.Fatal(err)
	}
	st, err := LocalJobStatus(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != jobStateUnknown || st.Meta.ExitCode != nil {
		t.Fatalf("esperava OUTCOME_UNKNOWN sem rc: state=%s rc=%v", st.State, st.Meta.ExitCode)
	}
	// persistido: a segunda consulta devolve o mesmo veredito
	again, _ := LocalJobStatus(id)
	if again.State != jobStateUnknown {
		t.Fatalf("OUTCOME_UNKNOWN deveria ser persistido, veio %s", again.State)
	}
}

// Cancelamento: confirmed na primeira vez (árvore morta), denied quando o job já terminou.
func TestCancelVereditoCoerente(t *testing.T) {
	useTempJobs(t)
	marker := fmt.Sprintf("FXTEST_CANCEL_%d", time.Now().UnixNano())
	m, _, err := StartLocalJob("", tickJob(marker), "", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := LocalJobStop(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Verdict != "confirmed" && c.Verdict != "accepted" {
		t.Fatalf("veredito inesperado para job vivo: %+v", c)
	}
	c2, err := LocalJobStop(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Verdict != "denied" {
		t.Fatalf("job já cancelado deveria dar denied: %+v", c2)
	}
}
