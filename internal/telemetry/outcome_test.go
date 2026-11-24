package telemetry

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestFailureOutcomeMatrix(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		outText  string
		wantType string
		wantCode int
		wantEff  int
	}{
		{"guard", errors.New("[FastExec Guard] blocked"), "", ErrGuardRejected, -1, 0},
		{"exit lead in output", errors.New("command failed with exit code 2"), "[exit 2]\nboom", ErrCommandFailed, 2, 0},
		{"exit from error only", errors.New("command failed with exit code 127"), "", ErrCommandFailed, 127, 0},
		{"timeout", errors.New("timeout after 30s"), "", ErrTimeout, -1, 1},
		{"fatal intercepted", errors.New("boom"), "[FATAL ERROR INTERCEPTED] x", ErrToolInternal, -1, 1},
		{"invalid params", errors.New("invalid parameters: x"), "", ErrInvalidParams, -1, 0},
		{"required param", errors.New("parameter 'path' is required"), "", ErrInvalidParams, -1, 0},
		{"unrecognised stays unknown", errors.New("something odd"), "", ErrUnknown, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := FailureOutcome(c.err, c.outText)
			if o.TransportOK != 1 || o.ToolOK != 0 {
				t.Fatalf("transport/tool = %d/%d, want 1/0", o.TransportOK, o.ToolOK)
			}
			if o.ErrorType != c.wantType {
				t.Fatalf("error_type = %q, want %q", o.ErrorType, c.wantType)
			}
			if o.CommandExitCode != c.wantCode {
				t.Fatalf("command_exit_code = %d, want %d", o.CommandExitCode, c.wantCode)
			}
			if o.EffectUnknown != c.wantEff {
				t.Fatalf("effect_unknown = %d, want %d", o.EffectUnknown, c.wantEff)
			}
		})
	}
}

func TestOutcomeConstructors(t *testing.T) {
	if o := SuccessOutcome(true); o.ToolOK != 1 || o.CommandExitCode != 0 || o.ErrorType != "" {
		t.Fatalf("success with command: %+v", o)
	}
	if o := SuccessOutcome(false); o.CommandExitCode != -1 {
		t.Fatalf("success without command: exit = %d, want -1", o.CommandExitCode)
	}
	if o := LostOutcome(); o.TransportOK != 0 || o.ToolOK != -1 || o.ErrorType != ErrTransportLost || o.EffectUnknown != 1 {
		t.Fatalf("lost: %+v", o)
	}
	if o := PanicOutcome(); o.ErrorType != ErrToolInternal || o.EffectUnknown != 1 || o.TransportOK != 1 {
		t.Fatalf("panic: %+v", o)
	}
	if o := NotFoundOutcome(); o.ErrorType != ErrNotFound || o.EffectUnknown != 0 {
		t.Fatalf("not found: %+v", o)
	}
	if o := InvalidOutcome(); o.ErrorType != ErrInvalidParams || o.EffectUnknown != 0 {
		t.Fatalf("invalid: %+v", o)
	}
}

// Banco antigo (sem colunas novas): setupAuditSchema roda duas vezes; a linha antiga fica como desconhecida.
func TestLegacyDBMigratesWithoutBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		session_id TEXT, tool TEXT NOT NULL, host TEXT, duration_ms INTEGER,
		bytes_in INTEGER, bytes_out INTEGER, est_tokens_in INTEGER, est_tokens_out INTEGER,
		success INTEGER, exit_code INTEGER, error_message TEXT, command_summary TEXT);
		INSERT INTO audit (tool, bytes_out, success) VALUES ('exec', 10, 0);`)
	if err != nil {
		t.Fatal(err)
	}

	setupAuditSchema(db)
	setupAuditSchema(db)

	var schema, transport, tool, effect int
	var et string
	if err := db.QueryRow(`SELECT outcome_schema, transport_ok, tool_ok, effect_unknown, error_type FROM audit WHERE id=1`).
		Scan(&schema, &transport, &tool, &effect, &et); err != nil {
		t.Fatalf("linha antiga após migração: %v", err)
	}
	if schema != 0 || transport != -1 || tool != -1 || effect != -1 || et != "" {
		t.Fatalf("linha antiga foi reclassificada: schema=%d transport=%d tool=%d effect=%d et=%q",
			schema, transport, tool, effect, et)
	}

	o := FailureOutcome(errors.New("command failed with exit code 3"), "[exit 3]")
	if err := writeEntry(db, AuditEntry{Tool: "exec", Success: false, Outcome: &o}); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	s, err := queryOutcomes(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.LegacyRows != 1 || s.LegacyFails != 1 || s.NewRows != 1 || s.CommandFailed != 1 || s.ExitCodes[3] != 1 {
		t.Fatalf("resumo: %+v", s)
	}
}

func TestNewDBWritesExplicitOutcome(t *testing.T) {
	db := legacyDB(t)
	o := LostOutcome()
	if err := writeEntry(db, AuditEntry{Tool: "exec", Outcome: &o}); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	var schema, transport, tool, code, effect int
	var et string
	if err := db.QueryRow(`SELECT outcome_schema, transport_ok, tool_ok, command_exit_code, error_type, effect_unknown FROM audit`).
		Scan(&schema, &transport, &tool, &code, &et, &effect); err != nil {
		t.Fatal(err)
	}
	if schema != 1 || transport != 0 || tool != -1 || code != -1 || et != ErrTransportLost || effect != 1 {
		t.Fatalf("linha nova: schema=%d transport=%d tool=%d code=%d et=%q effect=%d",
			schema, transport, tool, code, et, effect)
	}
}

// Leitor em conexão separada enquanto o escritor grava em WAL: nenhuma falha de "database is locked".
func TestConcurrentWALReadWhileWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	writer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	setupAuditSchema(writer)

	reader, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if _, err := reader.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}

	const n = 200
	var wg sync.WaitGroup
	wg.Add(1)
	var werr error
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			o := FailureOutcome(errors.New("command failed with exit code 1"), "[exit 1]")
			if err := writeEntry(writer, AuditEntry{Tool: "exec", Outcome: &o}); err != nil {
				werr = err
				return
			}
		}
	}()

	var rerr error
	for i := 0; i < 100; i++ {
		if _, err := queryOutcomes(reader, ""); err != nil {
			rerr = err
			break
		}
	}
	wg.Wait()
	if werr != nil {
		t.Fatalf("escrita concorrente: %v", werr)
	}
	if rerr != nil {
		t.Fatalf("leitura concorrente: %v", rerr)
	}

	s, err := queryOutcomes(reader, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.NewRows != n || s.CommandFailed != n {
		t.Fatalf("após escrita: new=%d cmdFailed=%d, want %d", s.NewRows, s.CommandFailed, n)
	}
}
