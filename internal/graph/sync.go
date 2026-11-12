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

// extractorVersion é a versão do que os scanners GRAVAM (declarações, arestas, coluna da referência). O frescor
// incremental é por (mtime, hash) do arquivo, então arquivo parado nunca seria re-lido e um parser corrigido não
// chegaria ao índice já existente: a versão é o único gatilho que faz a re-extração acontecer uma vez por
// repositório. Suba o número SEMPRE que a saída de um scanner mudar de forma que o índice antigo fique errado.
const extractorVersion = 2

// staleMtime marca em code_files a linha cuja extração veio de OUTRA versão do extrator: com mtime negativo o
// frescor por arquivo não tem como casar (mtime e hash do arquivo em disco são sempre >= 0), então o arquivo é
// relido e reextraído uma vez. O próprio index() reescreve mtime e hash reais, e é assim que a marca desaparece.
const staleMtime = -1

type meta struct {
	head  string
	work  string
	files int
	docs  int // Markdown rastreados na passada completa: é o que mantém Stats.Docs verdadeiro no caminho "Fresh"
	ev    int // versão do extrator que produziu este índice
}

func readMeta(d *sql.DB, key string) (meta, bool) {
	var m meta
	err := d.QueryRow(`SELECT head, work, files, docs, ev FROM code_meta WHERE repo_root = ?`, key).
		Scan(&m.head, &m.work, &m.files, &m.docs, &m.ev)
	return m, err == nil
}

// sameState diz se o estado já gravado é o do repositório agora — o teste barato que abre toda passada (o porcelano
// cobre exatamente o que o índice segue). É o mesmo teste das duas pontas: o Sync e o disparo da indexação de fundo.
// Extrator de outra versão não é o mesmo estado: o índice tem de ser refeito antes de responder.
func sameState(d *sql.DB, key, head, work string) bool {
	m, ok := readMeta(d, key)
	return ok && m.head == head && m.work == work && m.files > 0 && m.ev == extractorVersion
}

