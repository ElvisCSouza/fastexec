package tools

import (
	"encoding/base64"
	"fmt"
	"strings"

	"homelab/fastexec/internal/analytics"
	"homelab/fastexec/internal/transport"
)

type SqlArgs struct {
	Host     string `json:"host,omitempty"`
	Database string `json:"database,omitempty"`
	Session  string `json:"session,omitempty"`
	Query    string `json:"query"`
}

func HandleSql(args SqlArgs) (string, error) {
	if args.Query == "" {
		return "", fmt.Errorf("parameter 'query' is required")
	}

	// 1. Modo DuckDB Analítico (sessão isolada, local)
	if args.Session != "" || (args.Database == "" && args.Host == "") {
		sessionName := args.Session
		if sessionName == "" {
			sessionName = "default"
		}
		return analytics.ExecuteDuckDBQuery(sessionName, args.Query, 60000)
	}

	// 2. Modo SQLite (host remoto ou local)
	if args.Database == "" {
		return "", fmt.Errorf("give 'database' (SQLite) or 'session' (analytic DuckDB)")
	}

	// Sem 'host', um caminho de unidade Windows no 'database' indica o próprio PC (mesma decisão do exec/job):
	// roda em 'local' e a saída abre com a nota de auto-remediação. Host explícito nunca é trocado.
	host, note := localHostForWindowsPath(args.Host, args.Database)

	target, err := transport.ResolveHost(host)
	if err != nil {
		return "", err
	}

	// Local: a consulta roda in-process (driver embutido), sem depender de sqlite3/base64 no Windows.
	if target.IsLocal {
		out, qerr := querySQLiteLocal(args.Database, args.Query)
		return withHostNote(note, out, qerr), qerr
	}

	b64Query := base64.StdEncoding.EncodeToString([]byte(args.Query))
	cmd := fmt.Sprintf("echo '%s' | base64 -d | sqlite3 -json '%s'", b64Query, args.Database)

	res, err := transport.Dispatch(host, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}

	if res.ExitCode != 0 {
		return fmt.Sprintf("[SQL Error: exit %d] %s", res.ExitCode, res.Stderr), fmt.Errorf("sql failed with exit code %d", res.ExitCode)
	}

	trimmed := strings.TrimSpace(res.Stdout)
	if trimmed == "" {
		return "[]", nil
	}

	return trimmed, nil
}
