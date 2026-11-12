package graph

// indexer.go: indexação incremental por repositório. Lista os arquivos (git ls-files respeita .gitignore; sem git,
// varredura pulando diretórios de ruído), compara (mtime, hash) com code_files e só reprocessa o que mudou, numa
// transação. Touch reindexa em segundo plano (debounce) um arquivo gravado por write_file/edit_file.

import (
	"bytes"
	"database/sql"
	"errors"
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

// maxRepoFiles: acima disso o repositório quase sempre comitou dependências (node_modules, vendor, builds); a
// indexação é recusada em vez de inflar o banco. Variável para os testes.
var maxRepoFiles = 10000

// Pasta avulsa (sem .git): o teto é bem menor, porque a varredura é do disco inteiro da pasta e não passa pelo
// .gitignore. Acima de qualquer um dos dois a recusa é a de sempre ("not inside a git repository"), sem promessa
// nova. Variáveis para os testes.
var (
	maxStandaloneFiles = 3000
	maxStandaloneBytes = int64(64 << 20)
)

const (
	maxFileSize   = 2 << 20
	touchDebounce = 300 * time.Millisecond
	// checkpointAfter: passadas que mexem em tantos arquivos fazem checkpoint do WAL (indexação inicial, rebuild)
	checkpointAfter = 50
)

// Stats resume uma passada de indexação. Docs conta os Markdown rastreados (só eles entram no grafo de
// documentação e só eles justificam consultar as arestas de doc). Fresh marca a passada que nem precisou varrer (o
// estado do repositório era o mesmo da última); OverBudget marca a que parou no teto de tempo do Sync (índice
// possivelmente incompleto).
type Stats struct {
	Files, Changed, Removed, Docs int
	Took                          time.Duration
	Fresh, OverBudget             bool
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

// ResolveStandaloneRoot devolve a pasta a indexar como avulsa: path, quando é pasta, ou a pasta que o contém,
// quando é arquivo. Só aceita pasta que exista e que NÃO seja raiz de repositório git (essa é do ResolveRepoRoot).
// Não sobe ancestral nenhum: a pasta pedida é o escopo, e é ela que vira chave do índice.
func ResolveStandaloneRoot(path string) (string, bool) {
	if isRepoRoot(path) {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", false
	}
	return abs, true
}

// standaloneFits diz se a pasta avulsa cabe nos limites (arquivos e bytes). É a mesma trava da passada completa,
// exposta para o caminho de um arquivo só não deixar entrar no índice a pasta que a passada completa recusaria.
func standaloneFits(root string) bool {
	_, _, over := listFiles(root, maxStandaloneFiles, maxStandaloneBytes)
	return !over
}

// SetStandaloneLimits troca os tetos da pasta avulsa e devolve a função que restaura os valores anteriores (testes;
// mesmo espírito do SetDBPath — os tetos são baixos demais para um teste montar a pasta de verdade).
func SetStandaloneLimits(files int, bytes int64) func() {
	oldF, oldB := maxStandaloneFiles, maxStandaloneBytes
	maxStandaloneFiles, maxStandaloneBytes = files, bytes
	return func() { maxStandaloneFiles, maxStandaloneBytes = oldF, oldB }
}

// notInRepoError é o erro de recusa, tipado para quem chama poder reconhecer a recusa e reescrevê-la com o caminho
// que o agente pediu (a mensagem é a mesma de sempre).
type notInRepoError struct{ path string }

func (e *notInRepoError) Error() string {
	return fmt.Sprintf("'%s' is not inside a git repository: code_graph only indexes git repositories (outside them use code_outline or exec)", e.path)
}

// ErrNotInRepo explica por que o grafo não age fora de um repositório git nem numa pasta avulsa dentro dos limites.
func ErrNotInRepo(path string) error { return &notInRepoError{path: path} }

// IsNotInRepo diz se err é a recusa (para a tools repetir a mensagem com o caminho pedido).
func IsNotInRepo(err error) bool {
	var e *notInRepoError
	return errors.As(err, &e)
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
	return (codescan.LangFor(rel) != nil || codescan.IsDoc(rel)) && !codescan.IsNoisePath(rel)
}

// tooBig aplica o limite de tamanho: documentação tem teto menor (MD de 2 MB é log colado ou doc gerada).
func tooBig(rel string, size int64) bool {
	if codescan.IsDoc(rel) {
		return size > maxDocSize
	}
	return size > maxFileSize
}

// listFiles devolve os caminhos relativos (com '/') dos arquivos indexáveis e o total listado (todos os arquivos
// do git ls-files, indexáveis ou não; na varredura sem git, os indexáveis), que alimenta a trava de sanidade.
// over=true quando a varredura passou de cap arquivos ou de byteCap bytes: aí a pasta é grande demais para ser
// indexada sem git e quem chamou recusa. Na varredura a contagem e o tamanho somam em quanto já foi varrido, então
// uma pasta enorme é abandonada no meio em vez de ser lida inteira.
func listFiles(root string, cap int, byteCap int64) (files []string, total int, over bool) {
	var out []string
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		cmd := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
		cmd.SysProcAttr = hiddenWindow()
		if b, err := cmd.Output(); err == nil {
			total := 0
			for _, rel := range bytes.Split(b, []byte{0}) {
				r := string(rel)
				if r == "" {
					continue
				}
				total++
				if indexable(r) {
					out = append(out, r)
				}
			}
			return out, total, false
		}
	}
	var bytesSeen int64
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
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || !indexable(filepath.ToSlash(rel)) {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if info, ierr := d.Info(); ierr == nil {
			bytesSeen += info.Size()
		}
		if len(out) > cap || (byteCap > 0 && bytesSeen > byteCap) {
			over = true
			return fs.SkipAll
		}
		return nil
	})
	return out, len(out), over
}

