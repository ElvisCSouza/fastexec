package mcp

import (
	"strings"
	"testing"
)

func TestToolsListTimeoutProfilePerClient(t *testing.T) {
	desk := string(toolsList("claude-ai"))
	code := string(toolsList("claude-code"))
	for _, want := range []string{"default limit 45 s", "max 50000", "over ~30 s", "within 40 s"} {
		if !strings.Contains(desk, want) {
			t.Errorf("Desktop sem %q", want)
		}
	}
	for _, want := range []string{"default limit 2 min", "default 120000, max 600000", "over ~2 min", "within 100 s"} {
		if !strings.Contains(code, want) {
			t.Errorf("Claude Code sem %q", want)
		}
	}
	if strings.Contains(code, "{") && strings.Contains(code, "{exec_") || strings.Contains(desk, "{await_s}") {
		t.Error("placeholder sem substituir")
	}
}
