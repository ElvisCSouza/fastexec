// lsp_types.go: a linha "types:" que acompanha um trecho lido.
//
// O que o Visual Studio dá de graça e o modelo não tem: o TIPO de uma variável declarada sem tipo escrito
// (`var x = ...`, `x := ...`, `const y = ...`). Sem isso, saber o tipo de `pedidos` custa abrir outro
// arquivo — que é exatamente o custo que esta linha existe para evitar.
//
// A regra é a mesma do resto da camada exata: o FastExec acha ONDE olhar (o texto do trecho, que ele já
// tem), o SERVIDOR diz O QUE É (inlay hint; hover quando o servidor não tem hints), e nada é inventado.
// Servidor frio, sem hints, ou sem resposta dentro do orçamento: a linha não sai e a resposta é
// byte a byte a de antes. Nada aqui inicia servidor: ler um arquivo não pode custar um gopls frio.
package tools

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/lsp"
)

const (
	// typeLineMaxNames é o teto de nomes na linha. Um trecho com vinte `:=` não vira um despejo: os
	// primeiros oito (na ordem do texto) já respondem a pergunta que fez o modelo ler o trecho.
	typeLineMaxNames = 8
	// typeLineMaxBytes é o teto do tamanho da linha montada (o resto da resposta é o trecho lido).
	typeLineMaxBytes = 400
	// typeLineBudget é o orçamento inteiro da linha, servidor QUENTE incluído: passou disso, sai sem ela.
	typeLineBudget = time.Second
	// outlineTypeMaxSpan limita quantas linhas de CADA declaração do outline são varridas atrás de tipos
	// inferidos. O outline pode mostrar uma declaração de mil linhas; o começo dela responde a pergunta.
	outlineTypeMaxSpan = 300
)

// outlineTypeLine é a linha "types:" que fecha um code_outline. O outline mostra só os cabeçalhos, então o
// trecho que o modelo tem em mãos é o corpo das declarações mostradas — é ali que um `x := ...` sem tipo
// escrito custa uma leitura extra para ser entendido.
func outlineTypeLine(path string, src []byte, decls []codescan.Decl, ends map[int]int, max int) string {
	if len(decls) > max {
		decls = decls[:max] // só o que foi renderizado conta: anotar o que não saiu seria mentir sobre a tela
	}
	var lines []int
	for _, d := range decls {
		end := d.Line
		if e, ok := ends[d.Line]; ok && e > end {
			end = e
		}
		if end-d.Line > outlineTypeMaxSpan {
			end = d.Line + outlineTypeMaxSpan
		}
		lines = append(lines, spanLines(d.Line, end)...)
	}
	return typeLine(path, src, lines)
}

// typeLineForRead é a linha "types:" de um read_file de faixa. Só a leitura por start_line/end_line entra:
// os outros modos (pattern, symbol, offset, ranges) mostram um recorte que este código não sabe reconstruir,
// e anotar um trecho que o modelo não está vendo é pior que não anotar nada.
//
// O texto de origem é lido do disco aqui, e não recebido de quem chamou: um arquivo não-UTF-8 sai decodificado
// para o modelo, e o servidor conta posições no arquivo como ele está.
func typeLineForRead(args ReadFileArgs, out string) string {
	if args.Path == "" || args.Pattern != "" || args.Ranges != "" || args.Symbol != "" ||
		args.Offset > 0 || args.Limit > 0 {
		return ""
	}
	if args.StartLine <= 0 && args.EndLine <= 0 {
		return ""
	}
	// O teto cortou o fim da faixa: o modelo não viu as últimas linhas, e os candidatos saem em ordem de
	// linha — anotar além do corte seria anotar o invisível.
	if strings.Contains(out, "read cap;") {
		return ""
	}
	start := args.StartLine
	if start <= 0 {
		start = 1
	}
	shown := len(strings.Split(strings.TrimRight(out, "\r\n"), "\n"))
	end := args.EndLine
	if end <= 0 || end > start+shown-1 {
		end = start + shown - 1
	}
	if end < start {
		return ""
	}
	// Host remoto: não há servidor de linguagem deste lado para um arquivo do outro. E o caminho local que
	// por acaso existisse aqui teria as linhas de OUTRO arquivo — o servidor responderia com posições que
	// não casam com o texto mostrado.
	if t, err := ResolveTargetHost(args.Host, args.Path); err != nil || !t.IsLocal {
		return ""
	}
	src, err := os.ReadFile(expandHome(args.Path))
	if err != nil {
		return ""
	}
	return typeLine(args.Path, src, spanLines(start, end))
}

// typeCand é uma declaração candidata: o nome que o texto declara sem escrever o tipo.
type typeCand struct {
	name     string
	line     int // 1-based, absoluta no arquivo
	startCol int // UTF-16, como o servidor conta colunas
	endCol   int
}

