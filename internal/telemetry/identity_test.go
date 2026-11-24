package telemetry

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCaller(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantPID  int64
	}{
		{"cursor#42", "cursor", 42},
		{"my#tool#7", "my#tool", 7}, // name keeps a '#': split at the last one
		{"cursor", "cursor", UnknownPID},
		{"cursor#0", "cursor#0", UnknownPID},
		{"x#abc", "x#abc", UnknownPID},
		{"", Unknown, UnknownPID},
	}
	for _, c := range cases {
		name, pid := SplitCaller(c.in)
		if name != c.wantName || pid != c.wantPID {
			t.Errorf("SplitCaller(%q) = %q,%d; want %q,%d", c.in, name, pid, c.wantName, c.wantPID)
		}
	}
}

// A row written without any identity stores the explicit sentinels, never a guessed value.
func TestWriteEntryExplicitUnknownIdentity(t *testing.T) {
	db := legacyDB(t)
	if err := writeEntry(db, AuditEntry{Tool: "exec"}); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	var appVer, appBuild, cname, cver, mode string
	var pid int64
	if err := db.QueryRow(`SELECT app_version, app_build, client_name, client_version, client_pid, invoke_mode
		FROM audit WHERE tool='exec'`).Scan(&appVer, &appBuild, &cname, &cver, &pid, &mode); err != nil {
		t.Fatal(err)
	}
	if appVer != Unknown || appBuild != Unknown || cname != Unknown || cver != Unknown || mode != Unknown || pid != UnknownPID {
		t.Fatalf("sentinels: app_version=%q app_build=%q client_name=%q client_version=%q client_pid=%d invoke_mode=%q",
			appVer, appBuild, cname, cver, pid, mode)
	}
}

// A new write with identity stores the build, the client name and pid split from the caller, and the client version.
// The legacy client column keeps the full "name#pid" text.
func TestNewWriteRecordsIdentity(t *testing.T) {
	prevVer := buildVersion
	t.Cleanup(func() { buildVersion = prevVer })
	SetBuildVersion("test-1.2.3")

	db := legacyDB(t)
	e := withIdentity(AuditEntry{Tool: "exec", Client: "cursor#42", ClientVersion: "0.9"})
	if err := writeEntry(db, e); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	var client, appVer, cname, cver string
	var pid int64
	if err := db.QueryRow(`SELECT client, app_version, client_name, client_pid, client_version FROM audit WHERE tool='exec'`).
		Scan(&client, &appVer, &cname, &pid, &cver); err != nil {
		t.Fatal(err)
	}
	if client != "cursor#42" || cname != "cursor" || pid != 42 || cver != "0.9" || appVer != "test-1.2.3" {
		t.Fatalf("identity: client=%q client_name=%q pid=%d client_version=%q app_version=%q",
			client, cname, pid, cver, appVer)
	}
}

// An old table gets the identity columns with their DEFAULT; the old row reads as unknown and is not backfilled.
func TestLegacyDBIdentityColumnsReadUnknown(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old_identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		session_id TEXT, tool TEXT NOT NULL, host TEXT, duration_ms INTEGER,
		bytes_in INTEGER, bytes_out INTEGER, est_tokens_in INTEGER, est_tokens_out INTEGER,
		success INTEGER, exit_code INTEGER, error_message TEXT, command_summary TEXT,
		client TEXT);
		INSERT INTO audit (tool, success, client) VALUES ('exec', 1, 'cursor#42');`)
	if err != nil {
		t.Fatal(err)
	}
	setupAuditSchema(db)

	var appVer, cname, mode string
	var pid int64
	if err := db.QueryRow(`SELECT app_version, client_name, client_pid, invoke_mode FROM audit WHERE id=1`).
		Scan(&appVer, &cname, &pid, &mode); err != nil {
		t.Fatalf("legacy row after migration: %v", err)
	}
	if appVer != Unknown || cname != Unknown || pid != UnknownPID || mode != Unknown {
		t.Fatalf("legacy row was backfilled: app_version=%q client_name=%q pid=%d invoke_mode=%q", appVer, cname, pid, mode)
	}
}

func insertIdentityRow(t *testing.T, db *sql.DB, tool string, appVer, appBuild, client string) {
	t.Helper()
	name, pid := SplitCaller(client)
	if err := writeEntry(db, AuditEntry{Tool: tool, Success: true, AppVersion: appVer, AppBuild: appBuild,
		Client: client, ClientName: name, ClientPID: pid}); err != nil {
		t.Fatalf("writeEntry %s: %v", tool, err)
	}
}

func TestStatsIsolatesBuildAndClient(t *testing.T) {
	db := openTestAudit(t)
	insertIdentityRow(t, db, "toolA", "1.0", "abc123", "cursor#1")
	insertIdentityRow(t, db, "toolB", "2.0", "def456", "claude#2")

	count := func(f StatsFilter) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit ` + periodWhere(f)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(StatsFilter{Build: "1.0"}); n != 1 {
		t.Errorf("build by app_version: %d rows, want 1", n)
	}
	if n := count(StatsFilter{Build: "def456"}); n != 1 {
		t.Errorf("build by app_build: %d rows, want 1", n)
	}
	if n := count(StatsFilter{Client: "cursor"}); n != 1 {
		t.Errorf("client: %d rows, want 1", n)
	}
	if n := count(StatsFilter{Client: "cursor", Build: "2.0"}); n != 0 {
		t.Errorf("client AND build: %d rows, want 0", n)
	}
	// A value with a quote must stay a literal, not change the query.
	if n := count(StatsFilter{Client: "x' OR '1'='1"}); n != 0 {
		t.Errorf("quoted value matched %d rows, want 0", n)
	}

	out, err := renderStats(db, StatsFilter{Client: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "toolA") || strings.Contains(out, "toolB") {
		t.Errorf("client filter output mixes clients:\n%s", out)
	}
	if strings.Contains(out, "SAVINGS") {
		t.Errorf("savings block must be left out under a segment filter:\n%s", out)
	}
}

func TestStatsDefaultHasNoSegment(t *testing.T) {
	if w := periodWhere(StatsFilter{}); w != "" {
		t.Fatalf("default filter adds a WHERE: %q", w)
	}
	db := openTestAudit(t)
	insertIdentityRow(t, db, "toolA", "1.0", "abc123", "cursor#1")
	out, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Filter:") {
		t.Errorf("default output shows a filter line:\n%s", out)
	}
	empty := openTestAudit(t)
	msg, _ := renderStats(empty, StatsFilter{Build: "9.9"})
	if !strings.Contains(msg, "build=9.9") {
		t.Errorf("no-records message should name the segment: %q", msg)
	}
}

func TestArgsHashIgnoresClientVersion(t *testing.T) {
	a := ArgsHash([]byte(`{"command":"ls","_caller":"c#1","_client_version":"1.0"}`))
	b := ArgsHash([]byte(`{"command":"ls"}`))
	if a != b {
		t.Fatalf("injected _client_version changed the args hash: %s vs %s", a, b)
	}
}
