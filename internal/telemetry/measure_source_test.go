package telemetry

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/measure"
)

func TestMeasureBytesPerTokenIgual(t *testing.T) {
	if measure.BytesPerToken != BytesPerToken {
		t.Fatalf("measure.BytesPerToken %v diverge de telemetry.BytesPerToken %v", measure.BytesPerToken, BytesPerToken)
	}
}

func i64(v int64) *int64 { return &v }

// O adaptador lê o banco real (schema e migração incluídos) e o domínio monta o relatório.
func TestMeasureSobreBancoReal(t *testing.T) {
	db := openTestDB(t)
	entries := []AuditEntry{
		{Tool: "exec", Host: "local", Success: true, EstTokensOut: 1000, DurationMs: 120, CmdMs: i64(100), Client: "c#1", CommandSummary: "go test",
			ArgsHash: "h1", Truncated: true, Marks: "Hint:aa", Savings: []Saving{{Cat: CatCut, Bytes: 7000, Exact: true}}},
		{Tool: "exec", Host: "local", Success: true, EstTokensOut: 200, DurationMs: 50, CmdMs: i64(40), Client: "c#1", CommandSummary: "go test",
			ArgsHash: "h1", Marks: "Hint:aa", Savings: []Saving{{Cat: CatSlice, Bytes: 3500, Exact: true}}},
		{Tool: "read_file", Host: "local", Success: true, EstTokensOut: 10, DurationMs: 5, Client: "c#2"}, // sem comando: cmd_ms = -1
	}
	for _, e := range entries {
		if err := writeEntry(db, e); err != nil {
			t.Fatal(err)
		}
	}
	r, err := measure.Build(sqlSource{db}, measure.Window{Today: true}, measure.DefaultPolicy(), measure.DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	if r.Calls != 3 || r.Channels != 2 {
		t.Fatalf("chamadas/canais: %+v", r)
	}
	if r.Overhead.N != 2 || r.Overhead.MaxMs != 20 {
		t.Errorf("overhead: %+v", r.Overhead)
	}
	if r.Savings.MeasuredTokens != 2000 || r.Savings.EstimatedTokens != 1000 {
		t.Errorf("economia: %+v", r.Savings)
	}
	for _, h := range r.Harm {
		if h.Detector == "retry_after_cut" && h.Count != 1 {
			t.Errorf("retry_after_cut: %d, esperado 1 (mesma chamada repetida depois do corte)", h.Count)
		}
	}
}

// Banco criado antes da coluna cmd_ms: a migração acrescenta com -1 (não medido) e não perde linhas.
func TestMigracaoCmdMs(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO audit (tool, success) VALUES ('exec', 1)`); err != nil {
		t.Fatal(err)
	}
	var v int64
	if err := db.QueryRow(`SELECT cmd_ms FROM audit ORDER BY id DESC LIMIT 1`).Scan(&v); err != nil || v != measure.CmdUnknown {
		t.Fatalf("cmd_ms padrão: %d (%v), esperado -1", v, err)
	}
	setupAuditSchema(db) // idempotente: rodar de novo não falha nem duplica a coluna
	if err := db.QueryRow(`SELECT cmd_ms FROM audit ORDER BY id DESC LIMIT 1`).Scan(&v); err != nil || v != measure.CmdUnknown {
		t.Fatalf("depois da 2ª migração: %d (%v)", v, err)
	}
}

func TestRenderMeasureTexto(t *testing.T) {
	db := openTestDB(t)
	if err := writeEntry(db, AuditEntry{Tool: "exec", Success: true, EstTokensOut: 10, Client: "c#1"}); err != nil {
		t.Fatal(err)
	}
	r, err := measure.Build(sqlSource{db}, measure.Window{}, measure.DefaultPolicy(), measure.DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Render(); !strings.Contains(out, "todo o histórico") {
		t.Errorf("saída: %s", out)
	}
}
