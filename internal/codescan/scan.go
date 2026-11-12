package codescan

import (
	"strings"
)

// Decl é uma declaração: linhas 1-based (EndLine = fim do bloco), nome, tipo, assinatura enxuta e se é exportada.
type Decl struct {
	Line, EndLine int
	Name, Kind    string
	Sig           string
	Exported      bool
}

// Ref é uma referência saindo do arquivo: chamada, import ou uso de nome importado.
//
// Col é a coluna 1-based do nome na linha (0 = o scanner não sabe). Só quem consegue posicionar o nome preenche:
// o C# precisa dela porque declara membro e classe na MESMA linha, e aí a linha sozinha não diz qual símbolo
// contém a referência (ver enclosing.go). Os demais scanners deixam 0 e continuam valendo pela linha.
type Ref struct {
	Line   int
	Col    int
	Target string
	Rel    string // "calls", "imports", "references"
}

const (
	maxLineLen = 400 // linha maior é minificada/gerada: ignorada
	sigMax     = 160
	utf8BOM    = "\xef\xbb\xbf"
)

// Scan extrai declarações e referências de um arquivo inteiro.
func Scan(src []byte, l *Lang) ([]Decl, []Ref) {
	if l.custom != nil { // linguagem fora do modelo "uma declaração por linha" (CSS/HTML/Razor)
		return l.custom(src)
	}
	text := strings.TrimPrefix(string(src), utf8BOM)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	clean, inside := cleanLines(lines, l)

	var decls []Decl
	declAt := map[int]string{}
	for i, raw := range lines {
		if inside[i] || len(raw) > maxLineLen || strings.TrimSpace(clean[i]) == "" {
			continue
		}
		d, ok := matchDecl(raw, l)
		if !ok {
			continue
		}
		d.Line = i + 1
		d.EndLine = endLine(lines, clean, i, l, d.Kind) + 1
		decls = append(decls, d)
		declAt[i] = d.Name
	}

	var refs []Ref
	for i, raw := range lines {
		if len(raw) > maxLineLen || strings.TrimSpace(clean[i]) == "" {
			continue
		}
		for _, re := range l.imports {
			if m := re.FindStringSubmatch(raw); m != nil {
				refs = append(refs, Ref{Line: i + 1, Target: m[1], Rel: "imports"})
			}
		}
		if l.importNames != nil {
			if m := l.importNames.FindStringSubmatch(raw); m != nil {
				for _, n := range importedNames(m[1]) {
					refs = append(refs, Ref{Line: i + 1, Target: n, Rel: "references"})
				}
				continue
			}
		}
		if strings.Contains(raw, "import") && len(l.imports) > 0 && isImportLine(raw, l) {
			continue
		}
		for _, m := range reCall.FindAllStringSubmatch(clean[i], -1) {
			name := m[1]
			if notAName[name] || declAt[i] == name {
				continue
			}
			refs = append(refs, Ref{Line: i + 1, Target: name, Rel: "calls"})
		}
	}
	return decls, refs
}

func isImportLine(raw string, l *Lang) bool {
	for _, re := range l.imports {
		if re.MatchString(raw) {
			return true
		}
	}
	return false
}

// importedNames lê "a, b as c, type D" e devolve os nomes originais (a, b, D).
func importedNames(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		f := strings.Fields(strings.TrimSpace(part))
		if len(f) == 0 {
			continue
		}
		name := f[0]
		if name == "type" && len(f) > 1 {
			name = f[1]
		}
		name = strings.Trim(name, "()")
		if name != "" && name != "*" {
			out = append(out, name)
		}
	}
	return out
}

func matchDecl(raw string, l *Lang) (Decl, bool) {
	for _, r := range l.rules {
		m := r.re.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		name := m[r.re.SubexpIndex("name")]
		if notAName[name] {
			continue
		}
		kind := r.kind(raw)
		if kind == "" {
			continue // a regra casou, mas a linha não é declaração (comando, protótipo)
		}
		return Decl{Name: name, Kind: kind, Sig: signature(raw), Exported: exported(l, kind, name, raw)}, true
	}
	if l.routes {
		if m := reHTTPRoute.FindStringSubmatch(raw); m != nil {
			return Decl{Name: m[reHTTPRoute.SubexpIndex("name")], Kind: "route", Sig: signature(raw), Exported: true}, true
		}
	}
	return Decl{}, false
}

// signature tira a chave de abertura do fim e corta assinaturas longas.
func signature(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "{"))
	if len(s) > sigMax {
		s = s[:sigMax] + "…"
	}
	return s
}

