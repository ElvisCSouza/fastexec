package telemetry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type AuditEntry struct {
	SessionID      string
	Tool           string
	Host           string
	DurationMs     int64
	BytesIn        int
	BytesOut       int
	EstTokensIn    int
	EstTokensOut   int
	Success        bool
	ExitCode       int
	ErrorMessage   string
	CommandSummary string
	Truncated      bool
	NoResult       bool
}

var (
	dbInstance   *sql.DB
	auditQueue   chan AuditEntry
	once         sync.Once
	sessionIDVal string
)

func init() {
	sessionIDVal = fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), os.Getpid())
}

// SessionID retorna o identificador único da sessão do processo atual.
func SessionID() string {
	return sessionIDVal
}

func getDBPath() string {
	if _, err := os.Stat(`W:\`); err == nil {
		baseDir := `W:\Data\telemetry`
		_ = os.MkdirAll(baseDir, 0755)
		return filepath.Join(baseDir, "fastexec.db")
	}
	if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
		baseDir := filepath.Join(localApp, "fastexec", "telemetry")
		_ = os.MkdirAll(baseDir, 0755)
		return filepath.Join(baseDir, "fastexec.db")
	}
	if home := os.Getenv("HOME"); home != "" {
		baseDir := filepath.Join(home, ".local", "share", "fastexec")
		_ = os.MkdirAll(baseDir, 0755)
		return filepath.Join(baseDir, "fastexec.db")
	}
	baseDir := filepath.Join(os.TempDir(), "fastexec", "telemetry")
	_ = os.MkdirAll(baseDir, 0755)
	return filepath.Join(baseDir, "fastexec.db")
}

// Init inicializa a conexão SQLite, cria a tabela e inicia o worker em background.
func Init() {
	once.Do(func() {
		dbPath := getDBPath()
		var err error
		dbInstance, err = sql.Open("sqlite", dbPath)
		if err != nil {
			return
		}

		// Configurações de alta performance e concorrência
		pragmas := []string{
			"PRAGMA journal_mode=WAL;",
			"PRAGMA synchronous=NORMAL;",
			"PRAGMA busy_timeout=5000;",
			"PRAGMA temp_store=MEMORY;",
		}
		for _, p := range pragmas {
			_, _ = dbInstance.Exec(p)
		}

		setupAuditSchema(dbInstance)

		auditQueue = make(chan AuditEntry, 2048)
		go worker()
	})
}

func setupAuditSchema(db *sql.DB) {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		session_id TEXT,
		tool TEXT NOT NULL,
		host TEXT,
		duration_ms INTEGER,
		bytes_in INTEGER,
		bytes_out INTEGER,
		est_tokens_in INTEGER,
		est_tokens_out INTEGER,
		success INTEGER,
		exit_code INTEGER,
		error_message TEXT,
		command_summary TEXT,
		truncated INTEGER NOT NULL DEFAULT 0,
		no_result INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit(timestamp);
	CREATE INDEX IF NOT EXISTS idx_audit_tool ON audit(tool);
	CREATE INDEX IF NOT EXISTS idx_audit_success ON audit(success);
	`
	_, _ = db.Exec(createTableSQL)

	existing := map[string]bool{}
	if rows, err := db.Query("PRAGMA table_info(audit);"); err == nil {
		for rows.Next() {
			var cid, notNull, pk int
			var name, colType string
			var dflt sql.NullString
			if rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk) == nil {
				existing[name] = true
			}
		}
		rows.Close()
	}
	for _, col := range []string{"truncated", "no_result"} {
		if !existing[col] {
			_, _ = db.Exec(fmt.Sprintf("ALTER TABLE audit ADD COLUMN %s INTEGER NOT NULL DEFAULT 0;", col))
		}
	}
}

func worker() {
	for entry := range auditQueue {
		insertEntry(entry)
	}
}

func insertEntry(e AuditEntry) {
	if dbInstance == nil {
		return
	}
	writeEntry(dbInstance, e)
}

func writeEntry(db *sql.DB, e AuditEntry) {
	query := `
	INSERT INTO audit (
		session_id, tool, host, duration_ms,
		bytes_in, bytes_out, est_tokens_in, est_tokens_out,
		success, exit_code, error_message, command_summary,
		truncated, no_result
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, _ = db.Exec(
		query,
		e.SessionID, e.Tool, e.Host, e.DurationMs,
		e.BytesIn, e.BytesOut, e.EstTokensIn, e.EstTokensOut,
		boolInt(e.Success), e.ExitCode, e.ErrorMessage, e.CommandSummary,
		boolInt(e.Truncated), boolInt(e.NoResult),
	)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Record registra uma execução de forma assíncrona (zero overhead no caminho crítico).
func Record(e AuditEntry) {
	if auditQueue == nil {
		Init()
	}

	if e.SessionID == "" {
		e.SessionID = sessionIDVal
	}
	if e.EstTokensIn == 0 && e.BytesIn > 0 {
		e.EstTokensIn = int(float64(e.BytesIn) / 3.5)
	}
	if e.EstTokensOut == 0 && e.BytesOut > 0 {
		e.EstTokensOut = int(float64(e.BytesOut) / 3.5)
	}

	select {
	case auditQueue <- e:
	default:
		// Se a fila estiver cheia, descarta silenciosamente para nunca travar a ferramenta
	}
}

// SummarizeCall extrai host e resumo técnico do comando a partir dos argumentos brutos.
func SummarizeCall(toolName string, rawArgs []byte) (string, string) {
	var generic map[string]any
	if err := json.Unmarshal(rawArgs, &generic); err != nil {
		return "", ""
	}

	host := ""
	if h, ok := generic["host"].(string); ok {
		host = h
	}

	summary := ""
	switch toolName {
	case "exec":
		if cmd, ok := generic["command"].(string); ok && cmd != "" {
			summary = truncate(cleanLine(cmd), 120)
		} else if argv, ok := generic["argv"].([]any); ok && len(argv) > 0 {
			parts := make([]string, 0, len(argv))
			for _, a := range argv {
				if s, ok := a.(string); ok {
					parts = append(parts, s)
				}
			}
			summary = truncate(cleanLine(strings.Join(parts, " ")), 120)
		}
	case "sql":
		if q, ok := generic["query"].(string); ok {
			summary = truncate(cleanLine(q), 120)
		}
	case "file_ops":
		action, _ := generic["action"].(string)
		path, _ := generic["path"].(string)
		summary = fmt.Sprintf("%s %s", action, path)
	case "read_file":
		path, _ := generic["path"].(string)
		summary = path
	case "write_file", "edit_file":
		path, _ := generic["path"].(string)
		summary = path
	case "get_file":
		remote, _ := generic["remote_path"].(string)
		local, _ := generic["local_path"].(string)
		summary = fmt.Sprintf("%s -> %s", remote, local)
	case "put_file":
		local, _ := generic["local_path"].(string)
		remote, _ := generic["remote_path"].(string)
		summary = fmt.Sprintf("%s -> %s", local, remote)
	case "job":
		action, _ := generic["action"].(string)
		jobID, _ := generic["job_id"].(string)
		cmd, _ := generic["command"].(string)
		if cmd != "" {
			summary = fmt.Sprintf("%s %s", action, truncate(cmd, 60))
		} else {
			summary = fmt.Sprintf("%s %s", action, jobID)
		}
	case "ping":
		summary = "ping"
	}

	return host, summary
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func cleanLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