// ErrTooManyFiles recusa indexar repositório grande demais (dependências comitadas, quase sempre).
func ErrTooManyFiles(total int) error {
	return fmt.Errorf("[FastExec Graph] The repository lists %d files (limit %d): usually committed dependencies "+
		"(node_modules, vendor, builds). Add them to .gitignore (and 'git rm -r --cached' them) before indexing", total, maxRepoFiles)
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

// index troca símbolos e arestas do arquivo pelos recém-extraídos e registra (mtime, hash). Num Markdown os nós
// são as seções e as arestas de saída dependem do índice inteiro já atualizado, então voltam para a segunda
// passada da mesma transação em vez de serem gravadas aqui.
func (s *stmts) index(key, rel string, src []byte, mtime int64, hash string) ([]DocRef, error) {
	if _, err := s.delSym.Exec(key, rel); err != nil {
		return nil, err
	}
	if _, err := s.delEdge.Exec(key, rel); err != nil {
		return nil, err
	}
	if codescan.IsDoc(rel) {
		secs, refs := ParseMarkdown(rel, src)
		for _, sec := range secs {
			if _, err := s.insSym.Exec(key, rel, sec.Name, KindDocSection, sec.Header, sec.Line, sec.EndLine, true); err != nil {
				return nil, err
			}
		}
		if _, err := s.upFile.Exec(key, rel, mtime, hash); err != nil {
			return nil, err
		}
		return refs, nil
	}
	decls, refs, _ := ParseFile(rel, src)
	for _, d := range decls {
		if _, err := s.insSym.Exec(key, rel, d.Name, d.Kind, d.Sig, d.Line, d.EndLine, d.Exported); err != nil {
			return nil, err
		}
	}
	for _, r := range refs {
		if _, err := s.insEdge.Exec(key, rel, r.Line, r.Target, r.Rel); err != nil {
			return nil, err
		}
	}
	if _, err := s.upFile.Exec(key, rel, mtime, hash); err != nil {
		return nil, err
	}
	return nil, nil
}

// docEdges grava as arestas de um Markdown já indexado, resolvendo cada referência contra o índice recém-atualizado
// (ix). Uma aresta por (relação, alvo): a linha guardada é a da primeira menção, que é a que a linha "docs:" mostra.
func (s *stmts) docEdges(key, rel string, refs []DocRef, ix *docIndex) error {
	seen := map[string]bool{}
	for _, r := range refs {
		t, _ := ix.refTarget(rel, r)
		if t == "" {
			continue
		}
		k := r.Kind + "\x00" + t
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := s.insEdge.Exec(key, rel, r.Line, t, r.Kind); err != nil {
			return err
		}
	}
	return nil
}

// indexableRoot: raiz que o índice aceita — repositório git (comportamento de sempre) ou pasta avulsa existente.
func indexableRoot(root string) bool {
	if isRepoRoot(root) {
		return true
	}
	_, ok := ResolveStandaloneRoot(root)
	return ok
}

// Reindex apaga o índice do repositório (ou da pasta avulsa) e indexa tudo de novo.
func Reindex(root string) (Stats, error) {
	if !indexableRoot(root) {
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
		`DELETE FROM code_meta WHERE repo_root = ?`, // sem isto o estado antigo diria "em dia" para um índice vazio
	} {
		if _, err := d.Exec(q, key); err != nil {
			return Stats{}, err
		}
	}
	return Refresh(root)
}

// ReindexFile atualiza um único arquivo (ou o remove do índice, se sumiu), numa transação.
func ReindexFile(root, absPath string) error {
	if !indexableRoot(root) {
		return ErrNotInRepo(root)
	}
	// Pasta avulsa que ainda não tem índice passa pela trava da passada completa: recusada lá, recusada aqui (o hook
	// de gravação só chama isto para raiz já indexada, então a varredura extra só acontece no caminho frio).
	if !isRepoRoot(root) && !indexed(root) && !standaloneFits(root) {
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
	if tooBig(rel, info.Size()) {
		return nil
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	refs, err := s.index(key, rel, src, info.ModTime().UnixNano(), contentHash(src))
	if err != nil {
		return err
	}
	if refs != nil {
		ix, err := loadDocIndex(tx, key, root, false)
		if err != nil {
			return err
		}
		if err := s.docEdges(key, rel, refs, ix); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TrackedFiles conta os arquivos indexados na raiz (é o N do aviso de pasta avulsa).
func TrackedFiles(root string) int {
	d, err := store()
	if err != nil {
		return 0
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM code_files WHERE repo_root = ?`, repoKey(root)).Scan(&n)
	return n
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
	if !indexable(filepath.ToSlash(path)) || !exists() {
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