// endLine acha o índice (0-based) da última linha do bloco que começa em i: por chaves balanceadas no texto limpo,
// ou por indentação (Python). Declaração sem bloco (type X = ..., rota) termina na própria linha.
func endLine(lines, clean []string, i int, l *Lang, kind string) int {
	if kind == "route" || kind == "macro" || (kind == "type" && l.Name == "c") {
		return i
	}
	if l.indentBlocks {
		base := indentOf(lines[i])
		end := i
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(clean[j]) == "" {
				continue
			}
			if indentOf(lines[j]) <= base {
				break
			}
			end = j
		}
		return end
	}
	// parens abertos = assinatura em várias linhas: segue até fechar antes de procurar a chave do corpo
	depth, parens, started := 0, 0, false
	for j := i; j < len(clean) && j < i+20000; j++ {
		for _, c := range clean[j] {
			switch c {
			case '(':
				parens++
			case ')':
				parens--
			case '{':
				depth++
				started = true
			case '}':
				depth--
			}
		}
		if started && depth <= 0 {
			return j
		}
		if !started {
			if parens > 0 && j-i < 50 {
				continue
			}
			if j+1 < len(clean) {
				next := strings.TrimSpace(clean[j+1])
				// corpo na linha seguinte; em C++, também a lista de inicialização do construtor (": a(x)", ", b(y)")
				if strings.HasPrefix(next, "{") || (l.cppRaw && j-i < 50 && (strings.HasPrefix(next, ":") || strings.HasPrefix(next, ","))) {
					continue
				}
			}
			return i
		}
	}
	return i
}

// rawPrefixOK: o R de R"( começa um literal cru (não é o fim de um identificador), com ou sem prefixo L, u, U, u8.
func rawPrefixOK(line string, i int) bool {
	ident := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	j := i
	switch {
	case j >= 2 && line[j-2:j] == "u8":
		j -= 2
	case j >= 1 && (line[j-1] == 'L' || line[j-1] == 'u' || line[j-1] == 'U'):
		j--
	}
	return j == 0 || !ident(line[j-1])
}

func indentOf(s string) int {
	n := 0
	for _, c := range s {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// cleanLines devolve cada linha sem comentários e sem o conteúdo das strings (aspas preservadas), e se a linha começa
// dentro de comentário de bloco ou string multilinha. Assim chaves e "chamadas" dentro de texto não contam.
func cleanLines(lines []string, l *Lang) ([]string, []bool) {
	const (
		none = iota
		block
		tripleDQ
		tripleSQ
		backtick
		rawStr // C++ R"delim(...)delim"
	)
	clean := make([]string, len(lines))
	inside := make([]bool, len(lines))
	state := none
	rawClose := ""
	var b strings.Builder
	for n, line := range lines {
		inside[n] = state != none
		b.Reset()
	scan:
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch state {
			case block:
				if c == '*' && i+1 < len(line) && line[i+1] == '/' {
					state = none
					i++
				}
				continue
			case tripleDQ, tripleSQ:
				q := `"""`
				if state == tripleSQ {
					q = `'''`
				}
				if strings.HasPrefix(line[i:], q) {
					state = none
					b.WriteString(`""`)
					i += 2
				}
				continue
			case rawStr:
				if strings.HasPrefix(line[i:], rawClose) {
					state = none
					b.WriteByte('"')
					i += len(rawClose) - 1
				}
				continue
			case backtick:
				if c == '\\' && l.backtickEsc {
					i++
				} else if c == '`' {
					state = none
					b.WriteByte('`')
				}
				continue
			}
			switch {
			case l.slashComment && c == '/' && i+1 < len(line) && line[i+1] == '/':
				break scan
			case l.slashComment && c == '/' && i+1 < len(line) && line[i+1] == '*':
				state = block
				i++
			case l.hashComment && c == '#':
				break scan
			case l.tripleQuote && (strings.HasPrefix(line[i:], `"""`) || strings.HasPrefix(line[i:], `'''`)):
				if line[i] == '"' {
					state = tripleDQ
				} else {
					state = tripleSQ
				}
				i += 2
			case l.cppRaw && c == 'R' && i+1 < len(line) && line[i+1] == '"' && rawPrefixOK(line, i):
				if j := strings.IndexByte(line[i+2:], '('); j >= 0 && j <= 16 {
					rawClose = ")" + line[i+2:i+2+j] + "\""
					b.WriteByte('"')
					state = rawStr
					i += 2 + j
				} else {
					b.WriteByte(c)
				}
			case l.backtick && c == '`':
				b.WriteByte('`')
				state = backtick
			case c == '"' || c == '\'':
				b.WriteByte(c)
				j := i + 1
				for j < len(line) {
					if line[j] == '\\' {
						j += 2
						continue
					}
					if line[j] == c {
						break
					}
					j++
				}
				b.WriteByte(c)
				i = j
			default:
				b.WriteByte(c)
			}
		}
		clean[n] = b.String()
	}
	return clean, inside
}
