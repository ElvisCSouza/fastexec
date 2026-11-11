package tools

import (
	"testing"
	"time"
)

func TestExecTimeoutsPerClient(t *testing.T) {
	def, max := execTimeouts("claude-code#42", 45*time.Second)
	if def != 120*time.Second || max != 600*time.Second {
		t.Errorf("claude-code: %v %v", def, max)
	}
	for _, c := range []string{"claude-ai#1", "", "local-agent-mode-fastexec#9"} {
		def, max = execTimeouts(c, 45*time.Second)
		if def != 45*time.Second || max != maxClientTimeout {
			t.Errorf("%q deve manter o perfil do Desktop: %v %v", c, def, max)
		}
	}
	if awaitMax("claude-code#1") != 100*time.Second || awaitMax("claude-ai#1") != startAwaitMax {
		t.Error("awaitMax")
	}
}
