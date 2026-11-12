// lsp_def.go: o que o servidor sabe de um símbolo ALÉM de onde ele está.
//
// Um `def` responde "onde isso é declarado". O que falta ali é justamente o que faz o modelo abrir mais dois
// arquivos: QUAL é a assinatura resolvida (o tipo concreto por trás de um genérico), QUEM implementa aquela
// interface, de QUEM este tipo herda, e O QUE um tipo tem dentro. Nada disso é dedutível do texto — um
// `implements` de C# pode estar a três arquivos de distância, e em Go ele nem existe escrito: o tipo
// satisfaz a interface por estrutura. O servidor sabe; o índice, não.
//
// Toda linha daqui tem a mesma forma das outras respostas exatas: o SERVIDOR aponta o alvo, o ÍNDICE diz o
// nome do arquivo, e o que não veio de um dos dois não sai. Sem capacidade, sem resposta dentro do
// orçamento, ou servidor que devolve null: a resposta é a de antes, byte a byte.
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

const (
	// relMaxNames é o teto de alvos por linha de relação. Uma interface com trinta implementações responde
	// "quem" com os oito primeiros (na ordem do servidor); o resto é contagem, não lista.
	relMaxNames = 8
	// relMaxMembers é o teto de membros listados de um tipo. Um tipo gerado com duzentos membros não vira
	// duzentas linhas: o que o modelo pediu foi descobrir o que o tipo TEM.
	relMaxMembers = 24
	// relMaxNameLen corta um nome absurdamente qualificado (genérico aninhado). O corte é visível ("…") para
	// nunca passar por um nome completo.
	relMaxNameLen = 120
	// relBudget é o orçamento de UMA consulta de relações. Ela complementa a resposta, não a substitui: se o
	// servidor estiver ocupado, o def sai como saía antes.
	relBudget = 3 * time.Second
)

// LSP SymbolKind, os que decidem alguma coisa aqui: o que é um TIPO (membros e relações valem a pena) e o
// que é um MEMBRO (a relação é "de quem ele é implementação").
const (
	skClass       = 5
	skMethod      = 6
	skProperty    = 7
	skField       = 8
	skConstructor = 9
	skEnum        = 10
	skInterface   = 11
	skFunction    = 12
	skVariable    = 13
	skConstant    = 14
	skStruct      = 23
	skEvent       = 24
	skOperator    = 25
	skTypeParam   = 26
)

// kindWord é o nome do que o símbolo é. Vem do servidor (SymbolKind), não de heurística sobre o texto.
func kindWord(k int) string {
	switch k {
	case skClass:
		return "class"
	case skMethod:
		return "method"
	case skProperty:
		return "property"
	case skField:
		return "field"
	case skConstructor:
		return "constructor"
	case skEnum:
		return "enum"
	case skInterface:
		return "interface"
	case skFunction:
		return "function"
	case skVariable:
		return "variable"
	case skConstant:
		return "constant"
	case skStruct:
		return "struct"
	case skEvent:
		return "event"
	case skOperator:
		return "operator"
	case skTypeParam:
		return "typeparam"
	}
	return ""
}

// isTypeKind diz se o símbolo é um TIPO: é o que separa "liste os membros" de "liste os usos".
func isTypeKind(k int) bool {
	switch k {
	case skClass, skInterface, skStruct, skEnum:
		return true
	}
	return false
}

// isMemberKind: membro de um tipo. A relação útil dele é de quem ele é a implementação — em C# isso é o
// override e a base, em Go é o método da interface que ele satisfaz, e o protocolo responde os dois pela
// mesma pergunta.
func isMemberKind(k int) bool {
	switch k {
	case skMethod, skFunction, skProperty, skConstructor, skOperator, skField:
		return true
	}
	return false
}

// lspKindOf traduz o kind do índice para o vocabulário do protocolo. É o último recurso: vale quando o
// servidor não devolve árvore de símbolos para o arquivo, e mesmo aí é o que o índice leu do texto.
func lspKindOf(indexKind string) int {
	switch indexKind {
	case "method":
		return skMethod
	case "func", "function", "delegate":
		return skFunction
	case "field":
		return skField
	case "property":
		return skProperty
	case "class", "record", "object":
		return skClass
	case "struct", "union":
		return skStruct
	case "interface", "trait":
		return skInterface
	case "enum":
		return skEnum
	case "constructor":
		return skConstructor
	case "constant":
		return skConstant
	case "variable":
		return skVariable
	}
	return 0
}

// relTarget é um alvo de relação: o que o servidor apontou, com o nome e o que ele é quando deu para saber.
type relTarget struct {
	name string
	kind int
	file string // relativo à raiz, formato do índice
	line int    // 1-based
}

