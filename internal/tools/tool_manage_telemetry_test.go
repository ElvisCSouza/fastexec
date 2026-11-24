package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A telemetria de tool_manage grava ação e tool resolvidas; nunca params, nem nomes fora do catálogo.
func TestToolManageTelemetry(t *testing.T) {
	a, m, tool := ToolManageTelemetry([]byte(`{"action":"call","tool":"ping","params":"{\"host\":\"local\"}"}`))
	if a != "call" || tool != "ping" || m == "" {
		t.Fatalf("call resolved: action=%q module=%q tool=%q", a, m, tool)
	}

	a, _, tool = ToolManageTelemetry([]byte(`{"action":"call","tool":"nao-existe-xyz","params":"{\"command\":\"secret\"}"}`))
	if a != "call" || tool != "unresolved" {
		t.Fatalf("unknown tool: action=%q tool=%q", a, tool)
	}

	a, m, tool = ToolManageTelemetry([]byte(`{"action":"enable","module":"modulo-inexistente"}`))
	if a != "enable" || m != "unknown" || tool != "" {
		t.Fatalf("unknown module: action=%q module=%q tool=%q", a, m, tool)
	}

	a, _, _ = ToolManageTelemetry([]byte(`{"action":"DROP TABLE x"}`))
	if a != "other" {
		t.Fatalf("free-text action must be bucketed, got %q", a)
	}

	a, _, _ = ToolManageTelemetry([]byte(`not json`))
	if a != "unparsed" {
		t.Fatalf("bad json: got %q", a)
	}

	// Quem chama a telemetria não deve ver os params brutos.
	raw, _ := json.Marshal(map[string]string{"action": "call", "tool": "ping", "params": "{\"command\":\"rm -rf\"}"})
	_, m2, tool2 := ToolManageTelemetry(raw)
	if strings.Contains(m2+tool2, "rm -rf") {
		t.Fatalf("params leaked into telemetry fields")
	}
}
