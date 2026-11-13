package graph

// transitive.go: fecho transitivo dos chamadores ('callers' com depth>1). O grafo casa chamadas só pelo NOME e não
// resolve tipos, então "quem alcança X" é um BFS por nome: nível 1 = quem referencia X; nível 2 = quem referencia os
// símbolos do nível 1, e assim por diante, cada símbolo aparecendo UMA vez (o primeiro nível em que foi achado). Não
// há laço infinito: o símbolo de partida e todo símbolo já listado ficam fora da fronteira, então um ciclo A->B->A
// seca no nível 2. Homônimos reaproveitam homonyms.go: a referência mostra o receptor e o tipo que ele aponta, e a
// saída marca os nomes ambíguos — sem inventar ligação.
//
// Uma linha de arquivo de teste no topo ("tests: N files: ...") responde "quais testes exercitam X" sem outra
// chamada. Ela só aparece quando existe algum arquivo de teste no resultado, então depth=1 sem teste nenhum continua
// com a saída de sempre.

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxCallDepth é o teto de 'depth' em callers: cada nível é uma consulta, e 4 cobre o caso real (o fecho transitivo
// de um projeto grande vira "tudo", que não ajuda o agente).
const MaxCallDepth = 4

// testFilePatterns reconhece arquivo de teste por convenção de nome ou de diretório. Caminhos do grafo são
// relativos ao repositório e com barra normal (ver indexer.go).
var testFilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`_test\.go$`),
	regexp.MustCompile(`\.(test|spec)\.(ts|tsx|js|jsx|mts|cts|mjs|cjs)$`),
	regexp.MustCompile(`(^|/)(test_[^/]*|[^/]*_test)\.py$`),
	regexp.MustCompile(`(^|/)(tests?|__tests__|spec)/`),
}

