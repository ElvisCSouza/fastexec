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

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/measure"
)

type AuditEntry struct {
	SessionID      string
	Tool           string
	Host           string
	DurationMs     int64
	CmdMs          *int64 // tempo do comando dentro da chamada; nil = não medido (grava -1)
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
	Client         string    // canal: clientInfo.name#pid do proxy MCP ("" para CLI)
	ClientName     string    // parte "name" de Client ("unknown" se ausente); preenchido em withIdentity
	ClientPID      int64     // parte "pid" de Client (-1 se ausente); preenchido em withIdentity
	ClientVersion  string    // clientInfo.version do cliente MCP ("unknown" se ausente)
	AppVersion     string    // versão do FastExec que gerou a linha (cli.Version)
	AppBuild       string    // revisão VCS do binário (12 hex, "+dirty" se havia alterações); "unknown" se ausente
	InvokeMode     string    // proxy | daemon | cli
	Conversation   string    // id de conversa vindo do cliente (_meta), quando ele manda (ex.: Antigravity)
	Graph          string    // decisão do grafo: outline, repeat, search ("" = nenhuma)
	GraphSavedKB   int       // tamanho do texto que o outline substituiu
	ArgsHash       string    // sha256 (16 hex) dos argumentos completos, sem campos internos: repetição real de comando/script/arquivo
	OutHash        string    // sha256 (16 hex) do texto devolvido: saída idêntica de verdade
	Marks          string    // marcas do FastExec na resposta ("Hint:ab12,Guard:cd34,args"): o que foi dito ao agente
	Doc            string    // .md lido nesta chamada (caminho normalizado)
	DocFrom        string    // .md lido antes, pela mesma identidade, cujo texto cita Doc
	DocHow         string    // link | dir | name | none | first
	DocLinks       []string  // .md citados no texto que o agente viu (vai para doc_links)
	MarkDefs       []MarkDef // marcas desta resposta com o texto-modelo (vai para mark_catalog, uma vez por id)
	Savings        []Saving  // economias desta chamada (tabela saving)
	Outcome        *Outcome  // desfecho explícito (outcome.go); nil = linha sem desfecho (legado: outcome_schema=0)
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
	return filepath.Join(DataDir(), "fastexec.db")
}

// DataDir é o diretório de dados do FastExec (telemetria e índice de código), criado se não existir.
// Leitura (stats) usa dataDirPath, que não cria nada.
func DataDir() string {
	baseDir := dataDirPath()
	_ = os.MkdirAll(baseDir, 0755)
	return baseDir
}

// dataDirOverride fixa o diretório de dados (testes).
var dataDirOverride string

func dataDirPath() string {
	if dataDirOverride != "" {
		return dataDirOverride
	}
	return filepath.Join(config.Get().Data(), "telemetry")
}

