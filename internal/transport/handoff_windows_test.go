//go:build windows

package transport

import (
	"strings"
	"testing"
	"time"
)

// No estouro do timeout com HandoffOnTimeout, o exec vira job de fundo: nada morre, a saída até ali volta na hora e
// o resto (com o código de saída) vai para o log do job.
func TestHandoffOnTimeoutKeepsRunning(t *testing.T) {
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	old := HandoffOnTimeout
	HandoffOnTimeout = true
	t.Cleanup(func() { HandoffOnTimeout = old })

	script := "console.log('inicio'); setTimeout(() => { console.log('fim'); process.exit(3) }, 1500)"
	res, err := ExecLocalArgv([]string{"node", "-e", script}, "", "", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID == "" || res.TimedOut {
		t.Fatalf("expected a handoff, got %+v", res)
	}
	if !strings.Contains(res.Stdout, "inicio") || strings.Contains(res.Stdout, "fim") {
		t.Errorf("partial output: %q", res.Stdout)
	}

	info, finished, err := LocalJobWait(res.JobID, 10*time.Second)
	if err != nil || !finished {
		t.Fatalf("job did not finish: %v %+v", err, info)
	}
	deadline := time.Now().Add(2 * time.Second)
	var log string
	for time.Now().Before(deadline) {
		log, _ = TailLocalJob(res.JobID, 20)
		if strings.Contains(log, "[exit 3]") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, want := range []string{"inicio", "fim", "[exit 3]"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

// Sem HandoffOnTimeout (CLI de um comando só), o estouro continua matando a árvore.
func TestTimeoutWithoutHandoffKills(t *testing.T) {
	old := HandoffOnTimeout
	HandoffOnTimeout = false
	t.Cleanup(func() { HandoffOnTimeout = old })

	start := time.Now()
	res, err := ExecLocalArgv([]string{"node", "-e", "setTimeout(() => {}, 10000)"}, "", "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.JobID != "" || time.Since(start) > 3*time.Second {
		t.Fatalf("expected a kill on timeout: %+v after %v", res, time.Since(start))
	}
}