func (t relTarget) String() string {
	name := t.name
	if len(name) > relMaxNameLen {
		name = name[:relMaxNameLen] + "…"
	}
	if name == "" {
		// Sem nome, arquivo:linha ainda é a resposta do servidor — e é verdade.
		return fmt.Sprintf("%s:%d", t.file, t.line)
	}
	return fmt.Sprintf("%s (%s:%d)", name, t.file, t.line)
}

// relProbe é o estado de uma consulta de relações. Um alvo pode morar em outro arquivo (a interface, a base
// de um genérico), e a árvore de símbolos de cada arquivo é pedida uma vez só.
type relProbe struct {
	root string
	tree map[string][]lsp.DocumentSymbol
}

func newRelProbe(root string) *relProbe {
	return &relProbe{root: root, tree: map[string][]lsp.DocumentSymbol{}}
}

func (p *relProbe) treeOf(ctx context.Context, c *lsp.Client, abs string) []lsp.DocumentSymbol {
	if t, ok := p.tree[abs]; ok {
		return t
	}
	t, _ := c.DocumentSymbolsTree(ctx, abs)
	p.tree[abs] = t
	return t
}

// targetsFromLocs nomeia as localizações que o servidor devolveu. O `implementation` responde posição, não
// nome: quem diz o nome é a árvore de símbolos do arquivo apontado, e por isso ela é pedida aqui.
func (p *relProbe) targetsFromLocs(ctx context.Context, c *lsp.Client, locs []lsp.Location) []relTarget {
	var out []relTarget
	seen := map[string]bool{}
	for _, l := range locs {
		site, ok := relSite(p.root, l)
		if !ok {
			continue // fora do repositório: a resposta exata é sobre este repositório
		}
		t := relTarget{file: site.File, line: site.Line}
		if abs := lsp.URItoPath(l.URI); abs != "" {
			if sym, parent, found := symAt(p.treeOf(ctx, c, abs), l.Range.Start); found {
				t.name, t.kind = qualName(sym, parent), sym.Kind
			}
		}
		key := fmt.Sprintf("%s\x00%s", t.name, graph.SiteKey(graph.Site{File: t.file, Line: t.line}))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

// implTargets pergunta quem implementa o símbolo (interface), o que ele implementa (tipo concreto) ou de
// que membro ele é a implementação (método). É a MESMA pergunta do protocolo; a direção sai do que o
// símbolo é, não de um palpite sobre a resposta.
func (p *relProbe) implTargets(ctx context.Context, c *lsp.Client, abs string, pos lsp.Position) []relTarget {
	locs, err := c.Implementation(ctx, abs, pos)
	if err != nil {
		return nil
	}
	return p.targetsFromLocs(ctx, c, locs)
}

// superTargets: o que o servidor diz que aquele tipo estende ou implementa. Aqui o item já vem nomeado.
func (p *relProbe) superTargets(ctx context.Context, c *lsp.Client, abs string, pos lsp.Position) []relTarget {
	items, err := c.Supertypes(ctx, abs, pos)
	if err != nil {
		return nil
	}
	var out []relTarget
	for _, it := range items {
		if it.URI == "" {
			continue
		}
		site, ok := relSite(p.root, lsp.Location{URI: it.URI, Range: it.Range})
		if !ok {
			continue
		}
		out = append(out, relTarget{name: it.Name, kind: it.Kind, file: site.File, line: site.Line})
	}
	return out
}

// relLine monta a linha: um rótulo e os alvos, na ordem do servidor. Lista vazia não vira linha nenhuma.
func relLine(label string, targets []relTarget) string {
	if len(targets) == 0 {
		return ""
	}
	parts := make([]string, 0, len(targets))
	for i, t := range targets {
		if i >= relMaxNames {
			parts = append(parts, fmt.Sprintf("... +%d", len(targets)-i))
			break
		}
		parts = append(parts, t.String())
	}
	return label + ": " + strings.Join(parts, ", ") + "\n"
}

// byKindLines separa os alvos pelo que o servidor diz que ELES são: um tipo de interface é um contrato que o
// símbolo cumpre, qualquer outro é um tipo concreto. Sem essa separação, "implements" e "inherits" sairiam
// trocados — e uma relação trocada é pior que nenhuma.
func byKindLines(ifaceLabel, otherLabel string, targets []relTarget) []string {
	var ifaces, others []relTarget
	for _, t := range targets {
		if t.kind == skInterface {
			ifaces = append(ifaces, t)
		} else {
			others = append(others, t)
		}
	}
	var out []string
	for _, l := range []string{relLine(ifaceLabel, ifaces), relLine(otherLabel, others)} {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// factsForDef resolve as relações de uma declaração, na direção que interessa a quem PERGUNTOU por ela: em
// uma interface, quem a implementa; em um tipo, o que ele implementa e o que ele estende; em um membro, de
// que membro de interface ele é a implementação.
func factsForDef(ctx context.Context, p *relProbe, c *lsp.Client, abs string, pos lsp.Position, kind int) []string {
	ctx, cancel := context.WithTimeout(ctx, relBudget)
	defer cancel()
	switch {
	case kind == skInterface:
		out := []string{relLine("implemented by", p.implTargets(ctx, c, abs, pos))}
		return append(out, byKindLines("extends", "inherits", p.superTargets(ctx, c, abs, pos))...)
	case isTypeKind(kind):
		out := byKindLines("implements", "derived", p.implTargets(ctx, c, abs, pos))
		return append(out, byKindLines("implements", "inherits", p.superTargets(ctx, c, abs, pos))...)
	case isMemberKind(kind):
		return []string{relLine("implements", p.implTargets(ctx, c, abs, pos))}
	}
	return nil
}

// factsForUses é a versão para callers/impact: só a relação que muda a LEITURA da lista de usos. Quem mexe
// em uma interface precisa saber quem a implementa (a lista de referências não mostra isso: em Go não há
// referência nenhuma ao nome da interface no tipo que a satisfaz), e quem mexe em um método precisa saber de
// que interface/base ele é.
func factsForUses(ctx context.Context, p *relProbe, c *lsp.Client, abs string, pos lsp.Position, kind int) []string {
	ctx, cancel := context.WithTimeout(ctx, relBudget)
	defer cancel()
	switch {
	case kind == skInterface:
		return []string{relLine("implemented by", p.implTargets(ctx, c, abs, pos))}
	case isMemberKind(kind):
		return []string{relLine("implements", p.implTargets(ctx, c, abs, pos))}
	}
	return nil
}

// defFacts é o que fecha um `def`: as relações da declaração e, quando ela é um TIPO, os membros.
func defFacts(ctx context.Context, p *relProbe, c *lsp.Client, abs string, d graph.DefSite) []string {
	if d.Col <= 0 {
		return nil // sem coluna não há âncora (ver stepExact): perguntar seria perguntar por outro símbolo
	}
	pos := lsp.Position{Line: d.Line - 1, Character: d.Col - 1}
	sym, _, found := symAt(p.treeOf(ctx, c, abs), pos)
	kind := lspKindOf(d.Kind)
	if found {
		kind = sym.Kind // o servidor decidiu, e ele viu o arquivo de verdade
	}
	out := factsForDef(ctx, p, c, abs, pos, kind)
	if found && isTypeKind(sym.Kind) {
		out = append(out, memberLines(sym)...)
	}
	// Em Go a hierarquia de tipos reporta as interfaces IMPLEMENTADAS como supertipos, então a mesma relação
	// chega pelas duas perguntas ("o que ele implementa" e "de quem ele herda") e sairia duas vezes. A linha
	// repetida não é informação nova: repete o mesmo arquivo e a mesma linha.
	return dedupeLines(out)
}

// usesFacts é o que acompanha callers/impact.
func usesFacts(ctx context.Context, p *relProbe, c *lsp.Client, abs string, d graph.DefSite) []string {
	if d.Col <= 0 {
		return nil
	}
	pos := lsp.Position{Line: d.Line - 1, Character: d.Col - 1}
	kind := lspKindOf(d.Kind)
	if sym, _, found := symAt(p.treeOf(ctx, c, abs), pos); found {
		kind = sym.Kind
	}
	return factsForUses(ctx, p, c, abs, pos, kind)
}

// defSignature devolve a assinatura da declaração como o SERVIDOR a resolve — é por aqui que aparece o tipo
// concreto de um genérico, ou o tipo de retorno que o índice nem sempre guarda.
//
// Cai para o cabeçalho do índice quando o servidor não responde ou devolve um corpo em vez de uma
// assinatura: para um tipo, o hover traz o struct inteiro ("type Order struct { … }"), e aí o cabeçalho do
// índice é a resposta certa. Comentário de rodapé do servidor ("// size=16, offset=0") também sai: é
// diagnóstico dele, não assinatura.
func defSignature(ctx context.Context, c *lsp.Client, abs string, pos lsp.Position, fallback string) string {
	h, err := c.HoverAt(ctx, abs, pos)
	if err != nil || h == nil {
		return fallback
	}
	text := strings.TrimSpace(stripFence(h.Contents))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if i := strings.Index(text, " //"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if text == "" || strings.ContainsAny(text, "{}\r") {
		return fallback
	}
	return text
}

// defEndLine devolve a última linha (1-based) do símbolo que a âncora declara, segundo a árvore de símbolos
// do servidor — a mesma árvore que defFacts já busca, então não custa requisição nenhuma.
//
// É o que faz um `def` responder "344-526" em vez de "344-344": a pergunta é ONDE isso é declarado, e um
// método de 180 linhas é declarado nas 180. Sem a árvore (servidor que não responde documentSymbol) o fim
// fica com quem chamou, que é a linha da âncora — o que já se dizia antes.
func defEndLine(tree []lsp.DocumentSymbol, pos lsp.Position) int {
	sym, _, found := symAt(tree, pos)
	if !found {
		return 0
	}
	return sym.Range.End.Line + 1
}

// memberLines lista o conteúdo de um TIPO, um membro por linha e sem corpo: é o que o modelo pediria abrindo
// o arquivo do tipo. A linha é o que o servidor diz (nome, o que é, e o tipo que ele declarou) mais a linha
// onde ele está — que é o que permite ler só aquele membro.
func memberLines(sym lsp.DocumentSymbol) []string {
	if len(sym.Children) == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("members (%d):\n", len(sym.Children))}
	for i, ch := range sym.Children {
		if i >= relMaxMembers {
			out = append(out, fmt.Sprintf("... +%d\n", len(sym.Children)-i))
			break
		}
		out = append(out, "- "+memberLine(ch)+"\n")
	}
	return out
}

func memberLine(ch lsp.DocumentSymbol) string {
	line := fmt.Sprintf("%d: %s", ch.Range.Start.Line+1, strings.TrimSpace(ch.Name))
	if w := kindWord(ch.Kind); w != "" {
		line += " (" + w + ")"
	}
	if d := strings.TrimSpace(ch.Detail); d != "" {
		line += " " + d
	}
	return line
}

// symAt acha o símbolo que cobre a posição, junto do pai que o declara (o tipo, quando o alvo é um membro).
// O mais interno vence, e um nó cujo selectionRange (o NOME) contém a posição vence um que só cobre pela
// faixa inteira: o alvo é o membro, não o tipo que o contém.
func symAt(tree []lsp.DocumentSymbol, pos lsp.Position) (lsp.DocumentSymbol, lsp.DocumentSymbol, bool) {
	var best, parent lsp.DocumentSymbol
	bestScore, found := 0, false
	var walk func(nodes []lsp.DocumentSymbol, up lsp.DocumentSymbol)
	walk = func(nodes []lsp.DocumentSymbol, up lsp.DocumentSymbol) {
		for _, n := range nodes {
			sel := inRange(n.Selection, pos)
			if sel || inRange(n.Range, pos) {
				score := -spanOf(n.Range)
				if sel {
					score += 1 << 30
				}
				if !found || score > bestScore {
					best, parent, bestScore, found = n, up, score, true
				}
			}
			walk(n.Children, n)
		}
	}
	walk(tree, lsp.DocumentSymbol{})
	return best, parent, found
}

// spanOf mede a faixa em "caracteres com a linha pesando mais": só serve para ordenar qual nó é mais
// interno, e para isso a linha domina.
func spanOf(r lsp.Range) int {
	lines := r.End.Line - r.Start.Line
	chars := r.End.Character - r.Start.Character
	if lines == 0 && chars < 0 {
		chars = 0
	}
	return lines*1<<20 + chars
}

// inRange: a posição está na faixa. O fim é exclusivo, como o protocolo define; uma faixa vazia (o servidor
// que devolve só o ponto) casa pelo próprio ponto.
func inRange(r lsp.Range, pos lsp.Position) bool {
	if r.Start == r.End {
		return pos == r.Start
	}
	return !positionLess(pos, r.Start) && positionLess(pos, r.End)
}

func positionLess(a, b lsp.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

// qualName monta o nome do alvo. O servidor já nomeia o que é declarado por si ("(Order).Sum" em Go); quando
// o alvo é um membro listado sob o tipo, o nome vem sozinho ("Total") e é o pai que diz de quem ele é.
func qualName(sym, parent lsp.DocumentSymbol) string {
	name := strings.TrimSpace(sym.Name)
	if name == "" || parent.Name == "" || strings.ContainsAny(name, ".( ") {
		return name
	}
	return parent.Name + "." + name
}

// dedupeLines mantém a ordem e tira repetição: homônimos ancorados em arquivos diferentes respondem a mesma
// relação, e ela é uma linha só.
func dedupeLines(lines []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}