// Init inicializa a conexão SQLite, cria a tabela e inicia o worker em background.
func Init() {
	once.Do(func() {
		dbPath := getDBPath()
		var err error
		// busy_timeout no DSN vale para toda conexão do pool; o PRAGMA abaixo só atinge a conexão que o executa.
		dbInstance, err = sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			noteFailure("open "+dbPath, err)
			dbInstance = nil
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
			if _, err := dbInstance.Exec(p); err != nil {
				noteFailure("open "+dbPath, err) // a primeira instrução já falha se o arquivo não abre
				break
			}
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
	if _, err := db.Exec(createTableSQL); err != nil {
		noteFailure("schema", err)
	}

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
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE audit ADD COLUMN %s INTEGER NOT NULL DEFAULT 0;", col)); err != nil {
				noteFailure("schema migration", err)
			}
		}
	}
	if !existing["graph_saved_kb"] {
		if _, err := db.Exec("ALTER TABLE audit ADD COLUMN graph_saved_kb INTEGER NOT NULL DEFAULT 0;"); err != nil {
			noteFailure("schema migration", err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS saving (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		audit_id INTEGER NOT NULL DEFAULT 0,
		cat TEXT NOT NULL,
		bytes INTEGER NOT NULL DEFAULT 0,
		trips INTEGER NOT NULL DEFAULT 0,
		ms INTEGER NOT NULL DEFAULT 0,
		exact INTEGER NOT NULL DEFAULT 0);
		CREATE INDEX IF NOT EXISTS idx_saving_ts ON saving(timestamp);
		CREATE INDEX IF NOT EXISTS idx_saving_cat ON saving(cat);`); err != nil {
		noteFailure("schema saving", err)
	}
	savingCols := map[string]bool{}
	if rows, err := db.Query("PRAGMA table_info(saving);"); err == nil {
		for rows.Next() {
			var cid, notNull, pk int
			var name, colType string
			var dflt sql.NullString
			if rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk) == nil {
				savingCols[name] = true
			}
		}
		rows.Close()
	}
	for col, typ := range map[string]string{"size_before": "INTEGER NOT NULL DEFAULT 0", "size_after": "INTEGER NOT NULL DEFAULT 0", "note": "TEXT NOT NULL DEFAULT ''"} {
		if !savingCols[col] {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE saving ADD COLUMN %s %s;", col, typ)); err != nil {
				noteFailure("schema migration saving", err)
			}
		}
	}
	// Chamadas anteriores a este marco não guardaram o tamanho original: ficam fora do relatório de economia.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT OR IGNORE INTO meta (key, value) VALUES ('savings_since', CURRENT_TIMESTAMP);`); err != nil {
		noteFailure("schema meta", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS doc_links (
		host TEXT NOT NULL, src TEXT NOT NULL, dst TEXT NOT NULL,
		first_seen DATETIME DEFAULT CURRENT_TIMESTAMP, last_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (host, src, dst));`); err != nil {
		noteFailure("schema doc_links", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mark_catalog (
		mark TEXT PRIMARY KEY, template TEXT NOT NULL,
		first_seen DATETIME DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		noteFailure("schema mark_catalog", err)
	}
	if !existing["cmd_ms"] {
		if _, err := db.Exec("ALTER TABLE audit ADD COLUMN cmd_ms INTEGER NOT NULL DEFAULT -1;"); err != nil {
			noteFailure("schema migration", err)
		}
	}
	for _, col := range []string{"client", "conversation", "graph", "args_hash", "out_hash", "marks", "doc", "doc_from", "doc_how"} {
		if !existing[col] {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE audit ADD COLUMN %s TEXT NOT NULL DEFAULT '';", col)); err != nil {
				noteFailure("schema migration", err)
			}
		}
	}
	// Desfecho (outcome.go). Só ADD COLUMN com DEFAULT: nada é reescrito, e linhas antigas ficam "desconhecido".
	for _, c := range []struct{ name, def string }{
		{"outcome_schema", "INTEGER NOT NULL DEFAULT 0"},
		{"transport_ok", "INTEGER NOT NULL DEFAULT -1"},
		{"tool_ok", "INTEGER NOT NULL DEFAULT -1"},
		{"command_exit_code", "INTEGER NOT NULL DEFAULT -1"},
		{"error_type", "TEXT NOT NULL DEFAULT ''"},
		{"effect_unknown", "INTEGER NOT NULL DEFAULT -1"},
	} {
		if !existing[c.name] {
			addAuditColumn(db, c.name, c.def)
		}
	}
	// Identidade (identity.go): mesma regra. Linhas antigas ficam 'unknown' / -1 pelo DEFAULT, sem backfill.
	for _, c := range []struct{ name, def string }{
		{"app_version", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"app_build", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"client_name", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"client_version", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"client_pid", "INTEGER NOT NULL DEFAULT -1"},
		{"invoke_mode", "TEXT NOT NULL DEFAULT 'unknown'"},
	} {
		if !existing[c.name] {
			addAuditColumn(db, c.name, c.def)
		}
	}
}

// addAuditColumn adiciona uma coluna. Duas conexões (CLI e daemon) podem migrar juntas: a que chega depois recebe
// "duplicate column", e isso é o resultado desejado, não uma falha.
func addAuditColumn(db *sql.DB, name, def string) {
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE audit ADD COLUMN %s %s;", name, def)); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		noteFailure("schema migration", err)
	}
}

// Close fecha o banco (usado pelos testes antes de apagar a pasta temporária: no Windows um SQLite aberto
// impede o RemoveAll e deixava pastas fastexec-test-* no %TEMP%).
func Close() {
	if dbInstance != nil {
		_ = dbInstance.Close()
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
	noteFailure("insert", writeEntry(dbInstance, e))
}

func writeEntry(db *sql.DB, e AuditEntry) error {
	e = sanitizeEntry(e)
	query := `
	INSERT INTO audit (
		session_id, tool, host, duration_ms,
		bytes_in, bytes_out, est_tokens_in, est_tokens_out,
		success, exit_code, error_message, command_summary,
		truncated, no_result, client, conversation, graph, graph_saved_kb,
		args_hash, out_hash, marks, doc, doc_from, doc_how, cmd_ms,
		outcome_schema, transport_ok, tool_ok, command_exit_code, error_type, effect_unknown,
		app_version, app_build, client_name, client_version, client_pid, invoke_mode
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	o, schema := UnknownOutcome, 0
	if e.Outcome != nil {
		o, schema = *e.Outcome, 1
	}
	res, err := db.Exec(
		query,
		e.SessionID, e.Tool, e.Host, e.DurationMs,
		e.BytesIn, e.BytesOut, e.EstTokensIn, e.EstTokensOut,
		boolInt(e.Success), e.ExitCode, e.ErrorMessage, e.CommandSummary,
		boolInt(e.Truncated), boolInt(e.NoResult), e.Client, e.Conversation, e.Graph, e.GraphSavedKB,
		e.ArgsHash, e.OutHash, e.Marks, e.Doc, e.DocFrom, e.DocHow, cmdMsValue(e.CmdMs),
		schema, o.TransportOK, o.ToolOK, o.CommandExitCode, o.ErrorType, o.EffectUnknown,
		orUnknown(e.AppVersion), orUnknown(e.AppBuild), orUnknown(e.ClientName), orUnknown(e.ClientVersion),
		clientPIDValue(e.ClientPID), orUnknown(e.InvokeMode),
	)
	if err == nil && len(e.Savings) > 0 {
		id, _ := res.LastInsertId()
		if serr := writeSavings(db, id, e.Savings); serr != nil {
			return serr
		}
	}
	if err == nil {
		for _, d := range e.MarkDefs {
			if _, merr := db.Exec(`INSERT OR IGNORE INTO mark_catalog (mark, template) VALUES (?, ?)`, d.Mark, d.Template); merr != nil {
				return merr
			}
		}
	}
	if err == nil && e.Doc != "" {
		for _, dst := range e.DocLinks {
			if _, lerr := db.Exec(`INSERT INTO doc_links (host, src, dst) VALUES (?, ?, ?)
				ON CONFLICT(host, src, dst) DO UPDATE SET last_seen = CURRENT_TIMESTAMP;`, e.Host, e.Doc, dst); lerr != nil {
				return lerr
			}
		}
	}
	return err
}

func writeSavings(db *sql.DB, auditID int64, items []Saving) error {
	for _, s := range items {
		if _, err := db.Exec(`INSERT INTO saving (audit_id, cat, bytes, trips, ms, exact, size_before, size_after, note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`,
			auditID, s.Cat, s.Bytes, s.Trips, s.Ms, boolInt(s.Exact), s.Before, s.After, s.Note); err != nil {
			return err
		}
	}
	return nil
}

// cmdMsValue converte o campo opcional para a coluna (-1 = não medido).
func cmdMsValue(ms *int64) int64 {
	if ms == nil {
		return measure.CmdUnknown
	}
	return *ms
}

// orUnknown grava o sentinela de texto quando o campo não foi informado.
func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}

// clientPIDValue grava o sentinela de inteiro (-1) quando o pid não foi informado.
func clientPIDValue(pid int64) int64 {
	if pid <= 0 {
		return UnknownPID
	}
	return pid
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Record registra uma execução de forma assíncrona (zero overhead no caminho crítico).
func Record(e AuditEntry) {
	if !config.Get().TelemetryOn() {
		return
	}
	if auditQueue == nil {
		Init()
	}

	if e.SessionID == "" {
		e.SessionID = sessionIDVal
	}
	e = withIdentity(e)
	if e.EstTokensIn == 0 && e.BytesIn > 0 {
		e.EstTokensIn = int(float64(e.BytesIn) / 3.5)
	}
	if e.EstTokensOut == 0 && e.BytesOut > 0 {
		e.EstTokensOut = int(float64(e.BytesOut) / 3.5)
	}

	select {
	case auditQueue <- e:
	default:
		// fila cheia: descarta para nunca travar a ferramenta, mas conta como falha
		noteFailure("queue", errQueueFull)
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
		// caminho + o que foi pedido, para medir o uso de intervalos/pattern e o corte ("arq [L10-40]", "arq [whole]")
		path, _ := generic["path"].(string)
		num := func(k string) int { f, _ := generic[k].(float64); return int(f) }
		var sel []string
		if s, e := num("start_line"), num("end_line"); s > 0 || e > 0 {
			sel = append(sel, fmt.Sprintf("L%d-%d", s, e))
		}
		if r, _ := generic["ranges"].(string); r != "" {
			sel = append(sel, "ranges="+truncate(r, 40))
		}
		if p, _ := generic["pattern"].(string); p != "" {
			sel = append(sel, "pattern="+truncate(cleanLine(p), 40))
		}
		if num("offset") > 0 || num("limit") > 0 {
			sel = append(sel, fmt.Sprintf("bytes %d+%d", num("offset"), num("limit")))
		}
		if len(sel) == 0 {
			sel = append(sel, "whole")
		}
		summary = path + " [" + strings.Join(sel, " ") + "]"
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

func cleanLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
