package graph

// query.go: consultas do grafo (callers, def, impact). Cada uma antes deixa o índice em dia com Refresh incremental,
// então a resposta reflete o disco. Saída enxuta "caminho:linha (relação)". Impacto de arquivo Go respeita
// visibilidade: mesmo pacote vê tudo; outro pacote só os exportados, e só se importar o pacote.

import (
	"database/sql"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const DefaultMax = 200

// fewDirectCallerFiles é o teto de arquivos não-teste (distintos) que conta como "poucos chamadores" na dica de
// depth: com até 3 arquivos diretos, o que responde a pergunta provavelmente está a um ou dois saltos.
const fewDirectCallerFiles = 3

// deepHint monta a dica de depth para uma resposta de depth 1. Ela só é anexada quando vale os ~50 bytes: nenhum
// arquivo de teste no resultado, poucos arquivos não-teste, e o fecho até depth 3 com MAIS símbolos do que a lista
// direta — ou seja, quando o que responde está mesmo escondido. Sem esse último critério a linha apareceria em toda
// consulta pequena (inclusive numa cadeia A->B que acaba ali), que é justamente o gasto que a regra evita.
func deepHint(d *sql.DB, root, name string, max int, tests []string, others int) string {
	if len(tests) > 0 || others > fewDirectCallerFiles {
		return ""
	}
	levels, _, _, _, err := callerLevels(d, root, name, max, 3)
	if err != nil || len(levels) == 0 {
		return ""
	}
	deeper := 0
	for _, l := range levels[1:] {
		deeper += len(l)
	}
	if deeper <= len(levels[0]) {
		return ""
	}
	return fmt.Sprintf("\ntransitive: depth=3 reaches %d more symbols.", deeper)
}

func lastName(sym string) string {
	if i := strings.LastIndex(sym, "."); i >= 0 && i < len(sym)-1 {
		return sym[i+1:]
	}
	return sym
}

func indexNote(st Stats) string {
	if st.Changed == 0 && st.Removed == 0 {
		return ""
	}
	return fmt.Sprintf("\n[index] %d file(s) reindexed, %d removed in %d ms (%d files tracked)", st.Changed, st.Removed, st.Took.Milliseconds(), st.Files)
}

func ready(root string) (*sql.DB, Stats, error) {
	st, err := Refresh(root)
	if err != nil {
		return nil, st, err
	}
	d, err := store()
	return d, st, err
}

var goReceiverRe = regexp.MustCompile(`^func\s*\(\s*(?:\w+\s+)?\*?\s*([A-Za-z_]\w*)(?:\[[^\]]*\])?\s*\)`)

// callerLabel turns the "name<US>signature" of an enclosing symbol into the name shown to the agent: Go methods
// carry their receiver type (Type.method), so same-named methods of different types stay distinguishable.
func callerLabel(nameAndSig string) string {
	name, sig, _ := strings.Cut(nameAndSig, "\x1f")
	if m := goReceiverRe.FindStringSubmatch(sig); m != nil {
		return m[1] + "." + name
	}
	return name
}

// Callers lista onde o símbolo é chamado/referenciado. depth 1 (padrão e comportamento de sempre) é um salto só;
// depth>1 devolve o fecho transitivo agrupado por nível, com a linha "tests:" e o tratamento de homônimos
// (ver transitive.go).
func Callers(root, symbol string, max, depth int) (string, error) {
	if max <= 0 {
		max = DefaultMax
	}
	d, st, err := ready(root)
	if err != nil {
		return "", err
	}
	name := lastName(symbol)
	if depth > 1 {
		return deepCallers(d, st, root, name, max, depth)
	}
	// homônimos: o mesmo nome declarado em mais de um tipo (ver homonyms.go)
	decl := declaringTypes(d, root, name)
	homonym := len(decl) >= 2
	want := ""
	if homonym {
		want = qualifierType(symbol, decl)
	}
	src := &srcLines{root: root}
	// the innermost symbol that contains the call site is the caller: naming it saves the agent a read per line
	rows, err := d.Query(`SELECT e.source_file, e.source_line, e.relation_type,
			COALESCE((SELECT s.name || char(31) || s.signature FROM code_symbols s
				WHERE s.repo_root = e.repo_root AND s.file_path = e.source_file
					AND s.start_line <= e.source_line AND s.end_line >= e.source_line
				ORDER BY s.end_line - s.start_line LIMIT 1), ''),
			COALESCE((SELECT t.name FROM code_symbols t
				WHERE t.repo_root = e.repo_root AND t.file_path = e.source_file AND t.kind IN (`+typeKindsSQL+`)
					AND t.start_line <= e.source_line AND t.end_line >= e.source_line
				ORDER BY t.end_line - t.start_line LIMIT 1), '')
		FROM code_edges e
		WHERE e.repo_root = ? AND e.target_symbol = ? AND e.relation_type IN ('calls','references','renders')
		ORDER BY e.source_file, e.source_line`, repoKey(root), name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	files := map[string]bool{}
	excluded := map[string]int{}
	total := 0
	for rows.Next() {
		var f, rel, in, typ string
		var line int
		if err := rows.Scan(&f, &line, &rel, &in, &typ); err != nil {
			return "", err
		}
		label := ""
		if in != "" {
			label = callerLabel(in)
			if typ != "" && !strings.Contains(label, ".") {
				label = typ + "." + label
			}
		}
		via := ""
		if homonym {
			recv, found := receiverOf(src.line(f, line), name)
			guess := ""
			if found {
				guess = guessType(recv, typ, decl)
			}
			if want != "" && guess != "" && guess != want {
				excluded[guess]++
				continue
			}
			if found {
				if recv != "" {
					via = " via " + recv
				}
				if guess != "" {
					via += " -> " + guess
				} else {
					via += " -> ?"
				}
			}
		}
		total++
		files[f] = true
		if total <= max {
			if label != "" {
				label = " in " + label
			}
			fmt.Fprintf(&b, "- %s:%d%s%s (%s)\n", f, line, label, via, rel)
		}
	}
	if total == 0 {
		return fmt.Sprintf("(no callers of '%s' in %d indexed files)", name, st.Files) + indexNote(st), nil
	}
	if total > max {
		fmt.Fprintf(&b, "... %d more (raise 'max')\n", total-max)
	}
	head := fmt.Sprintf("=== callers of '%s' ===\n", name)
	tests, others := splitTestFiles(files)
	if len(tests) > 0 { // só quando existe teste: sem isso, depth 1 continua com a saída de sempre
		head += fmt.Sprintf("tests: %d files: %s\n", len(tests), strings.Join(tests, ", "))
	}
	if homonym {
		head += fmt.Sprintf("NOTE: '%s' is declared in %d types (%s); references are matched by name. 'via' is the call's receiver and '->' the type it points to by the receiver's name ('?' = undecided). Pass symbol='Type.%s' to keep only one type's.\n", name, len(decl), strings.Join(decl, ", "), name)
	}
	tail := fmt.Sprintf("Total: %d references in %d files.", total, len(files))
	if len(excluded) > 0 {
		var ks []string
		n := 0
		for k, v := range excluded {
			ks = append(ks, fmt.Sprintf("%s (%d)", k, v))
			n += v
		}
		sort.Strings(ks)
		tail += fmt.Sprintf("\n[excluded %d reference(s) whose receiver points to another type: %s]", n, strings.Join(ks, ", "))
	}
	if total > 0 {
		tail += deepHint(d, root, name, max, tests, others)
	}
	return head + b.String() + tail + indexNote(st), nil
}

// Definition mostra arquivo, intervalo de linhas e assinatura de cada declaração com esse nome.
func Definition(root, symbol string) (string, error) {
	d, st, err := ready(root)
	if err != nil {
		return "", err
	}
	name := lastName(symbol)
	key := repoKey(root)
	rows, err := d.Query(`SELECT file_path, start_line, end_line, kind, signature FROM code_symbols
		WHERE repo_root = ? AND name = ? ORDER BY file_path, start_line`, key, name)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for rows.Next() {
		var f, kind, sig string
		var s, e int
		if err := rows.Scan(&f, &s, &e, &kind, &sig); err != nil {
			rows.Close()
			return "", err
		}
		n++
		fmt.Fprintf(&b, "- %s:%d-%d (%s)\n  %s\n", f, s, e, kind, sig)
	}
	rows.Close()
	if n > 0 {
		return fmt.Sprintf("=== definition of '%s' ===\n%s", name, strings.TrimRight(b.String(), "\n")) + indexNote(st), nil
	}
	var similar []string
	if r2, err := d.Query(`SELECT DISTINCT name FROM code_symbols WHERE repo_root = ? AND name LIKE ? LIMIT 8`, key, "%"+name+"%"); err == nil {
		for r2.Next() {
			var s string
			if r2.Scan(&s) == nil {
				similar = append(similar, s)
			}
		}
		r2.Close()
	}
	msg := fmt.Sprintf("(no definition of '%s' in %d indexed files)", name, st.Files)
	if len(similar) > 0 {
		msg = fmt.Sprintf("(no definition of '%s'; similar: %s)", name, strings.Join(similar, ", "))
	}
	return msg + indexNote(st), nil
}

// Impact lista os arquivos afetados por mudar um arquivo (quem usa o que ele declara ou o importa) ou um símbolo.
func Impact(root, target string, max int) (string, error) {
	if max <= 0 {
		max = DefaultMax
	}
	d, st, err := ready(root)
	if err != nil {
		return "", err
	}
	key := repoKey(root)
	rel, isFile := fileTarget(d, root, key, target)
	var uses map[string][]string // arquivo → símbolos usados ("imports" quando só importa)
	if isFile {
		uses, err = fileImpact(d, key, rel)
	} else {
		rel = lastName(target)
		uses, err = symbolImpact(d, key, rel)
	}
	if err != nil {
		return "", err
	}
	if len(uses) == 0 {
		return fmt.Sprintf("(nothing in %d indexed files depends on '%s')", st.Files, rel) + indexNote(st), nil
	}
	files := make([]string, 0, len(uses))
	for f := range uses {
		files = append(files, f)
	}
	sort.Strings(files)
	var b strings.Builder
	for i, f := range files {
		if i == max {
			fmt.Fprintf(&b, "... %d more files (raise 'max')\n", len(files)-max)
			break
		}
		fmt.Fprintf(&b, "- %s (%s)\n", f, strings.Join(uses[f], ", "))
	}
	return fmt.Sprintf("=== impact of '%s' ===\n%sTotal: %d files.", rel, b.String(), len(files)) + indexNote(st), nil
}

// fileTarget diz se target é um arquivo indexado do repositório e devolve o caminho relativo.
func fileTarget(d *sql.DB, root, key, target string) (string, bool) {
	rel := target
	if filepath.IsAbs(target) {
		r, err := filepath.Rel(root, target)
		if err != nil {
			return "", false
		}
		rel = r
	}
	rel = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "./")
	var one int
	if d.QueryRow(`SELECT 1 FROM code_files WHERE repo_root = ? AND file_path = ?`, key, rel).Scan(&one) != nil {
		return "", false
	}
	return rel, true
}

func symbolImpact(d *sql.DB, key, name string) (map[string][]string, error) {
	rows, err := d.Query(`SELECT source_file, COUNT(*) FROM code_edges WHERE repo_root = ? AND target_symbol = ?
		AND relation_type IN ('calls','references','renders') GROUP BY source_file`, key, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var f string
		var n int
		if err := rows.Scan(&f, &n); err != nil {
			return nil, err
		}
		out[f] = []string{fmt.Sprintf("%d refs", n)}
	}
	return out, nil
}

func fileImpact(d *sql.DB, key, rel string) (map[string][]string, error) {
	exported := map[string]bool{}
	rows, err := d.Query(`SELECT name, is_exported FROM code_symbols WHERE repo_root = ? AND file_path = ? AND kind != 'route'`, key, rel)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		var e bool
		if rows.Scan(&n, &e) == nil {
			exported[n] = e
		}
	}
	rows.Close()

	importers := map[string]bool{}
	rows, err = d.Query(`SELECT DISTINCT source_file, target_symbol FROM code_edges
		WHERE repo_root = ? AND relation_type = 'imports' AND source_file != ?`, key, rel)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, imp string
		if rows.Scan(&src, &imp) == nil && importResolves(src, imp, rel) {
			importers[src] = true
		}
	}
	rows.Close()

	// <script src>/<link href> apontam para o arquivo por caminho, não por pacote: o alvo é resolvido relativo a
	// quem referencia (assetResolves), e quem referencia entra como dependente mesmo sem usar símbolo nenhum.
	htmlUsers := map[string]bool{}
	rows, err = d.Query(`SELECT DISTINCT source_file, target_symbol FROM code_edges
		WHERE repo_root = ? AND relation_type = 'html_ref' AND source_file != ?`, key, rel)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, asset string
		if rows.Scan(&src, &asset) == nil && assetResolves(src, asset, rel) {
			htmlUsers[src] = true
		}
	}
	rows.Close()

	used := map[string]map[string]bool{}
	if len(exported) > 0 {
		names := make([]any, 0, len(exported)+2)
		names = append(names, key, rel)
		marks := make([]string, 0, len(exported))
		for n := range exported {
			names = append(names, n)
			marks = append(marks, "?")
		}
		rows, err = d.Query(`SELECT DISTINCT source_file, target_symbol FROM code_edges WHERE repo_root = ? AND source_file != ?
			AND relation_type IN ('calls','references','renders') AND target_symbol IN (`+strings.Join(marks, ",")+`)`, names...)
		if err != nil {
			return nil, err
		}
		isGo := strings.HasSuffix(rel, ".go")
		// nome de classe/id de CSS não é escopado por import (a folha de estilo é global, ligada por <link> ou
		// className): para arquivo de estilo, quem usa um dos nomes entra, importando ou não.
		style := isStyleFile(rel)
		for rows.Next() {
			var src, sym string
			if rows.Scan(&src, &sym) != nil {
				continue
			}
			visible := importers[src] || style
			if isGo {
				samePkg := strings.HasSuffix(src, ".go") && path.Dir(src) == path.Dir(rel)
				visible = samePkg || (importers[src] && exported[sym])
			}
			if !visible {
				continue
			}
			if used[src] == nil {
				used[src] = map[string]bool{}
			}
			used[src][sym] = true
		}
		rows.Close()
	}

	out := map[string][]string{}
	for src, syms := range used {
		list := make([]string, 0, len(syms))
		for s := range syms {
			list = append(list, s)
		}
		sort.Strings(list)
		out[src] = []string{"uses: " + strings.Join(list, ", ")}
	}
	if !strings.HasSuffix(rel, ".go") {
		for src := range importers {
			if _, ok := out[src]; !ok {
				out[src] = []string{"imports"}
			}
		}
	}
	for src := range htmlUsers {
		if _, ok := out[src]; !ok {
			out[src] = []string{"html_ref"}
		}
	}
	return out, nil
}

