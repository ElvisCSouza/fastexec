package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithCaller(t *testing.T) {
	out := withCaller(json.RawMessage(`{"path":"a"}`), "claude-ai#12", "")
	if c, conv := callerOf(out); c != "claude-ai#12" || conv != "" || !strings.Contains(string(out), `"path":"a"`) {
		t.Fatalf("caller not injected: %s", out)
	}
	out = withCaller(json.RawMessage(`{"path":"a"}`), "antigravity-client#3", "conv-1")
	if c, conv := callerOf(out); c != "antigravity-client#3" || conv != "conv-1" {
		t.Fatalf("conversation not injected: %s", out)
	}
	kept := withCaller(json.RawMessage(`{"_caller":"hook#1"}`), "x#2", "")
	if c, _ := callerOf(kept); c != "hook#1" {
		t.Fatalf("existing caller must be kept: %s", kept)
	}
	if string(withCaller(json.RawMessage(`[1]`), "x", "y")) != `[1]` {
		t.Fatal("non-object arguments must pass untouched")
	}
	s := &Server{}
	s.setClient(json.RawMessage(`{"clientInfo":{"name":"local agent/mode"}}`))
	if id := s.callerID(); !strings.HasPrefix(id, "local_agent_mode#") {
		t.Fatalf("caller id: %s", id)
	}
}

func TestConversationOf(t *testing.T) {
	meta := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		_ = json.Unmarshal([]byte(s), &m)
		return m
	}
	ag := meta(`{"antigravity.google/conversation_id":"6f1c2d3e-0000-4000-8000-123456789abc","antigravity.google/root_conversation_id":"x","progressToken":"p1"}`)
	if got := conversationOf(ag); got != "6f1c2d3e-0000-4000-8000-123456789abc" {
		t.Fatalf("antigravity: %q", got)
	}
	if got := conversationOf(meta(`{"session_id":"s-9","thread_id":"t-1"}`)); got != "s-9" {
		t.Fatalf("generic session_id: %q", got)
	}
	if got := conversationOf(meta(`{"progressToken":"p"}`)); got != "" {
		t.Fatalf("progressToken is per call, not a conversation: %q", got)
	}
	if got := conversationOf(nil); got != "" {
		t.Fatal("no meta")
	}
}
