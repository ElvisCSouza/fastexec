package tools

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Legacy handoff rows: inserted with SQL straight into a minimal audit table, so
// the read-side masking in collectHandoffFacts is what is under test.
const (
	hoTokenCanary = "ghp_CANARYHANDOFF0123456789abcdefgh"
	hoPassCanary  = "password=CANARYHANDOFF2026"
)

func TestLegacyHandoffRowsAreMasked(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE audit (id INTEGER PRIMARY KEY, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		tool TEXT, command_summary TEXT, success INTEGER, error_message TEXT)`); err != nil {
		t.Fatal(err)
	}
	bad := "\xff\xfe bad"
	rows := []struct {
		tool, summary string
		ok            int
		errMsg        string
	}{
		{"write_file", "edit " + bad + " " + hoPassCanary, 1, ""},
		{"exec", "go test ./... " + hoTokenCanary, 0, "build failed " + hoPassCanary + " " + bad},
		{"exec", "git status " + bad, 0, "fatal " + hoTokenCanary},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO audit (tool, command_summary, success, error_message) VALUES (?, ?, ?, ?)`,
			r.tool, r.summary, r.ok, r.errMsg); err != nil {
			t.Fatal(err)
		}
	}

	facts, err := collectHandoffFacts(db)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	all.WriteString(strings.Join(facts.ModifiedFiles, "\n"))
	all.WriteString(strings.Join(facts.RecentValidations, "\n"))
	for _, f := range facts.Failures {
		all.WriteString(f.Command + "\n" + f.Error + "\n")
	}
	text := all.String()
	if text == "" {
		t.Fatal("no facts extracted from legacy rows")
	}
	if !utf8.ValidString(text) {
		t.Error("handoff facts contain invalid UTF-8")
	}
	for _, c := range []string{hoTokenCanary, hoPassCanary, "CANARYHANDOFF"} {
		if strings.Contains(text, c) {
			t.Errorf("canary %q leaked into handoff facts", c)
		}
	}
}
