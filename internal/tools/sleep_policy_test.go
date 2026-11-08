package tools

import "testing"

func TestSleepAllowance(t *testing.T) {
	cases := []struct {
		cmd string
		ok  bool
	}{
		{"echo a; sleep 5; echo b", true},
		{"sleep 10; echo x; sleep 10", false},
		{"until curl -sf http://127.0.0.1:1/h; do sleep 1; done", true},
		{"while true; do sleep 20; done", false},
		{"Start-Sleep 4; Get-Content log.txt", true},
		{"timeout /t 5", false},
	}
	for _, c := range cases {
		_, reason, ok := sleepAllowance(c.cmd)
		if ok != c.ok {
			t.Errorf("%q: ok=%v want %v (%s)", c.cmd, ok, c.ok, reason)
		}
	}
}

func TestTimeoutAfterWrapper(t *testing.T) {
	if !isCoreutilsTimeout("nice -n 10 timeout 40 docker ps") {
		t.Error("timeout after nice must be recognized")
	}
	if checkSleepGuard("sudo -n timeout 30 ls", ExecArgs{}) {
		t.Error("coreutils timeout behind sudo must not trigger the guard")
	}
}
