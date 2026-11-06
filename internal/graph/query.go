package graph

// query.go: consultas do grafo (callers, def, impact). Cada uma antes deixa o índice em dia com Refresh incremental,
// então a resposta reflete o disco. Saída enxuta "caminho:linha (relação)". Impacto de arquivo Go respeita
// visibilidade: mesmo pacote vê tudo; outro pacote só os exportados, e só se importar o pacote.

import (
	"database/sql"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultMax = 200

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

// Callers lista onde o símbolo é chamado ou referenciado.
func Callers(root, symbol string, max int) (string, error) {
	if max <= 0 {
		max = DefaultMax
	}
	d, st, err := ready(root)
	if err != nil {
		return "", err
	}
	name := lastName(symbol)
	rows, err := d.Query(`SELECT source_file, source_line, relation_type FROM code_edges
		WHERE repo_root = ? AND target_symbol = ? AND relation_type IN ('calls','references')
		ORDER BY source_file, source_line`, repoKey(root), name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	files := map[string]bool{}
	total := 0
	for rows.Next() {
		var f, rel string
		var line int
		if err := rows.Scan(&f, &line, &rel); err != nil {
			return "", err
		}
		total++
		files[f] = true
		if total <= max {
			fmt.Fprintf(&b, "- %s:%d (%s)\n", f, line, rel)
		}
	}
	if total == 0 {
		return fmt.Sprintf("(no callers of '%s' in %d indexed files)", name, st.Files) + indexNote(st), nil
	}
	if total > max {
		fmt.Fprintf(&b, "... %d more (raise 'max')\n", total-max)
	}
	return fmt.Sprintf("=== callers of '%s' ===\n%sTotal: %d references in %d files.", name, b.String(), total, len(files)) + indexNote(st), nil
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
		AND relation_type IN ('calls','references') GROUP BY source_file`, key, name)
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
			AND relation_type IN ('calls','references') AND target_symbol IN (`+strings.Join(marks, ",")+`)`, names...)
		if err != nil {
			return nil, err
		}
		isGo := strings.HasSuffix(rel, ".go")
		for rows.Next() {
			var src, sym string
			if rows.Scan(&src, &sym) != nil {
				continue
			}
			visible := importers[src]
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
	return out, nil
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
