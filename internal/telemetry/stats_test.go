package telemetry

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func openTestAudit(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	setupAuditSchema(db)
	return db
}

func sectionLines(report, header string) []string {
	idx := strings.Index(report, header)
	if idx < 0 {
		return nil
	}
	return strings.Split(report[idx:], "\n")
}

func TestPercentileNearestRank(t *testing.T) {
	var values []int64
	for i := int64(1); i <= 100; i++ {
		values = append(values, i)
	}
	if got := percentile(values, 0.50); got != 50 {
		t.Errorf("p50 = %d, want 50", got)
	}
	if got := percentile(values, 0.90); got != 90 {
		t.Errorf("p90 = %d, want 90", got)
	}
	if got := percentile(values, 0.99); got != 99 {
		t.Errorf("p99 = %d, want 99", got)
	}
	if got := percentile([]int64{7}, 0.99); got != 7 {
		t.Errorf("valor único: p99 = %d, want 7", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("vazio: got %d, want 0", got)
	}
}

func TestRenderStatsReturnPercentiles(t *testing.T) {
	db := openTestAudit(t)
	for i := 1; i <= 100; i++ {
		e := AuditEntry{Tool: "exec", BytesOut: i, Success: true, CommandSummary: "cmd"}
		if i <= 2 {
			e.Truncated = true
		}
		if i <= 3 {
			e.NoResult = true
			e.Success = false
			e.ExitCode = 1
		}
		writeEntry(db, e)
	}
	writeEntry(db, AuditEntry{Tool: "read_file", BytesOut: 2048, Success: true, CommandSummary: strings.Repeat("c", 70)})

	report, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatalf("renderStats: %v", err)
	}

	var execLine string
	for _, line := range sectionLines(report, "RETURN SIZE BY TOOL") {
		if strings.HasPrefix(line, "exec") {
			execLine = line
		}
	}
	got := strings.Fields(execLine)
	want := []string{"exec", "50", "B", "90", "B", "99", "B", "100", "B", "2", "3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("linha exec: got %v, want %v", got, want)
	}

	var topLine string
	for _, line := range sectionLines(report, "LARGEST RETURNS IN THE PERIOD") {
		if strings.Contains(line, "read_file") {
			topLine = line
			break
		}
	}
	if !strings.Contains(topLine, "2.0 KB") {
		t.Errorf("maior retorno deveria aparecer com 2.0 KB: %q", topLine)
	}
	if !strings.Contains(topLine, strings.Repeat("c", 57)+"...") || strings.Contains(topLine, strings.Repeat("c", 58)) {
		t.Errorf("comando não foi limitado a 60 caracteres: %q", topLine)
	}
}

func TestSetupAuditSchemaMigratesOldTable(t *testing.T) {
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
		INSERT INTO audit (tool, bytes_out, success) VALUES ('exec', 10, 1);`)
	if err != nil {
		t.Fatal(err)
	}

	setupAuditSchema(db)
	setupAuditSchema(db)
	writeEntry(db, AuditEntry{Tool: "exec", BytesOut: 20, Success: true, Truncated: true})

	var total, truncated int
	if err := db.QueryRow("SELECT COUNT(*), SUM(truncated) FROM audit").Scan(&total, &truncated); err != nil {
		t.Fatalf("consulta após migração: %v", err)
	}
	if total != 2 || truncated != 1 {
		t.Fatalf("após migração: total=%d truncated=%d, want 2 e 1", total, truncated)
	}
}
