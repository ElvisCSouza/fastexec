package tools

import (
	"errors"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakePromote replaces the job start seam: it records the request and answers like a started job.
func fakePromote(t *testing.T, got *[]JobArgs, err error) {
	t.Helper()
	orig := promoteSleepJob
	t.Cleanup(func() { promoteSleepJob = orig })
	promoteSleepJob = func(a JobArgs) (string, error) {
		*got = append(*got, a)
		if err != nil {
			return "", err
		}
		return "[job 20261124_000000_abc123 started]", nil
	}
}

// Long pure pauses (sleep, Start-Sleep, timeout, python time.sleep) become a background job: the call returns the
// handle plus the next step, and the same command runs in the job (no retry, no hold).
func TestLongPauseIsPromotedToJob(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(time.Duration) { t.Fatal("a long pause must not hold the call") }
	defer func() { sleepFn = origSleep }()

	cases := []string{
		"sleep 140",
		"timeout 20",
		"timeout /t 25",
		"Start-Sleep 60",
		`python -c "import time; time.sleep(20)"`,
	}
	for _, cmd := range cases {
		var got []JobArgs
		fakePromote(t, &got, nil)
		out, err := HandleExec(ExecArgs{Host: "local", Command: cmd})
		if err != nil {
			t.Errorf("%q: esperava promoção para job, recebeu erro: %v", cmd, err)
			continue
		}
		if !strings.Contains(out, "promoted to a background job") || !strings.Contains(out, "job(action='wait'") {
			t.Errorf("%q: saída sem aviso de promoção e próximo passo: %s", cmd, out)
		}
		if len(got) != 1 || got[0].Action != "start" || got[0].Command != cmd {
			t.Errorf("%q: pedido de job inesperado: %+v", cmd, got)
		}
	}
}

// A failing job start is surfaced, not hidden behind a fake success.
func TestLongPauseFailedPromotionSurfaces(t *testing.T) {
	var got []JobArgs
	fakePromote(t, &got, errors.New("boom"))
	out, err := HandleExec(ExecArgs{Host: "local", Command: "sleep 140"})
	if err == nil || !strings.Contains(err.Error(), "boom") || out != "" {
		t.Errorf("falha de promoção deveria aparecer como erro: out=%q err=%v", out, err)
	}
}

// Short pauses keep running inline, and pings are not pauses: none of them reach the job seam.
func TestPromotionLeavesShortPausesAndPingsAlone(t *testing.T) {
	origSleep := sleepFn
	slept := time.Duration(0)
	sleepFn = func(d time.Duration) { slept += d }
	defer func() { sleepFn = origSleep }()

	var got []JobArgs
	fakePromote(t, &got, nil)
	if _, err := HandleExec(ExecArgs{Host: "local", Command: "sleep 3"}); err != nil {
		t.Fatalf("sleep curto deveria rodar inline: %v", err)
	}
	if slept != 3*time.Second || len(got) != 0 {
		t.Errorf("sleep curto: dormiu %v, promoções %d", slept, len(got))
	}

	for _, cmd := range []string{"ping -n 30 192.0.2.10", "ping -n 30 127.0.0.1", "ping -n 30 localhost"} {
		if _, handled, _ := tryRemediateSleep(cmd, ExecArgs{Command: cmd}); handled {
			t.Errorf("%q: ping não é pausa e não deve ser tratado pela remediação de sleep", cmd)
		}
	}
	if len(got) != 0 {
		t.Errorf("nenhuma promoção esperada para ping, houve %d", len(got))
	}
}

// Real job on Windows: the promotion starts a Job Object, the handle is returned and the job is stopped at the end.
func TestLongPauseRealJobWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("job local Windows")
	}
	t.Setenv("FASTEXEC_JOBS_DIR", t.TempDir())
	out, err := HandleExec(ExecArgs{Host: "local", Command: "sleep 140"})
	if err != nil {
		t.Fatalf("promoção falhou: %v", err)
	}
	id := regexp.MustCompile(`\d{8}_\d{6}_[0-9a-f]{6}`).FindString(out)
	if id == "" {
		t.Fatalf("saída sem job_id: %s", out)
	}
	t.Cleanup(func() { _, _ = HandleJob(JobArgs{Host: "local", Action: "stop", JobID: id}) })
}
