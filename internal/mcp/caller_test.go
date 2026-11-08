package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithCaller(t *testing.T) {
	out := withCaller(json.RawMessage(`{"path":"a"}`), "claude-ai#12")
	if callerOf(out) != "claude-ai#12" || !strings.Contains(string(out), `"path":"a"`) {
		t.Fatalf("caller not injected: %s", out)
	}
	kept := withCaller(json.RawMessage(`{"_caller":"hook#1"}`), "x#2")
	if callerOf(kept) != "hook#1" {
		t.Fatalf("existing caller must be kept: %s", kept)
	}
	if string(withCaller(json.RawMessage(`[1]`), "x")) != `[1]` {
		t.Fatal("non-object arguments must pass untouched")
	}
	s := &Server{}
	s.setClient(json.RawMessage(`{"clientInfo":{"name":"local agent/mode"}}`))
	if id := s.callerID(); !strings.HasPrefix(id, "local_agent_mode#") {
		t.Fatalf("caller id: %s", id)
	}
}
