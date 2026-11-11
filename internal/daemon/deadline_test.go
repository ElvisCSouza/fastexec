package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCallDeadlinePerClient(t *testing.T) {
	if d := callDeadline(json.RawMessage(`{"command":"x","_caller":"claude-code#12"}`)); d != 11*time.Minute {
		t.Errorf("claude-code: %v", d)
	}
	for _, a := range []string{`{"_caller":"claude-ai#1"}`, `{}`, `nao-json`} {
		if d := callDeadline(json.RawMessage(a)); d != 55*time.Second {
			t.Errorf("%s: %v", a, d)
		}
	}
}