// typeLine devolve a linha compacta que fecha um trecho lido, ou "" quando não há o que dizer.
//
// src é o texto inteiro do arquivo e lines são as linhas (1-based) que a resposta mostrou: só o que o
// modelo está vendo pode ser anotado. A linha sai prefixada com uma quebra dupla, pronta para concatenar.
func typeLine(path string, src []byte, lines []int) string {
	if lsp.Disabled() || len(lines) == 0 {
		return ""
	}
	// Linguagem sem servidor configurado não está degradada: não há como saber o tipo, e adivinhar é o
	// que esta camada existe para não fazer.
	if _, ok := lsp.LanguageForPath(path); !ok {
		return ""
	}
	cands := inferCandidates(splitLines(string(src)), lines)
	if len(cands) == 0 {
		return ""
	}
	if len(cands) > typeLineMaxNames {
		cands = cands[:typeLineMaxNames]
	}

	c, release, err := lsp.Manager().WarmFile(path)
	if err != nil {
		return "" // frio, desligado, linguagem sem servidor: silêncio, não nota
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), typeLineBudget)
	defer cancel()

	types := resolveTypes(ctx, c, path, cands)
	if len(types) == 0 {
		return ""
	}
	return "\ntypes: " + types
}

// resolveTypes casa cada candidato com o tipo que o servidor informa. Inlay hints primeiro (uma requisição
// para o trecho inteiro); hover depois, só para o que ficou sem tipo, e sempre dentro do orçamento.
func resolveTypes(ctx context.Context, c *lsp.Client, path string, cands []typeCand) string {
	first, last := cands[0].line, cands[0].line
	for _, cd := range cands {
		if cd.line < first {
			first = cd.line
		}
		if cd.line > last {
			last = cd.line
		}
	}
	got := map[string]string{}
	if hints, err := c.InlayHints(ctx, path, lsp.Range{
		Start: lsp.Position{Line: first - 1, Character: 0},
		End:   lsp.Position{Line: last, Character: 0},
	}); err == nil {
		for _, h := range hints {
			if h.Kind != 0 && h.Kind != 1 { // 2 é nome de parâmetro de chamada, não tipo
				continue
			}
			if cd, ok := candAt(cands, h.Position); ok {
				if _, seen := got[cd.name]; !seen {
					got[cd.name] = cleanType(h.Label)
				}
			}
		}
	}
	for _, cd := range cands {
		if _, seen := got[cd.name]; seen {
			continue
		}
		if ctx.Err() != nil {
			break // acabou o orçamento: sai com o que já veio, nunca com um chute
		}
		h, err := c.HoverAt(ctx, path, lsp.Position{Line: cd.line - 1, Character: cd.startCol})
		if err != nil || h == nil {
			continue
		}
		if t := hoverType(h.Contents, cd.name); t != "" {
			got[cd.name] = t
		}
	}

	var b strings.Builder
	for _, cd := range cands {
		t := got[cd.name]
		if t == "" {
			continue
		}
		piece := cd.name + ": " + t
		if b.Len() > 0 {
			piece = ", " + piece
		}
		if b.Len()+len(piece) > typeLineMaxBytes {
			break
		}
		b.WriteString(piece)
	}
	return b.String()
}

// candAt devolve o candidato que o hint anota. O servidor coloca o hint logo depois do nome; a folga de
// uma coluna cobre o padding que alguns servidores pedem.
func candAt(cands []typeCand, pos lsp.Position) (typeCand, bool) {
	var best typeCand
	found := false
	for _, cd := range cands {
		if cd.line-1 != pos.Line {
			continue
		}
		if pos.Character >= cd.startCol && pos.Character <= cd.endCol+1 {
			if !found || cd.startCol > best.startCol {
				best, found = cd, true
			}
		}
	}
	return best, found
}

// cleanType normaliza o rótulo do hint: alguns servidores escrevem ": int", outros "int".
func cleanType(label string) string {
	t := strings.TrimSpace(label)
	t = strings.TrimSpace(strings.TrimPrefix(t, ":"))
	if strings.ContainsAny(t, "\r\n") || len(t) > 120 {
		return ""
	}
	return t
}

// reHoverType reconhece as formas de hover em que o tipo aparece depois do nome (Go, Rust, C#, TS).
var reHoverType = regexp.MustCompile(`^(?:var|let|const|auto|val)\s+([A-Za-z_$][\w$]*)\s+(.+)$`)

// hoverType extrai o tipo do texto de hover, e só quando ele diz respeito a ESTE nome. Qualquer outra
// forma devolve "": o hover é texto livre do servidor, e um recorte errado viraria um tipo errado.
func hoverType(contents, name string) string {
	text := strings.TrimSpace(stripFence(contents))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i]) // a primeira linha é a assinatura; o resto é documentação
	}
	if m := reHoverType.FindStringSubmatch(text); m != nil && m[1] == name {
		return cleanType(m[2])
	}
	// Formas "nome: tipo" (Rust, TS, C# em alguns servidores).
	if rest, ok := strings.CutPrefix(text, name); ok {
		if t, ok := strings.CutPrefix(strings.TrimSpace(rest), ":"); ok {
			return cleanType(t)
		}
	}
	return ""
}

