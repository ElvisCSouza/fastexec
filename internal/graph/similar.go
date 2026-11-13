package graph

// similar.go: o code_outline de um símbolo que não existe responde com os nomes parecidos que o índice conhece.
// Só consulta: nunca indexa (uma pergunta que já falhou não paga passada de frescor) e nunca lê o disco (quem chama
// confere o arquivo antes de mostrar). Parecido = o nome do índice está contido no pedido (LevelOverride em
// LoggerLevelOverrideMap) ou divide com ele ao menos uma parte de CamelCase de 4+ letras (Level, Override, Map).

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SimilarSymbol é uma declaração do índice cujo nome se parece com o pedido.
type SimilarSymbol struct {
	File      string // relativo à raiz do repositório, com barras
	Name      string
	Signature string
	Line      int
	Score     int
}

const similarMaxParts = 8 // partes de CamelCase do pedido usadas na busca (as demais só pesam menos no SQL)

// SimilarSymbols devolve até max declarações do índice parecidas com want, da mais parecida para a menos. Vazio se
// o repositório não está indexado ou se nada se parece.
func SimilarSymbols(root, want string, max int) []SimilarSymbol {
	if root == "" || want == "" || max <= 0 || !exists() {
		return nil
	}
	d, err := store()
	if err != nil {
		return nil
	}
	// O SQL é um filtro largo (qualquer nome contido no pedido ou com uma parte longa em comum); quem decide é
	// similarScore, que é o mesmo critério para todas as linguagens.
	conds := []string{"(length(name) >= 4 AND instr(lower(?), lower(name)) > 0)"}
	args := []any{repoKey(root), want}
	for _, p := range longParts(want) {
		conds = append(conds, "name LIKE ?")
		args = append(args, "%"+p+"%")
	}
	rows, err := d.Query(`SELECT file_path, name, signature, start_line FROM code_symbols
		WHERE repo_root = ? AND (`+strings.Join(conds, " OR ")+`) LIMIT 2000`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []SimilarSymbol
	for rows.Next() {
		var f, n, sig string
		var line int
		if rows.Scan(&f, &n, &sig, &line) != nil {
			continue
		}
		sc := similarScore(want, n)
		if sc == 0 {
			continue
		}
		k := f + "\x00" + n + "\x00" + strconv.Itoa(line)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, SimilarSymbol{File: f, Name: n, Signature: sig, Line: line, Score: sc})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// similarScore: 0 se o nome não se parece com o pedido; maior = mais parecido. Nome igual (sem caixa) não conta: esse
// seria um acerto, e o outline já o teria mostrado.
func similarScore(want, name string) int {
	w, n := strings.ToLower(want), strings.ToLower(name)
	if n == "" || n == w {
		return 0
	}
	contained := len(n) >= 4 && strings.Contains(w, n)
	wantParts := map[string]bool{}
	for _, p := range splitParts(want) {
		wantParts[p] = true
	}
	shared, sharedLong := 0, false
	counted := map[string]bool{}
	for _, p := range splitParts(name) {
		if len(p) >= 3 && wantParts[p] && !counted[p] {
			counted[p] = true
			shared++
			if len(p) >= 4 {
				sharedLong = true
			}
		}
	}
	if !contained && !sharedLong {
		return 0
	}
	cover := len(n) * 10 / len(w) // quanto do pedido o nome cobre: desempata LevelOverrideMap à frente de LevelOverride
	if cover > 10 {
		cover = 10
	}
	score := shared*10 + cover
	if contained {
		score += 100
	}
	return score
}

// splitParts quebra um identificador em partes minúsculas: separadores (., _, espaço) e transições de caixa
// (LoggerLevel → logger, level; HTTPServer → http, server). Partes de uma letra também entram; quem filtra é o chamador.
func splitParts(s string) []string {
	rs := []rune(s)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// longParts: as partes de 4+ letras do pedido, sem repetição, no máximo similarMaxParts.
func longParts(want string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range splitParts(want) {
		if len(p) >= 4 && !seen[p] && len(out) < similarMaxParts {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
