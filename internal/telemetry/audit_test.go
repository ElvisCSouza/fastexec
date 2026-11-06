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
		Host:           "server",
		DurationMs:     42,
		BytesIn:        100,
		BytesOut:       250,
		Success:        true,
		ExitCode:       0,
		CommandSummary: "cat /home/user/workspace/README.md",
	}

	Record(entry)

	// Dá um pequeno tempo para o worker processar a fila assíncrona
	time.Sleep(100 * time.Millisecond)

	report, err := RenderStats(StatsFilter{})
	if err != nil {
		t.Fatalf("RenderStats falhou: %v", err)
	}

	if !strings.Contains(report, "FASTEXEC TELEMETRY & AUDIT") {
		t.Errorf("Relatório não contém cabeçalho esperado: %s", report)
	}
	if !strings.Contains(report, "exec") {
		t.Errorf("Relatório não contém tool 'exec': %s", report)
	}
}

func TestSummarizeCall(t *testing.T) {
	rawArgs := []byte(`{"host":"server","command":"ls -la /home/user"}`)
	host, summary := SummarizeCall("exec", rawArgs)

	if host != "server" {
		t.Errorf("Esperado host 'server', obtido '%s'", host)
	}
	if summary != "ls -la /home/user" {
		t.Errorf("Esperado summary 'ls -la /home/user', obtido '%s'", summary)
	}
}
