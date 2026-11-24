package telemetry

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"homelab/fastexec/internal/measure"
)

// Canaries are fake values in a known shape. They are inserted straight into the
// tables with SQL (legacy rows: not through writeEntry), so the tests cover the
// read path that must mask rows written before masking existed.
const (
	tokenCanary = "ghp_CANARYTOKEN0123456789abcdefghij"
	passCanary  = "password=CANARYPASS2026x"
	devCanary   = `Device("bf0000000000", "192.0.2.10", "CANARYLOCALKEY01")`
	badBytes    = "\xff\xfe bad"
)

// legacyDB opens a fresh database with the current schema.
func legacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	setupAuditSchema(db)
	return db
}

// legacyExec inserts a raw row; values are bound as parameters so invalid bytes
// reach the database unchanged.
func legacyExec(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func assertClean(t *testing.T, where, text string, canaries ...string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Errorf("%s: invalid UTF-8 in output", where)
	}
	for _, c := range canaries {
		if strings.Contains(text, c) {
			t.Errorf("%s: canary %q leaked", where, c)
		}
	}
}

func TestLegacyStatsRowsAreMasked(t *testing.T) {
	db := legacyDB(t)
	legacyExec(t, db, `INSERT INTO audit (tool, host, command_summary, error_message, success, bytes_out, bytes_in, est_tokens_out, duration_ms, exit_code)
		VALUES ('exec', 'srv', ?, '', 1, 99999, 10, 5, 10, 0)`, "curl -H "+tokenCanary+" "+devCanary+" "+badBytes)
	legacyExec(t, db, `INSERT INTO audit (tool, host, command_summary, error_message, success, bytes_out, bytes_in, est_tokens_out, duration_ms, exit_code)
		VALUES (?, ?, 'login', ?, 0, 10, 10, 5, 10, 1)`, "exec "+badBytes, "srv "+badBytes, "auth failed "+passCanary)

	out, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LARGEST RETURNS") || !strings.Contains(out, "LATEST ERRORS") {
		t.Fatalf("sections missing from stats:\n%s", out)
	}
	assertClean(t, "stats", out, tokenCanary, "CANARYLOCALKEY01", "CANARYPASS2026x")
}

func TestLegacySavingsRowsAreMaskedAndCutOnRunes(t *testing.T) {
	db := legacyDB(t)
	long := strings.Repeat("é", 4500)   // 4500 runes, 9000 bytes: a byte cut would split a rune
	longErr := strings.Repeat("ß", 700) // 700 runes, 1400 bytes
	aid := legacyExec(t, db, `INSERT INTO audit (tool, host, client, marks, command_summary, error_message, success, bytes_out, bytes_in, est_tokens_out, duration_ms, exit_code)
		VALUES (?, ?, ?, 'Hint:ab12,Guard:cd34', ?, ?, 0, 10, 10, 5, 10, 1)`,
		"exec "+badBytes, "srv "+badBytes, "cli "+badBytes, long, longErr)
	legacyExec(t, db, `INSERT INTO saving (audit_id, cat, bytes, trips, ms, exact, note) VALUES (?, 'clean', 500, 1, 0, 1, ?)`, aid, "removed "+passCanary)

	r, err := buildSavingsReport(db, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Recent) == 0 {
		t.Fatal("no recent savings event")
	}
	ev := r.Recent[0]
	if !utf8.ValidString(ev.FullTarget) || !utf8.ValidString(ev.Err) || !utf8.ValidString(ev.Target) {
		t.Error("savings text fields are not valid UTF-8")
	}
	if n := utf8.RuneCountInString(ev.FullTarget); n != 4001 || !strings.HasSuffix(ev.FullTarget, "…") {
		t.Errorf("FullTarget: %d runes, want 4000 + suffix", n)
	}
	if n := utf8.RuneCountInString(ev.Err); n != 601 || !strings.HasSuffix(ev.Err, "…") {
		t.Errorf("Err: %d runes, want 600 + suffix", n)
	}
	if n := utf8.RuneCountInString(ev.Target); n != 91 {
		t.Errorf("Target: %d runes, want 90 + suffix", n)
	}
	if strings.Contains(ev.Host+ev.Tool+ev.Client, "\xff") {
		t.Error("Host/Tool/Client kept invalid bytes")
	}
	if ev.Marks != "Hint:ab12,Guard:cd34" {
		t.Errorf("marks changed: %q", ev.Marks)
	}
	b, _ := json.Marshal(r)
	assertClean(t, "savings report", string(b), passCanary, "CANARYPASS2026x")
}

func TestLegacyMeasureSourceIsMaskedAndKeepsIdentity(t *testing.T) {
	db := legacyDB(t)
	legacyExec(t, db, `INSERT INTO audit (tool, host, client, marks, command_summary, conversation, success, bytes_out, bytes_in, est_tokens_out, duration_ms, exit_code, truncated)
		VALUES (?, 'srv', ?, 'Hint:ab12,Guard:cd34', ?, ?, 1, 10, 10, 5, 10, 0, 0)`,
		"exec "+badBytes, "chan "+passCanary, "git diff --raw "+devCanary, "conv "+tokenCanary)
	legacyExec(t, db, `INSERT INTO mark_catalog (mark, template, first_seen) VALUES ('Hint:ab12', ?, 0)`, "Try again. "+tokenCanary+" "+badBytes)

	src := sqlSource{db}
	calls, err := src.Calls(measure.Window{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls: %d, want 1", len(calls))
	}
	c := calls[0]
	assertClean(t, "Calls", c.Summary+c.Conversation+c.Channel+c.Tool, tokenCanary, passCanary, "CANARYLOCALKEY01")
	// detectors read Summary (--raw) and Marks (identity): both must survive masking
	if !strings.Contains(c.Summary, "--raw") {
		t.Errorf("--raw flag lost from summary: %q", c.Summary)
	}
	if c.Marks != "Hint:ab12,Guard:cd34" {
		t.Errorf("marks changed: %q", c.Marks)
	}

	tpls, err := src.MarkTemplates()
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := tpls["Hint:ab12"]
	if !ok {
		t.Fatal("mark key Hint:ab12 changed or missing")
	}
	assertClean(t, "MarkTemplates", tpl, tokenCanary)

	r, err := measure.Build(src, measure.Window{}, measure.DefaultPolicy(), measure.DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	assertClean(t, "stats --measure", r.Render(), tokenCanary, "CANARYLOCALKEY01", passCanary)
}

// TestCutRunesBoundary checks the savings cuts at their exact limits with
// multibyte runes (3 and 4 bytes): no cut at or below the limit, and a cut of
// exactly the limit plus the suffix above it. The byte length is never used.
func TestCutRunesBoundary(t *testing.T) {
	for _, unit := range []string{"€", "𝔸"} {
		for _, limit := range []int{4000, 600} {
			for _, n := range []int{limit - 1, limit, limit + 1} {
				in := strings.Repeat(unit, n)
				got := cutRunes(in, limit, "…")
				if !utf8.ValidString(got) {
					t.Fatalf("unit %q limit %d n %d: invalid UTF-8", unit, limit, n)
				}
				wantRunes := n
				if n > limit {
					wantRunes = limit + 1 // limit runes plus the one-rune suffix
				}
				if c := utf8.RuneCountInString(got); c != wantRunes {
					t.Errorf("unit %q limit %d n %d: got %d runes, want %d", unit, limit, n, c, wantRunes)
				}
				if n > limit && !strings.HasSuffix(got, "…") {
					t.Errorf("unit %q limit %d n %d: suffix missing", unit, limit, n)
				}
			}
		}
	}
}
