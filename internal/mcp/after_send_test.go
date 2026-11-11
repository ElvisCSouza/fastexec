package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Falha depois de a chamada ter sido enviada ao daemon: não refaz a chamada no proxy (duplicaria o efeito).
func TestForwardAfterSendDoesNotRerun(t *testing.T) {
	s := NewServer()
	calls := 0
	s.Forwarder = func(name string, rawArgs json.RawMessage) (ToolCallResult, error) {
		calls++
		return ToolCallResult{}, &AfterSendError{Err: errors.New("i/o timeout")}
	}
	res := s.dispatchTool("exec", json.RawMessage(`{"host":"local","argv":["cmd","/c","echo EXECUTOU-LOCAL"]}`))
	if !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "may have run") {
		t.Fatalf("esperava erro orientado, veio %+v", res)
	}
	if strings.Contains(res.Content[0].Text, "EXECUTOU-LOCAL") || calls != 1 {
		t.Errorf("refez a chamada: calls=%d %+v", calls, res)
	}
	// daemon inacessível antes do envio: segue o fallback local
	s.Forwarder = func(string, json.RawMessage) (ToolCallResult, error) {
		return ToolCallResult{}, errors.New("connection refused")
	}
	res = s.dispatchTool("exec", json.RawMessage(`{"host":"local","argv":["cmd","/c","echo EXECUTOU-LOCAL"]}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "EXECUTOU-LOCAL") {
		t.Errorf("fallback local não rodou: %+v", res)
	}
}