// isStyleFile: folha de estilo. O nome declarado nela (.card, #main, --cor) vale no documento inteiro.
func isStyleFile(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".css", ".scss", ".less":
		return true
	}
	return false
}

// assetResolves diz se o asset referenciado em src (src de <script>/<link>, caminho relativo ao repositório) é o
// arquivo f. O alvo é relativo a quem referencia — é o que <script src="../js/app.js"> significa —, e "/x" conta a
// partir da raiz do repositório. Sem extensão também casa ("./app" para app.js), como nos imports de TS.
func assetResolves(src, asset, f string) bool {
	if asset == "" {
		return false
	}
	p := path.Clean(asset)
	if !strings.HasPrefix(asset, "/") {
		p = path.Clean(path.Join(path.Dir(src), asset))
	}
	if p == f {
		return true
	}
	return path.Ext(p) == "" && p == strings.TrimSuffix(f, path.Ext(f))
}

// importResolves diz se o import imp, feito em src, aponta para o arquivo f (caminhos relativos ao repositório).
func importResolves(src, imp, f string) bool {
	ext := path.Ext(f)
	fNoExt := strings.TrimSuffix(f, ext)
	switch ext {
	case ".go":
		dir := path.Dir(f)
		return imp == dir || strings.HasSuffix(imp, "/"+dir)
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		if !strings.HasPrefix(imp, ".") {
			return false
		}
		p := path.Clean(path.Join(path.Dir(src), imp))
		pNoExt := strings.TrimSuffix(p, path.Ext(p))
		return p == f || pNoExt == fNoExt || p == strings.TrimSuffix(fNoExt, "/index")
	case ".py":
		dots := len(imp) - len(strings.TrimLeft(imp, "."))
		mod := strings.ReplaceAll(strings.TrimLeft(imp, "."), ".", "/")
		if dots > 0 {
			base := path.Dir(src)
			for i := 1; i < dots; i++ {
				base = path.Dir(base)
			}
			p := path.Join(base, mod)
			return p == fNoExt || p+"/__init__" == fNoExt
		}
		return fNoExt == mod || strings.HasSuffix(fNoExt, "/"+mod) || fNoExt == mod+"/__init__"
	default:
		return path.Clean(path.Join(path.Dir(src), imp)) == f
	}
}
