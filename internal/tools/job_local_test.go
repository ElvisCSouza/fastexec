package tools

import (
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

func TestJobLocalPelaTool(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())

	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"node", "-e", "setInterval(function(){console.log('job tick')},1000)"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var id string
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "id:"); ok {
			id = strings.TrimSpace(v)
		}
	}
	if id == "" {
		t.Fatalf("start deveria devolver o id: %q", out)
	}
	t.Cleanup(func() { _, _ = transport.LocalJobStop(id) })

	if st, err := HandleJob(JobArgs{Host: "local", Action: "status", JobID: id}); err != nil || !strings.HasPrefix(st, "RUNNING") {
		t.Errorf("status deveria ser RUNNING: %q %v", st, err)
	}
	time.Sleep(3 * time.Second)
	if tail, err := HandleJob(JobArgs{Host: "local", Action: "tail", JobID: id, Lines: 5}); err != nil || !strings.Contains(tail, "job tick") {
		t.Errorf("tail deveria mostrar os ticks: %q %v", tail, err)
	}
	if ls, err := HandleJob(JobArgs{Host: "local", Action: "list"}); err != nil || !strings.Contains(ls, id) || !strings.Contains(ls, "RUNNING") {
		t.Errorf("list deveria incluir o job: %q %v", ls, err)
	}
	if stop, err := HandleJob(JobArgs{Host: "local", Action: "stop", JobID: id}); err != nil || !strings.Contains(stop, "STOPPED") {
		t.Errorf("stop: %q %v", stop, err)
	}
	if st, _ := HandleJob(JobArgs{Host: "local", Action: "status", JobID: id}); !strings.HasPrefix(st, "STOPPED") {
		t.Errorf("depois do stop: %q", st)
	}
}

func TestJobLocalStartExigeCommandOuArgv(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	if _, err := HandleJob(JobArgs{Host: "local", Action: "start"}); err == nil {
		t.Errorf("sem command nem argv deveria falhar")
	}
	if _, err := HandleJob(JobArgs{Host: "local", Action: "start", Command: "echo x", Argv: []string{"node"}}); err == nil {
		t.Errorf("command e argv juntos deveriam falhar")
	}
	if _, err := HandleJob(JobArgs{Host: "local", Action: "status"}); err == nil {
		t.Errorf("status sem job_id deveria falhar")
	}
}
