package graph

// answer.go: consultas estruturadas para respostas prontas. O texto de Definition/Callers continua sendo o que
// code_graph mostra; aqui o chamador (o grep do FastExec) pega os dados crus e monta a resposta no formato da
// pergunta (definição com corpo, chamadores agrupados por arquivo, lista de arquivos). Nenhuma destas funções
// sincroniza o índice: quem chama faz o Sync com teto de tempo antes (ver sync.go), porque só ele sabe se ainda
// dá tempo de responder ou se é melhor voltar à busca real.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Def é uma declaração indexada.
type Def struct {
	File       string
	Start, End int
	Kind, Sig  string
}

// Definitions lista as declarações com esse nome, em ordem de arquivo e linha. Mais de uma = homônimo: o nome
// existe em vários lugares e a resposta tem de dizer isso em vez de escolher uma.
func Definitions(root, symbol string) ([]Def, error) {
	d, err := store()
	if err != nil {
		return nil, err
	}
	name := lastName(symbol)
	rows, err := d.Query(`SELECT file_path, start_line, end_line, kind, signature FROM code_symbols
		WHERE repo_root = ? AND name = ? ORDER BY file_path, start_line`, repoKey(root), name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Def
	for rows.Next() {
		var x Def
		if err := rows.Scan(&x.File, &x.Start, &x.End, &x.Kind, &x.Sig); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// CallerFileCounts devolve, por arquivo, quantas referências ao nome o índice tem (chamadas e referências casadas
// pelo nome — o grafo não resolve tipos).
func CallerFileCounts(root, symbol string) (map[string]int, error) {
	d, err := store()
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT source_file, COUNT(*) FROM code_edges WHERE repo_root = ? AND target_symbol = ?
		AND relation_type IN ('calls','references') GROUP BY source_file`, repoKey(root), lastName(symbol))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var f string
		var n int
		if err := rows.Scan(&f, &n); err != nil {
			return nil, err
		}
		out[f] = n
	}
	return out, nil
}

// SymbolBody devolve as linhas start-end da declaração (no máximo maxLines; "" quando não couber ou não der para
// ler). É o corpo que o agente pediria com read_file logo depois do grep.
func SymbolBody(root string, def Def, maxLines int) string {
	if def.End < def.Start || def.End-def.Start+1 > maxLines {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(def.File)))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if def.Start < 1 || def.End > len(lines) {
		return ""
	}
	return strings.Join(lines[def.Start-1:def.End], "\n")
}

// TestFile diz se o caminho (relativo ao repositório, com '/') é arquivo de teste pela convenção do nome ou do
// diretório (transitive.go).
func TestFile(p string) bool { return isTestFile(p) }

// FindFiles acha arquivos do índice cujo caminho contém needle (busca por nome de arquivo: find/ls -R | grep).
func FindFiles(root, needle string, limit int) []string {
	d, err := store()
	if err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	like := "%" + strings.ReplaceAll(strings.ReplaceAll(needle, "%", `\%`), "_", `\_`) + "%"
	rows, err := d.Query(`SELECT file_path FROM code_files WHERE repo_root = ? AND file_path LIKE ? ESCAPE '\'
		ORDER BY file_path LIMIT ?`, repoKey(root), like, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			out = append(out, f)
		}
	}
	sortNonTestFirst(out)
	return out
}

// sortNonTestFirst põe os arquivos que não são de teste na frente (a resposta começa pelo que interessa).
func sortNonTestFirst(files []string) {
	sort.SliceStable(files, func(i, j int) bool {
		ti, tj := isTestFile(files[i]), isTestFile(files[j])
		if ti != tj {
			return !ti
		}
		return files[i] < files[j]
	})
}
