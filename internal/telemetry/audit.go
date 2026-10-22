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
	baseDir := `W:\Data\telemetry`
	if _, err := os.Stat(`W:\`); err != nil {
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = os.TempDir()
		}
		baseDir = filepath.Join(localApp, "fastexec", "telemetry")
	}
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
			command_summary TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit(timestamp);
		CREATE INDEX IF NOT EXISTS idx_audit_tool ON audit(tool);
		CREATE INDEX IF NOT EXISTS idx_audit_success ON audit(success);
		`
		_, _ = dbInstance.Exec(createTableSQL)

		auditQueue = make(chan AuditEntry, 2048)
		go worker()
	})
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

	successInt := 0
	if e.Success {
		successInt = 1
	}

	query := `
	INSERT INTO audit (
		session_id, tool, host, duration_ms,
		bytes_in, bytes_out, est_tokens_in, est_tokens_out,
		success, exit_code, error_message, command_summary
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, _ = dbInstance.Exec(
		query,
		e.SessionID, e.Tool, e.Host, e.DurationMs,
		e.BytesIn, e.BytesOut, e.EstTokensIn, e.EstTokensOut,
		successInt, e.ExitCode, e.ErrorMessage, e.CommandSummary,
	)
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
		if cmd, ok := generic["command"].(string); ok {
			summary = truncate(cleanLine(cmd), 120)
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
	case "write_file":
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
