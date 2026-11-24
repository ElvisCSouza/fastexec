package tools

import (
	"database/sql"
	"os"
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

// handoff save grava o texto do modelo (notes/pending) sanitizado: canário mascarado,
// texto legítimo e unicode sobrevivem.
func TestHandoffSaveMasksNotesAndPending(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := handleHandoffSave(tmpDir, HandoffArgs{
		Action:  "save",
		Topic:   "mask-test",
		Notes:   "deploy feito com " + hoTokenCanary + " e acentuação correta",
		Pending: "rotacionar " + hoPassCanary + " amanhã",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "latest.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !utf8.ValidString(content) {
		t.Error("handoff salvo não é UTF-8 válido")
	}
	for _, c := range []string{hoTokenCanary, hoPassCanary, "CANARYHANDOFF"} {
		if strings.Contains(content, c) {
			t.Errorf("canário %q vazou no handoff salvo", c)
		}
	}
	if !strings.Contains(content, "[REDACTED") {
		t.Error("placeholder de redação ausente no handoff salvo")
	}
	for _, want := range []string{"e acentuação correta", "amanhã"} {
		if !strings.Contains(content, want) {
			t.Errorf("texto legítimo %q não sobreviveu à sanitização", want)
		}
	}
}

// Arquivo legado gravado sem máscara: latest e search mascaram na leitura, sem reescrever o arquivo.
func TestHandoffReadMasksLegacyFileWithoutRewriting(t *testing.T) {
	tmpDir := t.TempDir()
	legacy := "# Handoff legado\n\nuse token " + hoTokenCanary + " no deploy\ncom " + hoPassCanary + " e erro \x80 aqui\nmarker-legado\n"
	name := "2026-01-01-legado-handoff-velho.md"
	legacyPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(legacyPath, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := handleHandoffLatest(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(got) {
		t.Error("latest não retornou UTF-8 válido para arquivo legado")
	}
	for _, c := range []string{hoTokenCanary, hoPassCanary, "CANARYHANDOFF"} {
		if strings.Contains(got, c) {
			t.Errorf("canário %q vazou na leitura do legado", c)
		}
	}
	if !strings.Contains(got, "[REDACTED") || !strings.Contains(got, "marker-legado") {
		t.Errorf("leitura do legado perdeu placeholder ou texto legítimo: %q", got)
	}

	// O arquivo datado legado não é reescrito...
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != legacy {
		t.Error("arquivo legado foi reescrito")
	}
	// ...mas o ponteiro latest.md criado a partir dele é gravado já mascarado.
	ptr, err := os.ReadFile(filepath.Join(tmpDir, "latest.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ptr), "CANARYHANDOFF") {
		t.Error("ponteiro latest.md criado a partir do legado contém canário")
	}

	res, err := handleHandoffSearch(tmpDir, "marker-legado", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{hoTokenCanary, hoPassCanary, "CANARYHANDOFF"} {
		if strings.Contains(res, c) {
			t.Errorf("canário %q vazou no search do legado", c)
		}
	}
	if !strings.Contains(res, "marker-legado") {
		t.Error("search não encontrou o texto legítimo do legado")
	}
}
