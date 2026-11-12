// edit_impact.go: quando a edição muda a ASSINATURA de um símbolo declarado (ou o remove/renomeia), a resposta diz
// quais outros arquivos usam aquele nome e podem precisar de ajuste. Sem isso o modelo gasta um turno inteiro
// descobrindo "quem mais chama isso" (bench ciclo3: M2 e M1) — exatamente o caso em que o FastExec perdia para a
// ferramenta nativa do editor (ciclo2: M4 +87%, M6 +33%).
//
// Só a lista de chamadores é entregue; quem edita os outros arquivos continua sendo o modelo. Nada é escrito em
// disco e nada é reindexado aqui: graph.Uses lê o índice como está (o Touch da gravação é debounced, então o nome
// antigo ainda está indexado com os sites de antes da edição — que é o que interessa).
//
// Dispara apenas em mudança de assinatura ou remoção/rename. Edição de corpo, comentário, formatação ou símbolo
// novo não muda um byte da resposta.
package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
)

const (
	// editImpactMaxWarn é o teto de avisos por chamada (uma edição em lote pode mexer em vários símbolos).
	editImpactMaxWarn = 3
	// editImpactMaxSites é quantos "arquivo:linha" a linha cita antes de resumir em "+K more".
	editImpactMaxSites = 5
	// editImpactBudget é o teto da linha, em caracteres: é um rodapé, não um relatório.
	editImpactBudget = 350
	// editImpactTimeout é o orçamento de tempo do bloco inteiro (parse dos dois textos + consulta); estourou, o
	// aviso é omitido. A edição em si nunca espera pelo grafo.
	editImpactTimeout = 150 * time.Millisecond
	// editImpactMaxNames limita o IN da consulta quando o lote mexe em muitos símbolos.
	editImpactMaxNames = 20
)

// impactSymbol é uma declaração que mudou: nome e se desapareceu (removida ou renomeada).
type impactSymbol struct {
	name    string
	removed bool
}

// editImpact devolve o rodapé "[graph] ..." da resposta de edit_file/write_file, ou "" quando não há o que avisar.
// oldText e newText são o conteúdo DECODIFICADO antes e depois da edição (o mesmo que o parser do grafo lê).
func editImpact(host, path, oldText, newText string) string {
	if oldText == "" || newText == "" || oldText == newText {
		return ""
	}
	if !impactLocal(host, path) {
		return ""
	}
	start := time.Now()

	oldDecls, _, ok := graph.ParseFile(path, []byte(oldText))
	if !ok {
		return "" // linguagem sem declarações parseáveis (json, md...) ou Go ilegível: não dispara
	}
	newDecls, _, ok := graph.ParseFile(path, []byte(newText))
	if !ok {
		return "" // o arquivo ficou ilegível? então não há como comparar assinatura
	}
	syms := changedDecls(oldDecls, newDecls)
	if len(syms) == 0 || time.Since(start) > editImpactTimeout {
		return ""
	}

	root, ok := graph.ResolveRepoRoot(path)
	if !ok {
		return ""
	}
	names := make([]string, 0, len(syms))
	for _, s := range syms {
		names = append(names, s.name)
	}
	if len(names) > editImpactMaxNames {
		names = names[:editImpactMaxNames]
	}
	uses, ok := graph.Uses(root, names, relToRoot(root, path))
	if !ok || time.Since(start) > editImpactTimeout {
		return ""
	}

	var lines []string
	for _, s := range syms {
		if len(lines) == editImpactMaxWarn {
			break
		}
		if l := impactLine(s, uses[s.name]); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// impactLocal: só o host local tem o repositório indexado deste lado. Em host remoto (ou path Windows em host
// Linux) nada é consultado.
func impactLocal(host, path string) bool {
	t, err := ResolveTargetHost(host, path)
	return err == nil && t.IsLocal
}

// relToRoot põe o caminho no formato do índice (relativo à raiz do repositório, barra normal) — é o que o próprio
// Touch grava, e é a chave para não listar o arquivo editado entre os que "precisam de ajuste".
func relToRoot(root, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// changedDecls compara as declarações por nome: assinatura (Sig, que no grafo é só o cabeçalho — o corpo fica de
// fora) ou kind diferente = mudou; sumiu = removida ou renomeada. Símbolo novo não entra. A ordem é a do arquivo
// antigo, então a mesma edição gera sempre a mesma resposta.
func changedDecls(old, neu []codescan.Decl) []impactSymbol {
	byName := make(map[string]codescan.Decl, len(neu))
	for _, d := range neu {
		byName[d.Name] = d
	}
	var out []impactSymbol
	for _, d := range old {
		n, ok := byName[d.Name]
		switch {
		case !ok:
			out = append(out, impactSymbol{name: d.Name, removed: true})
		case n.Sig != d.Sig || n.Kind != d.Kind:
			out = append(out, impactSymbol{name: d.Name})
		}
	}
	return out
}

// impactLine monta a linha de um símbolo. Vazia quando não há uso nenhum em outro arquivo (nada a avisar) — e o
// nome ambíguo, declarado em mais de um lugar, não vira lista: o grafo casa por nome, então a lista poderia ser de
// outro símbolo. Nesse caso o aviso aponta para code_graph em vez de afirmar.
func impactLine(s impactSymbol, u graph.SymUses) string {
	if u.Files == 0 {
		return ""
	}
	what := "signature changed"
	if s.removed {
		what = "removed/renamed"
	}
	head := fmt.Sprintf("[graph] %s %s:", s.name, what)
	if u.Ambiguous >= 2 {
		return fmt.Sprintf("%s name is ambiguous (%d definitions): check callers with code_graph.", head, u.Ambiguous)
	}

	tests := ""
	if u.TestFiles > 0 {
		tests = fmt.Sprintf(" (tests: %d)", u.TestFiles)
	}
	// encolhe a lista até caber no orçamento; abaixo de uma citação sobra só a contagem.
	for shown := min(len(u.Sites), editImpactMaxSites); shown >= 1; shown-- {
		line := fmt.Sprintf("%s %d use(s) in %d file(s) may need update: %s%s. By name, not type-checked.",
			head, len(u.Sites), u.Files, joinSites(u.Sites[:shown]), more(u.Sites, shown)+tests)
		if len(line) <= editImpactBudget {
			return line
		}
	}
	return fmt.Sprintf("%s %d use(s) in %d file(s) may need update%s. By name, not type-checked.",
		head, len(u.Sites), u.Files, tests)
}

// joinSites formata os locais citados ("a.go:12, b.go:40").
func joinSites(sites []graph.Site) string {
	parts := make([]string, 0, len(sites))
	for _, s := range sites {
		parts = append(parts, fmt.Sprintf("%s:%d", s.File, s.Line))
	}
	return strings.Join(parts, ", ")
}

// more resume os locais que ficaram de fora.
func more(sites []graph.Site, shown int) string {
	if n := len(sites) - shown; n > 0 {
		return fmt.Sprintf(", +%d more", n)
	}
	return ""
}
