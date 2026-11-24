package mcp

import (
	"errors"
	"strings"
	"testing"

	"homelab/fastexec/internal/telemetry"
)

// tool_manage: resumo com ação/tool resolvidas, sem hash de argumentos; "not found" vira uso (not_found).
func TestToolManageTelemetryPath(t *testing.T) {
	raw := []byte(`{"action":"call","tool":"ping","params":"{\"command\":\"secret-cmd\"}"}`)
	_, summary := summarizeCall("tool_manage", raw)
	if !strings.HasPrefix(summary, "action=call") || !strings.Contains(summary, "tool=ping") || strings.Contains(summary, "secret") {
		t.Fatalf("summary: %q", summary)
	}
	if h := argsHashFor("tool_manage", raw); h != "" {
		t.Fatalf("tool_manage must not record args hash, got %q", h)
	}
	if h := argsHashFor("exec", []byte(`{"command":"ls"}`)); h == "" {
		t.Fatalf("other tools keep their args hash")
	}

	o := toolOutcome("tool_manage", errors.New("module 'x' not found. Use tool_manage(action='list')"), "")
	if o.ErrorType != telemetry.ErrNotFound {
		t.Fatalf("not found: error_type=%q", o.ErrorType)
	}
}
