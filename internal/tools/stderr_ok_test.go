package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

// Exit 0 with an error on stderr (pwsh -Command - keeps going after a throw)
// must not hide the error.
func TestExit0StderrErrorIsShown(t *testing.T) {
	out, err := formatExecResult("pwsh -NoProfile -Command -", transport.ExecResult{
		Stdout: "5: // @version 1.3.15\n",
		Stderr: "Exception: nao achei: x\n",
	})
	if err != nil {
		t.Fatalf("exit 0 must not become an error: %v", err)
	}
	if !strings.Contains(out, "5: // @version 1.3.15") || !strings.Contains(out, "[stderr]\nException: nao achei: x") || !strings.Contains(out, "Exit code was 0") {
		t.Fatalf("stderr error hidden: %q", out)
	}
}

func TestExit0BenignStderrStaysHidden(t *testing.T) {
	out, _ := formatExecResult("git pull", transport.ExecResult{
		Stdout: "Already up to date.\n",
		Stderr: "From origin\n * branch main -> FETCH_HEAD\n",
	})
	if strings.Contains(out, "[stderr]") {
		t.Fatalf("benign stderr must not be appended: %q", out)
	}
}
