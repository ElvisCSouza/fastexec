package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolManageList(t *testing.T) {
	out, err := HandleToolManage(json.RawMessage(`{"action": "list"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "docker") || !strings.Contains(out, "sql") || !strings.Contains(out, "hosts") {
		t.Fatalf("list output missing modules: %s", out)
	}
}

func TestToolManageSearch(t *testing.T) {
	out, err := HandleToolManage(json.RawMessage(`{"action": "search", "query": "docker"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "docker_ps") || !strings.Contains(out, "docker_logs") {
		t.Fatalf("search output missing docker tools: %s", out)
	}

	outSql, err := HandleToolManage(json.RawMessage(`{"action": "search", "query": "duckdb"}`))
	if err != nil {
		t.Fatalf("search duckdb failed: %v", err)
	}
	if !strings.Contains(outSql, "sql") {
		t.Fatalf("search output missing sql tool: %s", outSql)
	}

	outPing, err := HandleToolManage(json.RawMessage(`{"action": "search", "query": "ping"}`))
	if err != nil {
		t.Fatalf("search ping failed: %v", err)
	}
	if !strings.Contains(outPing, "ping") {
		t.Fatalf("search output missing ping tool: %s", outPing)
	}
}

func TestToolManageEnableDisable(t *testing.T) {
	notified := make(chan struct{}, 2)
	SetOnListChanged(func() { notified <- struct{}{} })
	t.Cleanup(func() { SetOnListChanged(nil) })

	out, err := HandleToolManage(json.RawMessage(`{"action": "enable", "module": "docker"}`))
	if err != nil || !strings.Contains(out, "enabled (") {
		t.Fatalf("enable failed: (%q, %v)", out, err)
	}

	out, err = HandleToolManage(json.RawMessage(`{"action": "disable", "module": "docker"}`))
	if err != nil || !strings.Contains(out, "disabled") {
		t.Fatalf("disable failed: (%q, %v)", out, err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-notified:
		case <-time.After(time.Second):
			t.Fatal("list_changed notification was not delivered")
		}
	}
}

func TestToolManageCallDirect(t *testing.T) {
	// Chamada direta da tool sem estar ativa no catálogo (Tool RAG pattern)
	out, err := HandleToolManage(json.RawMessage(`{"action": "call", "tool": "ping", "params": "{\"host\": \"local\"}"}`))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	_ = out
}