// stripFence tira o bloco de código markdown que os servidores usam no hover.
func stripFence(text string) string {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSuffix(strings.TrimRight(t, "\n"), "```")
}

// inferCandidates lê as linhas mostradas e devolve, na ordem do texto, os nomes que elas declaram sem
// tipo escrito.
//
// As formas são reconhecidas por linguagem, e as linguagens se sobrepõem de propósito (`var x = ...` é Go,
// C#, TS e Java). Sem desempatar, a mesma declaração entraria várias vezes e a linha sairia repetindo o
// nome — então o mesmo (linha, coluna) conta uma vez só.
func inferCandidates(file []string, lines []int) []typeCand {
	var out []typeCand
	seen := map[[2]int]bool{}
	for _, n := range lines {
		if n < 1 || n > len(file) {
			continue
		}
		body := strings.TrimRight(file[n-1], "\r")
		for _, pat := range inferPatterns {
			for _, m := range pat.re.FindAllStringSubmatchIndex(body, -1) {
				if m[2] < 0 {
					continue
				}
				for _, off := range nameOffsets(body[m[2]:m[3]]) {
					name := body[m[2]+off[0] : m[2]+off[1]]
					start := m[2] + off[0]
					if !isPlainIdent(name) || seen[[2]int{n, start}] {
						continue
					}
					seen[[2]int{n, start}] = true
					out = append(out, typeCand{
						name:     name,
						line:     n,
						startCol: utf16Len(body[:start]),
						endCol:   utf16Len(body[:m[2]+off[1]]),
					})
				}
			}
		}
	}
	// A ordem do texto é a ordem em que o modelo lê: `a, b := f()` anota a antes de b, mesmo que a segunda
	// forma a reconhecer tenha sido outra.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].line != out[j].line {
			return out[i].line < out[j].line
		}
		return out[i].startCol < out[j].startCol
	})
	return out
}

// spanLines devolve as linhas de um trecho contíguo [from, to] (1-based, inclusivo).
func spanLines(from, to int) []int {
	if from < 1 || to < from {
		return nil
	}
	out := make([]int, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, n)
	}
	return out
}

// nameOffsets separa uma lista de nomes ("a, b") nos intervalos de cada um dentro do grupo casado.
func nameOffsets(group string) [][2]int {
	var out [][2]int
	start := 0
	for i := 0; i <= len(group); i++ {
		if i < len(group) && group[i] != ',' {
			continue
		}
		part := group[start:i]
		trimmed := strings.TrimLeft(part, " \t")
		s := start + (len(part) - len(trimmed))
		e := s + len(strings.TrimRight(trimmed, " \t"))
		if e > s {
			out = append(out, [2]int{s, e})
		}
		start = i + 1
	}
	return out
}

// isPlainIdent recusa o que não é um nome simples (o grupo pode casar `x[0]` num caso torto).
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && (r == '$' || r == '_'):
		default:
			return false
		}
	}
	return true
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// inferPattern é uma forma de declaração sem tipo escrito. O grupo 1 é o nome (ou a lista de nomes).
type inferPattern struct{ re *regexp.Regexp }

var inferPatterns = []inferPattern{
	// Go: `x := ...`, `a, b := f()`, `for i, v := range xs`.
	{regexp.MustCompile(`^\s*(?:for\s+)?([A-Za-z_][\w]*(?:\s*,\s*[A-Za-z_][\w]*)*)\s*:=`)},
	// Go: `var x = ...` / `const x = ...` (com tipo escrito, `var x int = 1`, não casa).
	{regexp.MustCompile(`^\s*(?:var|const)\s+([A-Za-z_][\w]*(?:\s*,\s*[A-Za-z_][\w]*)*)\s*=`)},
	// C#: `var x = ...`, `foreach (var x in ...)`, e `let` de LINQ.
	{regexp.MustCompile(`^\s*(?:var|let)\s+([A-Za-z_]\w*)\s*=`)},
	{regexp.MustCompile(`foreach\s*\(\s*var\s+([A-Za-z_]\w*)\s+in\b`)},
	// TS/JS: `const x = ...` (com anotação, `const x: T = ...`, não casa) e parâmetro de lambda.
	{regexp.MustCompile(`^\s*(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)},
	{regexp.MustCompile(`(?:\(|^)\s*([A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)\s*\)?\s*=>`)},
	// Rust: `let x = ...` / `let mut x = ...` (com tipo, `let x: T = ...`, não casa).
	{regexp.MustCompile(`^\s*let\s+(?:mut\s+)?([A-Za-z_]\w*)\s*=`)},
	// Java: `var x = ...`.
	{regexp.MustCompile(`^\s*var\s+([A-Za-z_]\w*)\s*=`)},
}
