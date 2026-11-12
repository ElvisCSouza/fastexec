// enclosing.go: qual símbolo CONTÉM uma posição — arquivo, linha E coluna.
//
// O grafo sabe em que LINHA uma referência está (é o que code_edges guarda) e respondia "em que símbolo isso
// está" só com isso: o menor símbolo cujo intervalo de linhas cobre a linha. Em código de uma linha por membro
// funciona; em C# de uma linha por CLASSE não (`public class Dog { public string Speak() => "woof"; }`): todos os
// membros daquela linha têm o mesmo intervalo, o desempate caía na ordem de inserção do índice e a referência
// saía atribuída ao membro errado — inclusive à própria declaração que ela chama.
//
// Aqui a coluna entra na conta. O índice não guarda coluna, mas guarda o NOME e a LINHA da declaração: a coluna
// do nome sai do texto atual do arquivo (nameCol, o mesmo helper que defsites.go usa para ancorar no LSP). Com
// ela, um símbolo que COMEÇA na mesma linha da referência só a contém se o nome dele vier antes dela — o corpo
// vem depois do nome, sempre. Empate de intervalo (membros que dividem a linha) desempata pelo nome mais à
// direita, que é o mais interno.
//
// Sem coluna (Col=0: arquivo mudou desde a indexação, ou o nome não está no texto) a decisão volta a ser por
// linha, como era antes: pior resposta, nunca resposta inventada.

package graph

import (
	"database/sql"
	"strings"
)

// enclSym é um símbolo do índice com a coluna do nome resolvida no texto atual do arquivo.
type enclSym struct {
	name, sig, kind string
	start, end      int // intervalo de linhas, como no índice
	col             int // coluna UTF-16 (1-based) do nome na linha start; 0 = não encontrada
}

// enclosingIndex responde "que símbolo contém esta posição" para uma raiz, lendo cada arquivo uma vez.
type enclosingIndex struct {
	d      *sql.DB
	key    string
	src    *srcLines
	byFile map[string][]enclSym
}

func newEnclosingIndex(d *sql.DB, root string, src *srcLines) *enclosingIndex {
	if src == nil {
		src = &srcLines{root: root}
	}
	return &enclosingIndex{d: d, key: repoKey(root), src: src, byFile: map[string][]enclSym{}}
}

// file devolve os símbolos do arquivo na ordem de inserção do índice — a mesma que o `ORDER BY ... LIMIT 1`, que
// esta consulta substitui, usava como desempate final.
func (ix *enclosingIndex) file(f string) []enclSym {
	if s, ok := ix.byFile[f]; ok {
		return s
	}
	ix.byFile[f] = nil // consulta de outro arquivo no meio do laço não pode recarregar este
	var out []enclSym
	rows, err := ix.d.Query(`SELECT name, signature, kind, start_line, end_line FROM code_symbols
		WHERE repo_root = ? AND file_path = ? ORDER BY id`, ix.key, f)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var s enclSym
		if rows.Scan(&s.name, &s.sig, &s.kind, &s.start, &s.end) == nil {
			out = append(out, s)
		}
	}
	rows.Close()
	// o texto é lido depois de fechar as linhas: o pool tem UMA conexão e ler arquivo dentro do laço é o padrão
	// "coletar primeiro, resolver depois" (ver transitive.go)
	for i := range out {
		out[i].col = nameCol(ix.src.line(f, out[i].start), out[i].name)
	}
	ix.byFile[f] = out
	return out
}

// refCol devolve a coluna da referência: a que o índice guardou (source_col, preenchida pelo scanner de C#, que
// sabe onde o nome está) e, quando não há (índice antigo ou linguagem que não posiciona), a primeira ocorrência do
// nome no texto atual — pior posição, nunca posição inventada. Linha sem o nome (arquivo mudou desde a indexação)
// devolve 0 e a decisão volta a ser por linha.
func (ix *enclosingIndex) refCol(file string, line, col int, name string) int {
	if col > 0 {
		return col
	}
	return nameCol(ix.src.line(file, line), name)
}

// at devolve o símbolo mais interno que contém (line, col) — no formato "nome<US>assinatura" que callerLabel
// consome — e o tipo que contém a posição. Os dois vazios quando nenhum símbolo a contém.
func (ix *enclosingIndex) at(file string, line, col int) (inner, typ string) {
	syms := ix.file(file)
	best, bestType := -1, -1
	for i := range syms {
		s := &syms[i]
		if s.start > line || s.end < line {
			continue
		}
		// o corpo começa depois do nome: símbolo que começa nesta linha com o nome à direita da referência
		// ainda não a contém (é o caso `public void M() { }` na mesma linha da classe)
		if s.start == line && s.col > 0 && col > 0 && s.col > col {
			continue
		}
		if best < 0 || innerOf(s, &syms[best]) {
			best = i
		}
		if isTypeKind(s.kind) && (bestType < 0 || innerOf(s, &syms[bestType])) {
			bestType = i
		}
	}
	if best >= 0 {
		inner = syms[best].name + "\x1f" + syms[best].sig
	}
	if bestType >= 0 {
		typ = syms[bestType].name
	}
	return inner, typ
}

// innerOf diz se a é mais interno que b: menor intervalo ganha; empatados, o que começa mais tarde e, na mesma
// linha, o de nome mais à direita — o membro declarado por último na linha, que é o que está por dentro.
func innerOf(a, b *enclSym) bool {
	if sa, sb := a.end-a.start, b.end-b.start; sa != sb {
		return sa < sb
	}
	if a.start != b.start {
		return a.start > b.start
	}
	return a.col > b.col
}

// label devolve o rótulo do símbolo que contém a referência, no formato do grafo ("Tipo.método", o mesmo que a
// saída sempre mostrou); "" quando nenhum símbolo a contém.
func (ix *enclosingIndex) label(file string, line, col int) string {
	l, _ := ix.labelType(file, line, col)
	return l
}

// labelType é label mais o tipo que contém a posição: quem trata homônimos precisa dos dois (homonyms.go).
func (ix *enclosingIndex) labelType(file string, line, col int) (string, string) {
	inner, typ := ix.at(file, line, col)
	if inner == "" {
		return "", typ
	}
	return callLabel(inner, typ), typ
}

// callLabel fecha o rótulo: o nome do símbolo envolvente (callerLabel resolve o receptor de Go) e, quando ele não
// traz o próprio tipo, o tipo que contém a posição — nunca "Tipo.Tipo", que era o rótulo de quem envolvia a si mesmo.
func callLabel(inner, typ string) string {
	l := callerLabel(inner)
	if typ != "" && !strings.Contains(l, ".") {
		l = typ + "." + l
	}
	return l
}
