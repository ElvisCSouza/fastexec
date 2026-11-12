// defsites.go: onde o índice diz que um nome é DECLARADO, com linha E coluna.
//
// O grafo por nome sabe responder "quem usa X", e responde por nome — que é o palpite dele. Um servidor de
// linguagem responde a mesma pergunta por tipo, mas só aceita perguntas com POSIÇÃO: ele não conhece nomes,
// conhece arquivo:linha:coluna. Este arquivo é a ponte entre os dois: pega do índice a posição de cada
// declaração e entrega para quem vai perguntar ao servidor. Nada aqui fala LSP — é só a posição.
package graph

import (
	"sort"
	"strconv"
	"strings"
)

// LastName é o nome sem o qualificador de tipo: "Alpha.Do" -> "Do". É o que o índice guarda em
// code_symbols.name, e portanto o que se pode consultar.
func LastName(symbol string) string { return lastName(symbol) }

// DefSite é uma declaração no índice, com a posição exata do NOME dentro da linha da declaração.
//
// Col é a coluna UTF-16, 1-based (a mesma unidade que o LSP usa em positionEncoding=utf-16), do primeiro
// caractere do nome. Col=0 quando o nome não pôde ser localizado no texto atual do arquivo — arquivo
// mudou desde a indexação, ou a declaração é gerada. Quem for perguntar ao servidor deve tratar Col=0 como
// "não dá para ancorar aqui" e desistir, nunca chutar a coluna 1.
type DefSite struct {
	File string // relativo à raiz do repositório, barra normal (formato do índice)
	Line int    // 1-based, como todo o grafo
	Col  int    // 1-based, UTF-16
	Kind string // 'func', 'method', 'class', ...
	Sig  string // cabeçalho da declaração, como o índice guardou
	Type string // tipo que declara (classe/receiver); "" quando é função solta
}

// DefSites devolve as declarações indexadas de symbol, na ordem do arquivo.
//
// O qualificador "Tipo.nome" é honrado como em Callers/Definition: com um tipo no nome, só as declarações
// daquele tipo voltam. Sem qualificador voltam TODAS (homônimos inclusive) — e isso é proposital: ancorar
// em cada uma e unir as respostas do servidor dá, por construção, o conjunto exato de referências ao nome,
// que é exatamente o que o grafo tenta adivinhar por nome.
func DefSites(root, symbol string) []DefSite {
	name := lastName(symbol)
	if root == "" || name == "" {
		return nil
	}
	d, _, err := ready(root)
	if err != nil {
		return nil
	}
	key := repoKey(root)
	rows, err := d.Query(`SELECT s.file_path, s.start_line, s.kind, s.signature,
			COALESCE((SELECT t.name FROM code_symbols t
				WHERE t.repo_root = s.repo_root AND t.file_path = s.file_path AND t.kind IN (`+typeKindsSQL+`)
					AND t.start_line <= s.start_line AND t.end_line >= s.end_line AND t.id <> s.id
				ORDER BY t.end_line - t.start_line LIMIT 1), '')
		FROM code_symbols s
		WHERE s.repo_root = ? AND s.name = ?
		ORDER BY s.file_path, s.start_line`, key, name)
	if err != nil {
		return nil
	}
	var out []DefSite
	for rows.Next() {
		var s DefSite
		if rows.Scan(&s.File, &s.Line, &s.Kind, &s.Sig, &s.Type) == nil {
			out = append(out, s)
		}
	}
	rows.Close()

	// O tipo que declara um método nem sempre sai da consulta de "tipo que envolve a linha": em Go o método
	// não está aninhado no tipo (nada envolve `func (a Alpha) Do()`), o receptor está no cabeçalho da
	// assinatura. Sem ler de lá, "Alpha.Do" não se distingue de "Do" e o servidor seria ancorado na
	// declaração errada — devolvendo a definição do homônimo.
	for i := range out {
		if out[i].Type == "" && out[i].Kind == "method" {
			if m := goReceiverRe.FindStringSubmatch(out[i].Sig); m != nil {
				out[i].Type = m[1]
			}
		}
	}

	// O qualificador é resolvido DEPOIS de fechar as linhas: declaringTypes abre outra consulta, e no
	// SQLite uma consulta nova enquanto outra ainda itera é pedir para embaralhar.
	cands := declaringTypes(d, root, name)
	for _, s := range out {
		if s.Type != "" && !containsFold(cands, s.Type) {
			cands = append(cands, s.Type)
		}
	}
	if want := qualifierType(symbol, cands); want != "" {
		var kept []DefSite
		for _, s := range out {
			if strings.EqualFold(s.Type, want) {
				kept = append(kept, s)
			}
		}
		out = kept
	}
	if len(out) == 0 {
		return nil
	}
	src := &srcLines{root: root}
	for i := range out {
		out[i].Col = nameCol(src.line(out[i].File, out[i].Line), name)
	}
	return out
}

// SymbolFiles devolve os arquivos do índice que DECLARAM name (sem diferenciar maiúsculas), como caminhos relativos
// à raiz. Não atualiza o índice: quem chama é uma sugestão para um caminho que não existe — um read_file que já
// falhou não pode pagar por um Refresh. Índice velho só pode sugerir arquivo a mais, e quem chama confere no disco.
func SymbolFiles(root, name string, max int) []string {
	if root == "" || name == "" || max <= 0 || !exists() {
		return nil
	}
	d, err := store()
	if err != nil {
		return nil
	}
	rows, err := d.Query(`SELECT DISTINCT file_path FROM code_symbols
		WHERE repo_root = ? AND name = ? COLLATE NOCASE ORDER BY file_path LIMIT ?`, repoKey(root), name, max)
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
	return out
}

