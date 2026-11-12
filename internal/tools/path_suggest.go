package tools

// path_suggest.go: o read_file de um arquivo que não existe devolve candidatos em vez de só "não encontrado".
// Duas fontes, nesta ordem: (1) o nome na pasta do arquivo e nos ancestrais até a raiz do repositório (ou 3 níveis
// acima, sem repositório) — mesmo nome base ignorando maiúsculas, extensão trocada, erro de digitação pequeno;
// (2) o índice do grafo, quando existe, procurando o nome base como símbolo (é o que acha `LevelOutputFormat.cs`
// quando o modelo adivinhou `Casing.cs`: lá o nome do arquivo não se parece com o pedido, mas o símbolo existe).
// Nada é lido no lugar do pedido: só caminhos, um por linha, e o conteúdo continua sendo do arquivo certo.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homelab/fastexec/internal/graph"
)

// maxSuggestions é o teto de candidatos devolvidos, e maxDirEntries o teto de entradas que uma pasta pode ter para
// ser varrida (uma pasta gigante não é candidata a nada).
const (
	maxSuggestions = 5
	maxDirEntries  = 5000
	maxUpLevels    = 3 // ancestrais olhados quando não há repositório git
	minFuzzyLen    = 6 // nome mais curto que isto só casa igual (ou com a mesma raiz e outra extensão)
)

type pathCand struct {
	path  string
	note  string // "(declares Casing)", "(nearest existing folder)" — vazio na maioria
	score int
}

func render(c pathCand) string {
	if c.note == "" {
		return c.path
	}
	return c.path + " (" + c.note + ")"
}

// suggestMissing lista caminhos existentes parecidos com o que foi pedido, no máximo maxSuggestions.
func suggestMissing(missing string) []pathCand {
	if abs, err := filepath.Abs(missing); err == nil {
		missing = abs
	}
	dir, base := filepath.Dir(missing), filepath.Base(missing)
	lb := strings.ToLower(base)
	stem := strings.TrimSuffix(lb, filepath.Ext(lb))
	// ostem mantém a caixa que o agente escreveu: é o que a mensagem mostra ("declares Casing"), o mesmo nome que o
	// índice guarda. A busca compara sem caixa nos dois lados, então tanto faz para achar.
	ostem := strings.TrimSuffix(base, filepath.Ext(base))

	root, inRepo := graph.ResolveRepoRoot(missing)

	var cs []pathCand
	seen := map[string]bool{}
	add := func(p, note string, score int) {
		k := strings.ToLower(filepath.Clean(p))
		if seen[k] {
			return
		}
		seen[k] = true
		cs = append(cs, pathCand{path: p, note: note, score: score})
	}

	// (1) nome: a pasta do arquivo e os ancestrais. O nível pesa no score, então o acerto na pasta certa vem antes
	// do mesmo acerto três níveis acima. Uma pasta que não existe é pulada (é o caso do caminho inventado).
	walked := false
	for d, level := dir, 0; ; {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			for _, c := range dirCandidates(d, lb, stem) {
				add(c.path, "", c.score+level*3)
			}
			level++
			if inRepo && samePath(d, root) {
				break
			}
			if !inRepo && level > maxUpLevels {
				break
			}
		} else {
			walked = true
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}

	// (2) grafo: o nome base como símbolo declarado. Só com índice já existente, e conferindo o disco — o índice
	// pode citar arquivo que já sumiu.
	if inRepo && len(stem) >= 3 {
		for _, rel := range graph.SymbolFiles(root, ostem, maxSuggestions) {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if st, err := os.Stat(p); err != nil || st.IsDir() {
				continue
			}
			add(p, "declares "+ostem, 20)
		}
	}

	if len(cs) == 0 && walked {
		cs = append(cs, pathCand{path: dir + string(filepath.Separator), note: "nearest existing folder"})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].score < cs[j].score })
	if len(cs) > maxSuggestions {
		cs = cs[:maxSuggestions]
	}
	return cs
}

// dirCandidates acha, numa pasta só, os nomes parecidos com o pedido: mesmo nome (só maiúsculas diferentes),
// mesma raiz com outra extensão, digitação próxima, ou o nome contido no arquivo.
func dirCandidates(dir, lb, stem string) []pathCand {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > maxDirEntries {
		return nil
	}
	var cs []pathCand
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		s := -1
		switch {
		case n == lb:
			s = 0 // só diferença de maiúsculas
		case len(stem) >= 3 && strings.TrimSuffix(n, filepath.Ext(n)) == stem:
			s = 1 // outra extensão
		default:
			// O parecido só vale para nome com tamanho de nome: em nome curto ("x.txt") a distância 1 alcança
			// qualquer coisa e a lista enche de arquivo que nada tem a ver (a pasta temp do sistema é o exemplo).
			if d := levenshtein(n, lb); len(lb) >= minFuzzyLen && d <= max(2, len(lb)/4) {
				s = 2 + d
			} else if len(stem) >= minFuzzyLen && strings.Contains(n, stem) {
				s = 8
			}
		}
		if s >= 0 {
			cs = append(cs, pathCand{path: filepath.Join(dir, e.Name()), score: s})
		}
	}
	return cs
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > 64 || len(rb) > 64 {
		return 99
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// notFoundWithSuggestions enrich a local "not found" error with the candidates, one per line.
func notFoundWithSuggestions(path string, err error) error {
	if !os.IsNotExist(err) {
		return err
	}
	cs := suggestMissing(path)
	if len(cs) == 0 {
		return err
	}
	var b strings.Builder
	b.WriteString(err.Error())
	b.WriteString("\nnot found; did you mean: ")
	b.WriteString(render(cs[0]))
	for _, c := range cs[1:] {
		b.WriteString("\n  ")
		b.WriteString(render(c))
	}
	return &suggestErr{err: err, msg: b.String()}
}

type suggestErr struct {
	err error
	msg string
}

func (e *suggestErr) Error() string { return e.msg }
func (e *suggestErr) Unwrap() error { return e.err }
