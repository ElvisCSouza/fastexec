package graph

// indexer.go: indexação incremental por repositório. Lista os arquivos (git ls-files respeita .gitignore; sem git,
// varredura pulando diretórios de ruído), compara (mtime, hash) com code_files e só reprocessa o que mudou, numa
// transação. Touch reindexa em segundo plano (debounce) um arquivo gravado por write_file/edit_file.

import (
	"bytes"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/codescan"
)

const (
	maxFileSize   = 2 << 20
	maxRepoFiles  = 20000
	touchDebounce = 300 * time.Millisecond
	// checkpointAfter: passadas que mexem em tantos arquivos fazem checkpoint do WAL (indexação inicial, rebuild)
	checkpointAfter = 50
)

// Stats resume uma passada de indexação.
type Stats struct {
	Files, Changed, Removed int
	Took                    time.Duration
}

// ResolveRepoRoot sobe a partir de path até a pasta com .git mais próxima (diretório ou arquivo, como em worktrees).
// Fora de repositório git devolve false: o grafo nunca indexa o disco solto (C:\, Desktop, configs avulsas).
// path pode ainda não existir (arquivo recém-apagado): a subida começa na pasta que existir.
func ResolveRepoRoot(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	dir := abs
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		dir = filepath.Dir(abs)
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

func isRepoRoot(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// ErrNotInRepo explica por que o grafo não age fora de um repositório git.
func ErrNotInRepo(path string) error {
	return fmt.Errorf("'%s' is not inside a git repository: code_graph only indexes git repositories (outside them use code_outline or exec)", path)
}

// repoKey normaliza a raiz (barras '/', minúsculas no Windows) para particionar o banco.
func repoKey(root string) string {
	k := filepath.ToSlash(filepath.Clean(root))
	if runtime.GOOS == "windows" {
		k = strings.ToLower(k)
	}
	return k
}

func contentHash(b []byte) string {
	h := fnv.New64a()
	h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}

func indexable(rel string) bool {
	return codescan.LangFor(rel) != nil && !codescan.IsNoisePath(rel)
}

// listFiles devolve os caminhos relativos (com '/') dos arquivos indexáveis.
func listFiles(root string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		cmd := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
		cmd.SysProcAttr = hiddenWindow()
		if b, err := cmd.Output(); err == nil {
			for _, rel := range bytes.Split(b, []byte{0}) {
				if r := string(rel); r != "" && indexable(r) {
					out = append(out, r)
				}
			}
			return capFiles(out)
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && codescan.IsNoiseDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil && indexable(filepath.ToSlash(rel)) {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return capFiles(out)
}

func capFiles(f []string) []string {
	if len(f) > maxRepoFiles {
		return f[:maxRepoFiles]
	}
	return f
}

type fileRec struct {
	mtime int64
	hash  string
}

type stmts struct {
	delSym, delEdge, delFile, insSym, insEdge, upFile, touchFile *sql.Stmt
}

func prepare(tx *sql.Tx) (*stmts, error) {
	s := &stmts{}
	for _, p := range []struct {
		dst **sql.Stmt
		q   string
	}{
		{&s.delSym, `DELETE FROM code_symbols WHERE repo_root = ? AND file_path = ?`},
		{&s.delEdge, `DELETE FROM code_edges WHERE repo_root = ? AND source_file = ?`},
		{&s.delFile, `DELETE FROM code_files WHERE repo_root = ? AND file_path = ?`},
		{&s.insSym, `INSERT INTO code_symbols (repo_root, file_path, name, kind, signature, start_line, end_line, is_exported) VALUES (?,?,?,?,?,?,?,?)`},
		{&s.insEdge, `INSERT INTO code_edges (repo_root, source_file, source_line, target_symbol, relation_type) VALUES (?,?,?,?,?)`},
		{&s.upFile, `INSERT INTO code_files (repo_root, file_path, mtime, content_hash, indexed_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)
			ON CONFLICT(repo_root, file_path) DO UPDATE SET mtime = excluded.mtime, content_hash = excluded.content_hash, indexed_at = CURRENT_TIMESTAMP`},
		{&s.touchFile, `UPDATE code_files SET mtime = ? WHERE repo_root = ? AND file_path = ?`},
	} {
		st, err := tx.Prepare(p.q)
		if err != nil {
			return nil, err
		}
		*p.dst = st
	}
	return s, nil
}

func (s *stmts) remove(key, rel string) error {
	for _, st := range []*sql.Stmt{s.delSym, s.delEdge, s.delFile} {
		if _, err := st.Exec(key, rel); err != nil {
			return err
		}
	}
	return nil
}

// index troca símbolos e arestas do arquivo pelos recém-extraídos e registra (mtime, hash).
func (s *stmts) index(key, rel string, src []byte, mtime int64, hash string) error {
	if _, err := s.delSym.Exec(key, rel); err != nil {
		return err
	}
	if _, err := s.delEdge.Exec(key, rel); err != nil {
		return err
	}
	decls, refs, _ := ParseFile(rel, src)
	for _, d := range decls {
		if _, err := s.insSym.Exec(key, rel, d.Name, d.Kind, d.Sig, d.Line, d.EndLine, d.Exported); err != nil {
			return err
		}
	}
	for _, r := range refs {
		if _, err := s.insEdge.Exec(key, rel, r.Line, r.Target, r.Rel); err != nil {
			return err
		}
	}
	_, err := s.upFile.Exec(key, rel, mtime, hash)
	return err
}

// Refresh deixa o índice do repositório em dia: só arquivos novos ou com (mtime, hash) diferentes são reprocessados;
// os que sumiram saem do índice. Na primeira chamada indexa tudo.
func Refresh(root string) (Stats, error) {
	start := time.Now()
	if !isRepoRoot(root) {
		return Stats{}, ErrNotInRepo(root)
	}
	d, err := store()
	if err != nil {
		return Stats{}, err
	}
	key := repoKey(root)
	files := listFiles(root)

	known := map[string]fileRec{}
	rows, err := d.Query(`SELECT file_path, mtime, content_hash FROM code_files WHERE repo_root = ?`, key)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var rel string
		var rec fileRec
		if err := rows.Scan(&rel, &rec.mtime, &rec.hash); err == nil {
			known[rel] = rec
		}
	}
	rows.Close()

	tx, err := d.Begin()
	if err != nil {
		return Stats{}, err
	}
	defer tx.Rollback()
	s, err := prepare(tx)
	if err != nil {
		return Stats{}, err
	}

	st := Stats{Files: len(files)}
	seen := make(map[string]bool, len(files))
	for _, rel := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > maxFileSize {
			continue
		}
		seen[rel] = true
		mtime := info.ModTime().UnixNano()
		rec, ok := known[rel]
		if ok && rec.mtime == mtime {
			continue
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		h := contentHash(src)
		if ok && rec.hash == h {
			if _, err := s.touchFile.Exec(mtime, key, rel); err != nil {
				return st, err
			}
			continue
		}
		if err := s.index(key, rel, src, mtime, h); err != nil {
			return st, err
		}
		st.Changed++
	}
	for rel := range known {
		if !seen[rel] {
			if err := s.remove(key, rel); err != nil {
				return st, err
			}
			st.Removed++
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	if st.Changed+st.Removed >= checkpointAfter {
		_, _ = d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`) // passada grande: devolve o WAL ao banco e zera o arquivo
	}
	st.Took = time.Since(start)
	return st, nil
}

// Reindex apaga o índice do repositório e indexa tudo de novo.
func Reindex(root string) (Stats, error) {
	if !isRepoRoot(root) {
		return Stats{}, ErrNotInRepo(root)
	}
	d, err := store()
	if err != nil {
		return Stats{}, err
	}
	key := repoKey(root)
	for _, q := range []string{
		`DELETE FROM code_symbols WHERE repo_root = ?`,
		`DELETE FROM code_edges WHERE repo_root = ?`,
		`DELETE FROM code_files WHERE repo_root = ?`,
	} {
		if _, err := d.Exec(q, key); err != nil {
			return Stats{}, err
		}
	}
	return Refresh(root)
}

// ReindexFile atualiza um único arquivo (ou o remove do índice, se sumiu), numa transação.
func ReindexFile(root, absPath string) error {
	if !isRepoRoot(root) {
		return ErrNotInRepo(root)
	}
	d, err := store()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || !indexable(rel) {
		return nil
	}
	key := repoKey(root)
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, err := prepare(tx)
	if err != nil {
		return err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if err := s.remove(key, rel); err != nil {
			return err
		}
		return tx.Commit()
	}
	if info.Size() > maxFileSize {
		return nil
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	if err := s.index(key, rel, src, info.ModTime().UnixNano(), contentHash(src)); err != nil {
		return err
	}
	return tx.Commit()
}

func indexed(root string) bool {
	d, err := store()
	if err != nil {
		return false
	}
	var one int
	return d.QueryRow(`SELECT 1 FROM code_files WHERE repo_root = ? LIMIT 1`, repoKey(root)).Scan(&one) == nil
}

var (
	touchMu sync.Mutex
	timers  = map[string]*time.Timer{}
)

// Touch agenda a reindexação de um arquivo gravado (debounce): só age se o banco já existe e o repositório do
// arquivo já foi indexado; nunca cria índice nem bloqueia quem gravou.
func Touch(path string) {
	if codescan.LangFor(path) == nil || !exists() {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	touchMu.Lock()
	defer touchMu.Unlock()
	if t, ok := timers[abs]; ok {
		t.Reset(touchDebounce)
		return
	}
	timers[abs] = time.AfterFunc(touchDebounce, func() {
		touchMu.Lock()
		delete(timers, abs)
		touchMu.Unlock()
		if root, ok := ResolveRepoRoot(abs); ok && indexed(root) {
			_ = ReindexFile(root, abs)
		}
	})
}