// RefInfo é o rótulo de uma referência, no mesmo vocabulário que o grafo já usa na saída.
type RefInfo struct {
	Label    string // símbolo que contém a referência, no formato do grafo ("Alpha.Do"); "" se o índice não sabe
	Relation string // relação registrada nessa linha ("calls", "references", "renders", ...); "" se não há aresta
}

// AnnotateRefs rotula referências (arquivo relativo à raiz + linha 1-based) com o símbolo que as contém e a
// relação que o índice registrou.
//
// É o que permite ao servidor de linguagem responder no MESMO formato que o grafo: quem decide o CONJUNTO
// de referências é o servidor (é o ponto todo), mas o texto de cada linha — "em que função isso está, é
// chamada ou leitura" — já está no índice e sai daqui sem custo nenhum por linha. Serve também para o caso
// em que o índice e o servidor discordam: o rótulo vem do índice, a linha vem do servidor, e a diferença
// aparece em vez de sumir.
func AnnotateRefs(root, name string, refs []Site) []RefInfo {
	out := make([]RefInfo, len(refs))
	if root == "" || len(refs) == 0 {
		return out
	}
	d, _, err := ready(root)
	if err != nil {
		return out
	}
	key := repoKey(root)
	for i, r := range refs {
		var inner, typ, rel string
		err := d.QueryRow(`SELECT COALESCE((SELECT s.name || char(31) || s.signature FROM code_symbols s
				WHERE s.repo_root = ? AND s.file_path = ? AND s.start_line <= ? AND s.end_line >= ?
				ORDER BY s.end_line - s.start_line LIMIT 1), ''),
			COALESCE((SELECT t.name FROM code_symbols t
				WHERE t.repo_root = ? AND t.file_path = ? AND t.kind IN (`+typeKindsSQL+`)
					AND t.start_line <= ? AND t.end_line >= ?
				ORDER BY t.end_line - t.start_line LIMIT 1), ''),
			COALESCE((SELECT e.relation_type FROM code_edges e
				WHERE e.repo_root = ? AND e.source_file = ? AND e.source_line = ?
					AND e.target_symbol IN (?, ?) LIMIT 1), '')`,
			key, r.File, r.Line, r.Line,
			key, r.File, r.Line, r.Line,
			key, r.File, r.Line, name, lastName(name)).Scan(&inner, &typ, &rel)
		if err != nil {
			continue
		}
		out[i].Relation = rel
		if inner == "" {
			continue
		}
		out[i].Label = callerLabel(inner)
		// O índice guarda o receptor no cabeçalho da assinatura (Go); nas linguagens em que ele não
		// está lá, o tipo que envolve a linha é o mesmo dado por outro caminho.
		if typ != "" && !strings.Contains(out[i].Label, ".") {
			out[i].Label = typ + "." + out[i].Label
		}
	}
	return out
}

// SortSites ordena referências como o grafo sempre ordenou: arquivos de teste no fim, depois por arquivo e
// linha. Serve para o resultado exato sair na mesma ordem do resultado por nome, e a troca de um pelo outro
// não mudar a leitura de quem já conhecia a saída antiga.
func SortSites(sites []Site) {
	sort.SliceStable(sites, func(i, j int) bool {
		ti, tj := IsTestPath(sites[i].File), IsTestPath(sites[j].File)
		if ti != tj {
			return !ti
		}
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
}

// containsFold diz se a lista já tem o nome, ignorando caixa (é como qualifierType compara).
func containsFold(list []string, s string) bool {
	for _, e := range list {
		if strings.EqualFold(e, s) {
			return true
		}
	}
	return false
}

// SiteKey é a chave de deduplicação de uma referência ("arquivo:linha"), a mesma em todo o pacote.
func SiteKey(s Site) string { return s.File + ":" + strconv.Itoa(s.Line) }

// NameCol é NameCol para quem já tem o texto da linha em mãos e não quer passar pelo índice — o caso do
// arquivo que acabou de ser editado, cujo conteúdo novo ainda não foi indexado.
func NameCol(line, name string) int { return nameCol(line, name) }

// nameCol devolve a coluna UTF-16 (1-based) onde name aparece na linha, ou 0 quando não aparece.
//
// A primeira ocorrência com fronteira de palavra é a certa: em uma declaração o nome vem antes do corpo,
// então mesmo em uma linha só ("function Do() { return Do }") a primeira é a da declaração.
func nameCol(line, name string) int {
	if line == "" || name == "" || len(line) < len(name) {
		return 0
	}
	for from := 0; from <= len(line)-len(name); {
		i := strings.Index(line[from:], name)
		if i < 0 {
			return 0
		}
		at := from + i
		if wordEdge(line, at-1) && wordEdge(line, at+len(name)) {
			return utf16Col(line[:at])
		}
		from = at + 1
	}
	return 0
}

// wordEdge diz se a posição i é uma borda de palavra em s (fora dos limites da string também é borda).
// Byte >= 0x80 conta como letra: erra só para pontuação não-ASCII, e o custo de errar aqui é uma coluna
// que não ancora — nunca uma coluna errada usada em silêncio.
func wordEdge(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	switch {
	case c == '_' || c == '$':
		return false
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= 0x80:
		return false
	}
	return true
}

// utf16Col conta a coluna UTF-16 (1-based) do fim do prefixo: o LSP mede em unidades de 16 bits, então um
// emoji antes do nome ocupa duas e a coluna em bytes daria errado.
func utf16Col(prefix string) int {
	col := 1
	for _, r := range prefix {
		if r > 0xFFFF {
			col += 2
		} else {
			col++
		}
	}
	return col
}
