package telemetry

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDocColumnsAndLinks(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "doc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupAuditSchema(db)
	e := AuditEntry{Tool: "read_file", Host: "noteserver", Success: true, Doc: "~/w/LLM.md", DocHow: "first",
		DocLinks: []string{"~/w/a.md", "~/w/b.md"}}
	if err := writeEntry(db, e); err != nil {
		t.Fatal(err)
	}
	if err := writeEntry(db, e); err != nil { // same links again: upsert, no error
		t.Fatal(err)
	}
	if err := writeEntry(db, AuditEntry{Tool: "read_file", Host: "noteserver", Success: true, Doc: "~/w/a.md", DocFrom: "~/w/LLM.md", DocHow: "link"}); err != nil {
		t.Fatal(err)
	}
	var links int
	db.QueryRow(`SELECT COUNT(*) FROM doc_links`).Scan(&links)
	if links != 2 {
		t.Fatalf("doc_links = %d, want 2", links)
	}
	var from, how string
	db.QueryRow(`SELECT doc_from, doc_how FROM audit WHERE doc='~/w/a.md'`).Scan(&from, &how)
	if from != "~/w/LLM.md" || how != "link" {
		t.Fatalf("got %q %q", from, how)
	}
}
