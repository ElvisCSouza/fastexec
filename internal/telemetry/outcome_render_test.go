package telemetry

import (
	"errors"
	"strings"
	"testing"
)

// Um banco com uma linha de cada classe e uma linha legada (sem colunas novas, como um banco antigo).
// Exige: validação nunca vira infra; erro desconhecido nunca vira infra; legado não é reclassificado.
func TestOutcomesBlockRendersEachClass(t *testing.T) {
	db := legacyDB(t)
	ok := SuccessOutcome(true)
	lost := LostOutcome()
	panicked := PanicOutcome()
	timedOut := FailureOutcome(errors.New("timeout after 30s"), "")
	invalid := InvalidOutcome()
	guard := FailureOutcome(errors.New("[FastExec Guard] blocked"), "")
	notFound := NotFoundOutcome()
	exit2a := FailureOutcome(errors.New("command failed with exit code 2"), "[exit 2]\nboom")
	exit2b := FailureOutcome(errors.New("command failed with exit code 2"), "[exit 2]")
	exit127 := FailureOutcome(errors.New("command failed with exit code 127"), "")
	unknown := FailureOutcome(errors.New("something odd"), "")

	for _, o := range []Outcome{ok, lost, panicked, timedOut, invalid, guard, notFound, exit2a, exit2b, exit127, unknown} {
		o := o
		if err := writeEntry(db, AuditEntry{Tool: "exec", Success: o.ErrorType == "", Outcome: &o}); err != nil {
			t.Fatalf("writeEntry: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO audit (tool, success) VALUES ('exec', 0)`); err != nil {
		t.Fatal(err)
	}

	s, err := queryOutcomes(db, "")
	if err != nil {
		t.Fatal(err)
	}
	want := OutcomeSummary{NewRows: 11, Success: 1, Infra: 3, Usage: 3, CommandFailed: 3, Unknown: 1, LegacyRows: 1, LegacyFails: 1}
	if s.NewRows != want.NewRows || s.Success != want.Success || s.Infra != want.Infra || s.Usage != want.Usage ||
		s.CommandFailed != want.CommandFailed || s.Unknown != want.Unknown || s.LegacyRows != want.LegacyRows ||
		s.LegacyFails != want.LegacyFails {
		t.Fatalf("resumo = %+v, want %+v", s, want)
	}
	if s.ExitCodes[2] != 2 || s.ExitCodes[127] != 1 || len(s.ExitCodes) != 2 {
		t.Fatalf("exit codes = %v, want {2:2, 127:1}", s.ExitCodes)
	}

	out, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"OUTCOMES (rows with explicit outcome; legacy rows are not reclassified)",
		"Success:                1",
		"Infrastructure failure: 3 (transport_lost, tool_internal, timeout)",
		"Usage / validation:     3 (invalid_params, guard_rejected, not_found)",
		"Command non-zero exit:  3 (exit 2: 2, exit 127: 1)",
		"Unknown error class:    1",
		"Legacy rows (no outcome): 1 (1 failed by success flag)",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("relatório sem a linha %q\n%s", line, out)
		}
	}
}

// Sem nenhuma linha com outcome_schema=1 o bloco não aparece: o relatório fica como antes.
func TestOutcomesBlockHiddenWithoutNewRows(t *testing.T) {
	db := legacyDB(t)
	if _, err := db.Exec(`INSERT INTO audit (tool, success) VALUES ('exec', 0)`); err != nil {
		t.Fatal(err)
	}
	s, err := queryOutcomes(db, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.NewRows != 0 || s.LegacyRows != 1 {
		t.Fatalf("resumo = %+v", s)
	}
	if got := renderOutcomes(s); got != "" {
		t.Fatalf("bloco apareceu sem linhas novas:\n%s", got)
	}
	out, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "OUTCOMES") {
		t.Fatalf("relatório tem OUTCOMES sem linhas novas:\n%s", out)
	}
}
