package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestTelemetryRecordAndStats(t *testing.T) {
	Init()

	entry := AuditEntry{
		Tool:           "exec",
		Host:           "noteserver",
		DurationMs:     42,
		BytesIn:        100,
		BytesOut:       250,
		Success:        true,
		ExitCode:       0,
		CommandSummary: "cat /home/elvis/workspace/LLM.md",
	}

	Record(entry)

	// Dá um pequeno tempo para o worker processar a fila assíncrona
	time.Sleep(100 * time.Millisecond)

	report, err := RenderStats(StatsFilter{})
	if err != nil {
		t.Fatalf("RenderStats falhou: %v", err)
	}

	if !strings.Contains(report, "TELEMETRIA & AUDITORIA FASTEXEC") {
		t.Errorf("Relatório não contém cabeçalho esperado: %s", report)
	}
	if !strings.Contains(report, "exec") {
		t.Errorf("Relatório não contém tool 'exec': %s", report)
	}
}

func TestSummarizeCall(t *testing.T) {
	rawArgs := []byte(`{"host":"noteserver","command":"ls -la /home/elvis"}`)
	host, summary := SummarizeCall("exec", rawArgs)

	if host != "noteserver" {
		t.Errorf("Esperado host 'noteserver', obtido '%s'", host)
	}
	if summary != "ls -la /home/elvis" {
		t.Errorf("Esperado summary 'ls -la /home/elvis', obtido '%s'", summary)
	}
}
