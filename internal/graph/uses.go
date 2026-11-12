package graph

// uses.go: consulta pontual de "quem mais usa este símbolo", para a resposta do edit_file/write_file avisar na hora
// quais outros arquivos podem precisar de ajuste. Duas diferenças deliberadas em relação a Callers (query.go):
//
//  1. NÃO chama Refresh. O índice é lido como está — o arquivo editado pode ainda não ter sido reindexado (o Touch é
//     debounced) e é justamente aí que está a informação: os chamadores vivem em OUTROS arquivos, e o nome antigo
//     ainda está indexado com os sites de antes da edição.
//  2. NÃO cria banco. RepoIndexed checa exists() antes de qualquer coisa; quem nunca usou o grafo não ganha um
//     índice por ter editado um arquivo (mesma regra do Touch).
//
// O grafo casa por nome e não resolve tipos, então "usa" é aproximado: o aviso diz isso em uma frase. Nome
// declarado em mais de um lugar (homônimo, ver homonyms.go) vira Ambiguous >= 2 e a lista deixa de ser afirmação.

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

// Site é uma referência de outro arquivo: caminho relativo ao repositório e linha (1-based).
type Site struct {
	File string
	Line int
}

// SymUses é o que o índice sabe sobre os usos de um nome fora do arquivo editado. Sites já vem deduplicado por
// file:line, com os arquivos de teste no fim da lista.
type SymUses struct {
	Name      string
	Sites     []Site
	Files     int // arquivos distintos com uso
	TestFiles int // quantos desses são arquivo de teste (isTestFile, transitive.go)
	Ambiguous int // >=2 = declarado em K lugares distintos: a lista pode ser de outro símbolo
}

// callableKindsSQL são os kinds que contam como "declaração do nome" para o teste de homônimo. Espelha o que
// declaringTypes (homonyms.go) considera, mais os kinds dos scanners de linha (codescan/langs.go).
const callableKindsSQL = `'func','method','function','def'`

// IsTestPath expõe a convenção de arquivo de teste (transitive.go) para quem monta avisos fora deste pacote.
func IsTestPath(p string) bool { return isTestFile(p) }

// RepoIndexed diz se o repositório já tem índice, sem criar banco nenhum (exists() é stat, store() é que cria).
func RepoIndexed(root string) bool {
	if root == "" || !exists() {
		return false
	}
	return indexed(root)
}

// Uses devolve os usos de cada nome em arquivos diferentes de excludeFile. ok=false quando não há índice ou a
// consulta falhou: sem índice não existe resposta, e inventar "0 usos" seria pior que não avisar.
func Uses(root string, names []string, excludeFile string) (map[string]SymUses, bool) {
	if len(names) == 0 || !RepoIndexed(root) {
		return nil, false
	}
	d, err := store()
	if err != nil {
		return nil, false
	}

	// Ambiguidade primeiro: uma consulta por nome, sem rows aberto (o pool tem UMA conexão — store.go).
	out := make(map[string]SymUses, len(names))
	for _, n := range names {
		out[n] = SymUses{Name: n, Ambiguous: ambiguousDefs(d, root, n)}
	}

	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, 0, len(names)+2)
	args = append(args, repoKey(root))
	for _, n := range names {
		args = append(args, n)
	}
	args = append(args, excludeFile)

	// 'imports' entra junto: um rename quebra também quem importa o símbolo, quando o scanner registra o nome.
	// Todas as linhas são lidas antes de qualquer outra consulta (uma conexão só).
	rows, err := d.Query(`SELECT e.target_symbol, e.source_file, e.source_line
		FROM code_edges e
		WHERE e.repo_root = ? AND e.target_symbol IN (`+ph+`)
			AND e.relation_type IN ('calls','references','imports') AND e.source_file <> ?
		ORDER BY e.source_file, e.source_line`, args...)
	if err != nil {
		return nil, false
	}
	type raw struct {
		name, file string
		line       int
	}
	var all []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.name, &r.file, &r.line); err != nil {
			rows.Close()
			return nil, false
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false
	}

	seen := map[string]bool{} // "nome\x1farquivo:linha"
	files := map[string]map[string]bool{}
	for _, r := range all {
		key := r.name + "\x1f" + r.file + ":" + strconv.Itoa(r.line)
		if seen[key] {
			continue
		}
		seen[key] = true
		u := out[r.name]
		u.Sites = append(u.Sites, Site{File: r.file, Line: r.line})
		if files[r.name] == nil {
			files[r.name] = map[string]bool{}
		}
		files[r.name][r.file] = true
		out[r.name] = u
	}
	for name, u := range out {
		sortSites(u.Sites)
		tests, _ := splitTestFiles(files[name])
		u.Files = len(files[name])
		u.TestFiles = len(tests)
		out[name] = u
	}
	return out, true
}

// FileSymUses é um símbolo declarado no arquivo apagado, com os usos que sobraram em outros arquivos.
type FileSymUses struct {
	Symbol string
	Uses   SymUses
}

// DeclaredUses devolve os símbolos declarados em file (o que o índice ainda sabe dele) com os usos em OUTROS
// arquivos. Serve para o executar de um 'rm x.go': o índice é de antes do apagamento, e é assim mesmo — as
// referências nos outros arquivos continuam lá. ok=false sem índice.
func DeclaredUses(root, file string) ([]FileSymUses, bool) {
	if file == "" || !RepoIndexed(root) {
		return nil, false
	}
	d, err := store()
	if err != nil {
		return nil, false
	}
	rows, err := d.Query(`SELECT name FROM code_symbols
		WHERE repo_root = ? AND file_path = ? ORDER BY start_line, name`, repoKey(root), file)
	if err != nil {
		return nil, false
	}
	// uma conexão só (store.go): as linhas são lidas todas antes de Uses, que abre outra consulta.
	var names []string
	seen := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, false
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false
	}
	uses, ok := Uses(root, names, file)
	if !ok {
		return nil, false
	}
	out := make([]FileSymUses, 0, len(names))
	for _, n := range names {
		if u := uses[n]; u.Files > 0 {
			out = append(out, FileSymUses{Symbol: n, Uses: u})
		}
	}
	return out, true
}

// sortSites põe os arquivos que não são de teste na frente (o aviso lista até 5, e o que o modelo precisa editar
// primeiro não é teste), depois por caminho e linha. Ordem determinística: a mesma edição gera o mesmo aviso.
func sortSites(s []Site) {
	sort.Slice(s, func(a, b int) bool {
		ta, tb := isTestFile(s[a].File), isTestFile(s[b].File)
		if ta != tb {
			return !ta
		}
		if s[a].File != s[b].File {
			return s[a].File < s[b].File
		}
		return s[a].Line < s[b].Line
	})
}

// ambiguousDefs conta em quantos lugares distintos o nome é declarado: arquivos distintos com declaração chamável
// e, se for maior, quantos tipos o declaram (declaringTypes, homonyms.go). 0 ou 1 = sem ambiguidade.
func ambiguousDefs(d *sql.DB, root, name string) int {
	files := 0
	err := d.QueryRow(`SELECT COUNT(DISTINCT file_path) FROM code_symbols
		WHERE repo_root = ? AND name = ? AND kind IN (`+callableKindsSQL+`)`, repoKey(root), name).Scan(&files)
	if err != nil {
		return 0
	}
	if n := len(declaringTypes(d, root, name)); n > files {
		files = n
	}
	return files
}