func isTestFile(p string) bool {
	for _, re := range testFilePatterns {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// testFiles devolve os arquivos de teste do conjunto, em ordem, e o total dos demais.
func splitTestFiles(files map[string]bool) (tests []string, others int) {
	for f := range files {
		if isTestFile(f) {
			tests = append(tests, f)
		} else {
			others++
		}
	}
	sort.Strings(tests)
	return tests, others
}

// callerSite é uma referência do grafo já rotulada: quem chama (label, name) e de onde.
type callerSite struct {
	file  string
	line  int
	rel   string
	label string // "Tipo.método" em Go, senão o nome do símbolo envolvente
	name  string // nome cru do símbolo envolvente, a chave do próximo salto ("" = fora de símbolo)
	via   string
	count int // sites do mesmo símbolo neste nível
}

func (c callerSite) key() string {
	if c.name == "" {
		return "?" + c.file + ":" + strconv.Itoa(c.line) // sem símbolo envolvido: dedup pelo site
	}
	return c.name
}

// levelSites busca as referências diretas a qualquer um dos símbolos da fronteira, uma vez por símbolo. declCache é
// compartilhado com callerLevels (por isso o parâmetro): quem foi declarado em mais de um tipo fica registrado uma
// vez por nível, não uma vez por consulta.
func levelSites(d *sql.DB, root string, targets []string, declCache map[string][]string) ([]callerSite, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(targets)), ",")
	args := make([]any, 0, len(targets)+1)
	args = append(args, repoKey(root))
	for _, t := range targets {
		args = append(args, t)
	}
	rows, err := d.Query(`SELECT e.source_file, e.source_line, e.source_col, e.relation_type, e.target_symbol
		FROM code_edges e
		WHERE e.repo_root = ? AND e.target_symbol IN (`+ph+`) AND e.relation_type IN ('calls','references','renders')
		ORDER BY e.source_file, e.source_line`, args...)
	if err != nil {
		return nil, err
	}
	// as linhas são lidas todas antes de qualquer outra consulta: o pool tem UMA conexão (store.go), então
	// consultar dentro do laço de rows travava o próprio rows aberto.
	type rawSite struct {
		file, rel, target string
		line, col         int
	}
	var raw []rawSite
	for rows.Next() {
		var r rawSite
		if err := rows.Scan(&r.file, &r.line, &r.col, &r.rel, &r.target); err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	src := &srcLines{root: root}
	ix := newEnclosingIndex(d, root, src)
	out := make([]callerSite, 0, len(raw))
	for _, r := range raw {
		s := callerSite{file: r.file, line: r.line, rel: r.rel}
		// o símbolo envolvente sai por linha E coluna (enclosing.go); o nome cru (s.name) é a chave do próximo salto.
		inner, typ := ix.at(r.file, r.line, ix.refCol(r.file, r.line, r.col, r.target))
		if inner != "" {
			s.name, _, _ = strings.Cut(inner, "\x1f")
			s.label = callLabel(inner, typ)
		}
		// homônimos (homonyms.go): só quando o nome chamado é declarado em mais de um tipo; o receptor da chamada
		// diz para qual deles ela aponta ("via x -> Tipo", "-> ?" quando indecidível). Nada é descartado em silêncio.
		decl, ok := declCache[r.target]
		if !ok {
			decl = declaringTypes(d, root, r.target)
			declCache[r.target] = decl
		}
		if len(decl) >= 2 {
			if recv, found := receiverOf(src.line(r.file, r.line), r.target); found {
				if recv != "" {
					s.via = " via " + recv
				}
				if g := guessType(recv, typ, decl); g != "" {
					s.via += " -> " + g
				} else {
					s.via += " -> ?"
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// callerLevels roda o BFS e devolve os níveis já deduplicados, o total de referências e os arquivos envolvidos.
// amb trazem os nomes ambíguos vistos, com quantos tipos os declaram.
func callerLevels(d *sql.DB, root, name string, max, depth int) (levels [][]callerSite, total int, files map[string]bool, amb map[string]int, err error) {
	seen := map[string]bool{name: true} // o símbolo de partida nunca volta: corta ciclo e recursão
	files = map[string]bool{}
	amb = map[string]int{}
	frontier := []string{name}
	declCache := map[string][]string{}
	for lvl := 1; lvl <= depth; lvl++ {
		sites, err := levelSites(d, root, frontier, declCache)
		if err != nil {
			return nil, 0, nil, nil, err
		}
		// o nome que está sendo expandido é o que o grafo casa por nome: se ele existe em mais de um tipo, o nível
		// inteiro é aproximado, e isso vai dito na saída (ver renderDeep).
		for _, t := range frontier {
			if n := len(declCache[t]); n >= 2 && amb[t] < n {
				amb[t] = n
			}
		}
		byKey := map[string]int{}
		var level []callerSite
		var next []string
		for _, s := range sites {
			k := s.key()
			if seen[k] {
				continue
			}
			if i, dup := byKey[k]; dup { // mesmo símbolo, outro site: uma linha só para ele
				level[i].count++
				continue
			}
			seen[k] = true
			s.count = 1
			byKey[k] = len(level)
			if s.name != "" {
				if _, ok := declCache[s.name]; !ok {
					declCache[s.name] = declaringTypes(d, root, s.name)
				}
				if n := len(declCache[s.name]); n >= 2 && amb[s.name] < n {
					amb[s.name] = n
				}
			}
			level = append(level, s)
			files[s.file] = true
			total++
		}
		levels = append(levels, level)
		next = append(next, keysOf(level)...)
		sort.Strings(next)
		if len(next) > max { // a saída é limitada por 'max'; não vale montar uma fronteira gigante
			next = next[:max]
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}
	return levels, total, files, amb, nil
}

func keysOf(level []callerSite) []string {
	out := make([]string, 0, len(level))
	for _, s := range level {
		if s.name != "" {
			out = append(out, s.name)
		}
	}
	return out
}

// deepExtraLines é quantas linhas dos níveis 2+ a saída mostra (o nível 1 só é limitado por 'max'); deepTestFiles, quantos
// arquivos de teste a linha "tests:" nomeia. Um depth=4 num símbolo popular passava de 19 KB (120 referências).
const (
	deepExtraLines = 40
	deepTestFiles  = 8
)

// renderDeep escreve a saída de depth>1: cabeçalho, linha "tests:", cada nível e o rodapé.
func renderDeep(name string, levels [][]callerSite, total int, files map[string]bool, amb map[string]int, depth, max int) string {
	tests, _ := splitTestFiles(files)
	var b strings.Builder
	fmt.Fprintf(&b, "=== callers of '%s' (depth %d) ===\n", name, depth)
	if len(tests) > 0 {
		list := tests
		more := ""
		if len(list) > deepTestFiles {
			more = fmt.Sprintf(", +%d more", len(list)-deepTestFiles)
			list = list[:deepTestFiles]
		}
		fmt.Fprintf(&b, "tests: %d files: %s%s\n", len(tests), strings.Join(list, ", "), more)
	}
	shown, deeper := 0, 0
	for i, level := range levels {
		if len(level) == 0 {
			continue
		}
		fmt.Fprintf(&b, "depth %d:\n", i+1)
		for _, s := range level {
			if shown == max {
				fmt.Fprintf(&b, "... %d more (raise 'max')\n", total-shown)
				shown = -1
				break
			}
			// O nível 1 é o que se edita; do 2 em diante é contexto, e por nome costuma ser aproximado: tem teto próprio.
			if i >= 1 {
				if deeper == deepExtraLines {
					fmt.Fprintf(&b, "... %d more at depth %d+ (use depth=2 or a narrower symbol)\n", total-shown, i+1)
					shown = -1
					break
				}
				deeper++
			}
			where := ""
			if s.label != "" {
				where = " in " + s.label
			}
			sites := ""
			if s.count > 1 {
				sites = fmt.Sprintf(", %d sites", s.count)
			}
			fmt.Fprintf(&b, "- %s:%d%s%s (%s%s)\n", s.file, s.line, where, s.via, s.rel, sites)
			shown++
		}
		if shown < 0 {
			break
		}
	}
	if len(amb) > 0 {
		names := make([]string, 0, len(amb))
		for n, c := range amb {
			names = append(names, fmt.Sprintf("%s (%d types)", n, c))
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "NOTE: ambiguous (declared in >1 type, matched by name): %s.\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "Total: %d references in %d files (%d symbols).", total, len(files), total)
	return b.String()
}

// deepCallers é o 'callers' com profundidade: fecho transitivo agrupado por nível.
func deepCallers(d *sql.DB, st Stats, root, name string, max, depth int) (string, error) {
	if depth > MaxCallDepth {
		depth = MaxCallDepth
	}
	levels, total, files, amb, err := callerLevels(d, root, name, max, depth)
	if err != nil {
		return "", err
	}
	if total == 0 {
		return fmt.Sprintf("(no callers of '%s' in %d indexed files)", name, st.Files) + indexNote(st), nil
	}
	return renderDeep(name, levels, total, files, amb, depth, max) + indexNote(st), nil
}
