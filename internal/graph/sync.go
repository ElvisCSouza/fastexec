package graph

// sync.go: frescor. Antes de responder do índice, o índice é comparado com o repositório. O estado barato é
// (HEAD, hash do `git status --porcelain`), guardado em code_meta a cada passada completa: o porcelano cobre
// exatamente o que o índice segue (arquivos rastreados editados, novos não ignorados e removidos), então estado
// igual = nada a fazer, sem tocar no disco. Estado diferente dispara a passada incremental sob um teto de tempo
// (SyncBudget): quem não couber no teto fica com o que já indexou (cada arquivo é consistente sozinho, e a próxima
// passada continua de onde parou) mas NÃO grava o estado nem é usado para responder — quem chamou volta à busca real.
// Nunca se responde com índice velho.

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homelab/fastexec/internal/codescan"
)

// SyncBudget é o teto da passada de frescor disparada por uma busca: acima disso a resposta pronta não sai a tempo
// e a busca real responde (o FastExec nunca troca uma resposta por uma espera).
const SyncBudget = 900 * time.Millisecond

type meta struct {
	head  string
	work  string
	files int
	docs  int // Markdown rastreados na passada completa: é o que mantém Stats.Docs verdadeiro no caminho "Fresh"
}

func readMeta(d *sql.DB, key string) (meta, bool) {
	var m meta
	err := d.QueryRow(`SELECT head, work, files, docs FROM code_meta WHERE repo_root = ?`, key).
		Scan(&m.head, &m.work, &m.files, &m.docs)
	return m, err == nil
}

func writeMeta(d *sql.DB, key, head, work string, files, docs int) error {
	_, err := d.Exec(`INSERT INTO code_meta (repo_root, head, work, files, docs, checked_at) VALUES (?,?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(repo_root) DO UPDATE SET head = excluded.head, work = excluded.work, files = excluded.files,
			docs = excluded.docs, checked_at = CURRENT_TIMESTAMP`, key, head, work, files, docs)
	return err
}

// gitState devolve HEAD (vazio quando o repositório ainda não tem commit) e o hash do status porcelano. ok=false
// quando o git não responde: aí não há caminho barato e toda consulta varre o disco.
func gitState(root string) (head, work string, ok bool) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD")
	cmd.SysProcAttr = hiddenWindow()
	if b, err := cmd.Output(); err == nil {
		head = strings.TrimSpace(string(b))
	}
	cmd = exec.Command("git", "-C", root, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all")
	cmd.SysProcAttr = hiddenWindow()
	b, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	return head, contentHash(b), true
}

// Sync deixa o índice em dia dentro do teto budget (0 = sem teto, o comportamento de sempre). Devolve Fresh=true
// quando o estado do repositório era o mesmo da última passada (nada foi varrido) e OverBudget=true quando o teto
// estourou (o índice pode estar incompleto: não responda dele).
func Sync(root string, budget time.Duration) (Stats, error) {
	start := time.Now()
	if !isRepoRoot(root) {
		return Stats{}, ErrNotInRepo(root)
	}
	d, err := store()
	if err != nil {
		return Stats{}, err
	}
	key := repoKey(root)
	head, work, have := gitState(root)
	if have {
		// O estado só é gravado no fim de uma passada completa (que inclui a segunda passada das arestas de doc),
		// então estado igual dispensa a varredura sem deixar Markdown pendente para trás. O estado carrega também a
		// contagem de Markdown daquela passada: sem ela a resposta fresca diria "0 docs" e as consultas esconderiam
		// toda a documentação (é o gate st.Docs > 0).
		if m, ok := readMeta(d, key); ok && m.head == head && m.work == work && m.files > 0 {
			return Stats{Files: m.files, Docs: m.docs, Fresh: true, Took: time.Since(start)}, nil
		}
	}
	deadline := time.Time{}
	if budget > 0 {
		deadline = start.Add(budget)
	}
	over := func() bool { return !deadline.IsZero() && time.Now().After(deadline) }
	partial := func(files int) (Stats, error) {
		if have {
			_, _ = d.Exec(`DELETE FROM code_meta WHERE repo_root = ?`, key) // índice incompleto: estado não vale
		}
		return Stats{Files: files, OverBudget: true, Took: time.Since(start)}, nil
	}

	files, total := listFiles(root)
	if total > maxRepoFiles {
		return Stats{}, ErrTooManyFiles(total)
	}
	if over() {
		return partial(len(files))
	}

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
	type pendingDoc struct {
		rel  string
		refs []DocRef
	}
	var docs []pendingDoc
	budgetHit := false
	for _, rel := range files {
		if over() {
			budgetHit = true
			break
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || tooBig(rel, info.Size()) {
			continue
		}
		seen[rel] = true
		if codescan.IsDoc(rel) {
			st.Docs++
		}
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
		refs, err := s.index(key, rel, src, mtime, h)
		if err != nil {
			return st, err
		}
		if refs != nil {
			docs = append(docs, pendingDoc{rel: rel, refs: refs})
		}
		st.Changed++
	}
	if !budgetHit {
		for rel := range known {
			if !seen[rel] {
				if err := s.remove(key, rel); err != nil {
					return st, err
				}
				st.Removed++
			}
		}
	}
	// Segunda passada: as arestas de documentação só podem ser resolvidas com o índice já atualizado (arquivos e
	// símbolos desta transação, sem os que sumiram). Roda também quando o teto cortou a varredura: as arestas de doc
	// dos arquivos processados ficam inteiras na transação e o que faltou é anunciado pelo OverBudget, que proíbe
	// responder do índice.
	if len(docs) > 0 {
		ix, err := loadDocIndex(tx, key, root, false)
		if err != nil {
			return st, err
		}
		for _, pd := range docs {
			if err := s.docEdges(key, pd.rel, pd.refs, ix); err != nil {
				return st, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	if st.Changed+st.Removed >= checkpointAfter {
		_, _ = d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`) // passada grande: devolve o WAL ao banco e zera o arquivo
	}
	if budgetHit {
		return partial(len(files))
	}
	// o estado só é gravado se o repositório não mexeu durante a passada: gravar um estado já vencido faria a
	// próxima chamada pular a passada que ainda falta.
	if have {
		if h2, w2, ok2 := gitState(root); ok2 && h2 == head && w2 == work {
			_ = writeMeta(d, key, head, work, len(files), st.Docs)
		}
	}
	st.Fresh = st.Changed == 0 && st.Removed == 0
	st.Took = time.Since(start)
	return st, nil
}

// Refresh deixa o índice em dia sem teto de tempo (é o que code_graph e as consultas sempre fizeram).
func Refresh(root string) (Stats, error) {
	return Sync(root, 0)
}

// Indexed diz se o repositório já tem índice. O grep pergunta ANTES do Sync: é o que separa "o índice ainda não
// existe" de "o índice estava atrasado" na nota de queda por teto (o índice nascer agora é permitido e anunciado).

func Indexed(root string) bool {
	return indexed(root)
}
