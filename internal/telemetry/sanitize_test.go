package telemetry

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"homelab/fastexec/internal/corpus"
)

// Canary values are fake. They only have to match the shapes the masking targets.
var canaries = []string{
	"CANARY-bearer-7f3a9c1e2b4d",
	"ghp_CANARYaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"CANARY-dbpass-91xq",
	"CANARY-apikey-55zk",
	"CANARY-url-user-pw",
	"CANARY-devkey-3hw8",
	"CANARY-conn-pw-77",
	"CANARY-cookie-0p9",
}

func canaryEntry() AuditEntry {
	cmd := strings.Join([]string{
		`curl -H "Authorization: Bearer ` + canaries[0] + `" https://api.example.invalid`,
		`DB_PASSWORD=` + canaries[2],
		`--api-key ` + canaries[3],
		`git clone https://svc:` + canaries[4] + `@git.example.invalid/repo.git`,
		`token=` + canaries[1],
		`Password=` + canaries[6] + `;Server=db`,
		`device_key=` + canaries[5],
		`Cookie: session=` + canaries[7],
	}, " ; ")
	return AuditEntry{
		Tool: "exec", Host: "noteserver", Success: false, ErrorMessage: "auth failed for " + canaries[1],
		CommandSummary: cmd, Marks: "Hint:ab12,Guard:cd34",
		Doc: "~/w/LLM.md", DocLinks: []string{"~/w/a.md"},
		MarkDefs: []MarkDef{{Mark: "Hint:ab12", Template: "retry with Authorization: Bearer " + canaries[0]}},
		Savings:  []Saving{{Cat: "exec", Note: "cut output of device_key=" + canaries[5]}},
	}
}

func dumpAllText(t *testing.T, db *sql.DB) []string {
	t.Helper()
	trows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for trows.Next() {
		var n string
		trows.Scan(&n)
		tables = append(tables, n)
	}
	trows.Close()
	var out []string
	for _, tb := range tables {
		rows, err := db.Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				switch x := v.(type) {
				case string:
					out = append(out, x)
				case []byte:
					out = append(out, string(x))
				}
			}
		}
		rows.Close()
	}
	return out
}

func TestCanarySecretsNotPersisted(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "canary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupAuditSchema(db)
	if err := writeEntry(db, canaryEntry()); err != nil {
		t.Fatal(err)
	}
	texts := dumpAllText(t, db)
	if len(texts) == 0 {
		t.Fatal("no text persisted; test is not exercising the writer")
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			t.Fatalf("persisted text is not valid UTF-8: %q", s)
		}
		for _, c := range canaries {
			if strings.Contains(s, c) {
				t.Fatalf("canary %q persisted in %q", c, s)
			}
		}
	}
	var summary string
	db.QueryRow(`SELECT command_summary FROM audit`).Scan(&summary)
	if !strings.Contains(summary, "[REDACTED") || !strings.Contains(summary, "curl") {
		t.Fatalf("placeholder or diagnostic missing: %q", summary)
	}
}

func TestSummarizeCallMasksBeforeTruncating(t *testing.T) {
	// the secret starts before the 120-character cut: it must not survive as a fragment
	cmd := strings.Repeat("a", 100) + " token=" + canaries[1]
	_, summary := SummarizeCall("exec", []byte(`{"command":"`+cmd+`"}`))
	if strings.Contains(summary, "CANARY") || strings.Contains(summary, "ghp_") {
		t.Fatalf("secret survived truncation: %q", summary)
	}
	if !utf8.ValidString(summary) {
		t.Fatalf("summary is not valid UTF-8: %q", summary)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	in := strings.Repeat("ação-日本✓", 20)
	out := truncate(in, 35)
	if !utf8.ValidString(out) {
		t.Fatalf("truncate split a rune: %q", out)
	}
	if utf8.RuneCountInString(out) != 35 || !strings.HasSuffix(out, "...") {
		t.Fatalf("truncate length/suffix wrong: %q", out)
	}
	if got := truncate("Informação útil", 60); got != "Informação útil" {
		t.Fatalf("legitimate text changed: %q", got)
	}
}

func TestSanitizeKeepsLegitimateText(t *testing.T) {
	for _, s := range []string{
		"ls -la /home/user",
		"Informação salva: ção, à, é — ✓ 日本語",
		"[REDACTED] placeholder stays",
		"tokens used: 1200, secret ballot, password policy: 12 chars",
	} {
		if got := corpus.Sanitize(s); got != s {
			t.Errorf("Sanitize changed legitimate text %q -> %q", s, got)
		}
	}
}

func TestSanitizeLegacyEncodings(t *testing.T) {
	// Windows-1252 bytes ("Informação") and a stray invalid byte must come out as valid UTF-8
	legacy := "Informa\xe7\xe3o \xff"
	got := corpus.Sanitize(legacy)
	if !utf8.ValidString(got) {
		t.Fatalf("not valid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "Informa") {
		t.Fatalf("lost text: %q", got)
	}
}

func TestSanitizeIsIdempotent(t *testing.T) {
	once := corpus.Sanitize("Authorization: Bearer " + canaries[0] + " --token " + canaries[1])
	if twice := corpus.Sanitize(once); twice != once {
		t.Fatalf("not idempotent:\n%q\n%q", once, twice)
	}
}