func writeMeta(d *sql.DB, key, head, work string, files, docs int) error {
	_, err := d.Exec(`INSERT INTO code_meta (repo_root, head, work, files, docs, ev, checked_at) VALUES (?,?,?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(repo_root) DO UPDATE SET head = excluded.head, work = excluded.work, files = excluded.files,
			docs = excluded.docs, ev = excluded.ev, checked_at = CURRENT_TIMESTAMP`, key, head, work, files, docs, extractorVersion)
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

// Sync aceita repositório git (o comportamento de sempre) e pasta avulsa: fora de git, a varredura do disco É o
// frescor, e a pasta acima de maxStandaloneFiles/maxStandaloneBytes é recusada com a mensagem de sempre.
// Deixa o índice em dia dentro do teto budget (0 = sem teto, o comportamento de sempre). Devolve Fresh=true
// quando o estado do repositório era o mesmo da última passada (nada foi varrido) e OverBudget=true quando o teto
// estourou (o índice pode estar incompleto: não responda dele).
func Sync(root string, budget time.Duration) (Stats, error) {
	return syncWith(root, syncOpts{budget: budget})
}

// syncOpts são as opções da passada que só a indexação de fundo usa (zero = comportamento de sempre): o contador
// N/M que a consulta mostra enquanto espera, e o cancelamento pedido no fechamento do processo.
type syncOpts struct {
	budget    time.Duration
	progress  func(done, total int)
	cancelled func() bool
}

func syncWith(root string, o syncOpts) (Stats, error) {
	start := time.Now()
	// Fora de repositório git a pasta é indexada como avulsa (chave própria, o caminho normalizado): sem HEAD e sem
	// git status, o frescor vem só da comparação (mtime, hash) de cada arquivo — que é o que a varredura já faz.
	standalone := !isRepoRoot(root)
	if standalone {
		if _, ok := ResolveStandaloneRoot(root); !ok {
			return Stats{}, ErrNotInRepo(root)
		}
	}
	d, err := store()
	if err != nil {
		return Stats{}, err
	}
	key := repoKey(root)
	head, work, have := "", "", false
	if !standalone {
		head, work, have = gitState(root)
	}
	if have {
		// O estado só é gravado no fim de uma passada completa (que inclui a segunda passada das arestas de doc),
		// então estado igual dispensa a varredura sem deixar Markdown pendente para trás. O estado carrega também a
		// contagem de Markdown daquela passada: sem ela a resposta fresca diria "0 docs" e as consultas esconderiam
		// toda a documentação (é o gate st.Docs > 0).
		if m, ok := readMeta(d, key); ok && sameState(d, key, head, work) {
			return Stats{Files: m.files, Docs: m.docs, Fresh: true, Took: time.Since(start)}, nil
		}
	}
	deadline := time.Time{}
	if o.budget > 0 {
		deadline = start.Add(o.budget)
	}
	// Cancelamento (fechamento do processo) para a passada como o teto de tempo para: o que já entrou na transação
	// fica, o estado não é gravado e quem chamou sabe que o índice pode estar incompleto.
	over := func() bool {
		if o.cancelled != nil && o.cancelled() {
			return true
		}
		return !deadline.IsZero() && time.Now().After(deadline)
	}
	partial := func(files int) (Stats, error) {
		if have {
			_, _ = d.Exec(`DELETE FROM code_meta WHERE repo_root = ?`, key) // índice incompleto: estado não vale
		}
		return Stats{Files: files, OverBudget: true, Took: time.Since(start)}, nil
	}

	var files []string
	var total int
	if standalone {
		var tooBig bool
		files, total, tooBig = listFiles(root, maxStandaloneFiles, maxStandaloneBytes)
		if tooBig {
			return Stats{}, ErrNotInRepo(root) // grande demais para avulsa: a recusa é a de sempre, sem promessa nova
		}
	} else {
		files, total, _ = listFiles(root, maxRepoFiles, 0)
		if total > maxRepoFiles {
			return Stats{}, ErrTooManyFiles(total)
		}
	}
	if over() {
		return partial(len(files))
	}

	// Índice de outra versão do extrator: a base de frescor por arquivo é marcada como vencida ANTES da varredura
	// (mtime = staleMtime, ver o comentário da constante) e o estado é apagado na hora. Marcar em vez de apagar
	// mantém o nome de cada arquivo, que é o que a passada usa para achar o que sumiu do disco; apagar o estado na
	// hora é o que impede a marca de ser reaplicada na passada seguinte (uma passada cortada pelo teto continua de
	// onde parou, em vez de recomeçar a marcação para sempre).
	if m, ok := readMeta(d, key); ok && m.ev != extractorVersion {
		if _, err := d.Exec(`UPDATE code_files SET mtime = ? WHERE repo_root = ? AND mtime >= 0`, staleMtime, key); err != nil {
			return Stats{}, err
		}
		_, _ = d.Exec(`DELETE FROM code_meta WHERE repo_root = ?`, key)
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
	scanned := 0
	for _, rel := range files {
		if over() {
			budgetHit = true
			break
		}
		scanned++
		if o.progress != nil {
			o.progress(scanned, len(files))
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
		stale := ok && rec.mtime < 0 // linha de outra versão do extrator: (mtime, hash) antigos não valem
		if ok && !stale && rec.mtime == mtime {
			continue
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		h := contentHash(src)
		if ok && !stale && rec.hash == h {
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
	// próxima chamada pular a passada que ainda falta. Fora de git (pasta avulsa, ou git que não respondeu) grava
	// assim mesmo, com head/work vazios: o frescor de lá é por arquivo e não olha este estado, mas é ele que
	// registra a versão do extrator daquele índice — sem isso uma pasta avulsa nunca seria reextraída.
	if !have {
		_ = writeMeta(d, key, head, work, len(files), st.Docs)
	} else if h2, w2, ok2 := gitState(root); ok2 && h2 == head && w2 == work {
		_ = writeMeta(d, key, head, work, len(files), st.Docs)
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
