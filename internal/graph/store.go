// Package graph: grafo de código incremental (símbolos, chamadas, imports) em SQLite, sem CGO.
// store.go: o banco (graph.db ao lado da telemetria), particionado por repo_root: um repositório nunca vê
// símbolos de outro. Uma conexão, WAL com synchronous=NORMAL (commit sem fsync) e cache de 2 MB.
package graph

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"homelab/fastexec/internal/telemetry"
)

const schema = `
CREATE TABLE IF NOT EXISTS code_files (
	repo_root    TEXT NOT NULL,
	file_path    TEXT NOT NULL,
	mtime        INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	indexed_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (repo_root, file_path)
);
CREATE TABLE IF NOT EXISTS code_symbols (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_root   TEXT NOT NULL,
	file_path   TEXT NOT NULL,
	name        TEXT NOT NULL,
	kind        TEXT NOT NULL,
	signature   TEXT NOT NULL,
	start_line  INTEGER NOT NULL,
	end_line    INTEGER NOT NULL,
	is_exported BOOLEAN NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_symbols_lookup ON code_symbols (repo_root, name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON code_symbols (repo_root, file_path);
CREATE TABLE IF NOT EXISTS code_edges (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_root     TEXT NOT NULL,
	source_file   TEXT NOT NULL,
	source_line   INTEGER NOT NULL,
	target_symbol TEXT NOT NULL,
	relation_type TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_edges_target ON code_edges (repo_root, target_symbol, relation_type);
CREATE INDEX IF NOT EXISTS idx_edges_source ON code_edges (repo_root, source_file);
`

var (
	mu     sync.Mutex
	db     *sql.DB
	dbPath string // vazio = graph.db no diretório de dados; testes trocam com SetDBPath
)

// SetDBPath troca o arquivo do banco (testes) e fecha o atual.
func SetDBPath(p string) {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		db.Close()
		db = nil
	}
	dbPath = p
}

func dbFile() string {
	if dbPath != "" {
		return dbPath
	}
	return filepath.Join(telemetry.DataDir(), "graph.db")
}

// exists diz se o banco já foi criado (o hook de write_file não cria banco em quem nunca usou o grafo).
func exists() bool {
	mu.Lock()
	open := db != nil
	mu.Unlock()
	if open {
		return true
	}
	_, err := os.Stat(dbFile())
	return err == nil
}

func store() (*sql.DB, error) {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		return db, nil
	}
	d, err := sql.Open("sqlite", dbFile())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA cache_size=-2048;",
	} {
		if _, err := d.Exec(p); err != nil {
			d.Close()
			return nil, err
		}
	}
	if _, err := d.Exec(schema); err != nil {
		d.Close()
		return nil, err
	}
	db = d
	return db, nil
}
