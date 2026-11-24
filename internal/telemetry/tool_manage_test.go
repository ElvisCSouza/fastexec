package telemetry

import "testing"

func TestToolManageSummaryFormat(t *testing.T) {
	if got := ToolManageSummary("call", "docker", "ping"); got != "action=call module=docker tool=ping" {
		t.Fatalf("got %q", got)
	}
	if got := ToolManageSummary("list", "", ""); got != "action=list" {
		t.Fatalf("got %q", got)
	}
}
