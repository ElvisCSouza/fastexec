// frontend.go: scanners de front-end (CSS/SCSS/LESS, HTML e Razor) registrados por extensão em langByExt.
// Ficam aqui, e não em regras por linha, porque a varredura genérica emite no máximo uma declaração por linha
// e não entende aninhamento — num CSS ".a, .b { ... }" são dois nós, e um id de HTML está no meio da tag.
// Como o despacho é por Lang (Scan → l.custom), code_outline e o grafo enxergam exatamente a mesma extração.
package codescan

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// addRef acrescenta a referência sem repetir a mesma aresta na mesma linha (o índice insere cada Ref
// recebido; um "class=\"a a\"" ou dois var(--x) na linha virariam linhas duplicadas na resposta).
func addRef(refs *[]Ref, seen map[string]bool, line int, target, rel string) {
	if target == "" {
		return
	}
	k := strconv.Itoa(line) + "\x00" + rel + "\x00" + target
	if seen[k] {
		return
	}
	seen[k] = true
	*refs = append(*refs, Ref{Line: line, Target: target, Rel: rel})
}

var (
	langCSS   = &Lang{Name: "css", custom: scanCSS}
	langHTML  = &Lang{Name: "html", custom: scanHTML}
	langRazor = &Lang{Name: "razor", custom: scanRazor}

	reCSSSel    = regexp.MustCompile(`([.#])([A-Za-z_][A-Za-z0-9_-]*)`) // seletor de classe/id
	reCSSVarUse = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)`)      // uso de variável CSS

	// (?i) e o prefixo [\s"'] evitam casar data-id / aria-id dentro de outro atributo.
	reHTMLID    = regexp.MustCompile(`(?i)(?:^|[\s"'])id\s*=\s*["']([^"']+)["']`)
	reHTMLClass = regexp.MustCompile(`(?i)(?:^|[\s"'])class\s*=\s*["']([^"']*)["']`)
	reScriptSrc = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
	reLinkHref  = regexp.MustCompile(`(?i)<link\b[^>]*\bhref\s*=\s*["']([^"']+)["']`)
	reElemByID  = regexp.MustCompile(`getElementById\(\s*["']([^"']+)["']`)

	// Nome de tag em marcação JSX/Blazor: grupo 1 = "/" de fechamento, grupo 2 = nome (pontuado).
	reTagName = regexp.MustCompile(`<(/?)([A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*)`)

	reRazorCode = regexp.MustCompile(`^@(?:code\b|functions\b|\{)`)
)

// frontLines normaliza o arquivo para uma lista de linhas (BOM fora, CRLF → LF).
func frontLines(src []byte) []string {
	text := strings.TrimPrefix(string(src), utf8BOM)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

// tooLong: linha gerada/minificada não é analisada (mesmo corte da varredura por linha).
func tooLong(line string) bool { return len(line) > maxLineLen }

// ---- CSS/SCSS/LESS -------------------------------------------------------------------------------

// scanCSS extrai seletores de classe/id (nós), variáveis --x (nós) e usos var(--x) (referências).
// Seletores compostos viram um nó por nome: ".btn, .btn--primary:hover" define as duas classes.
// O fim de cada nó é o "}" do bloco em que o seletor foi aberto.
func scanCSS(src []byte) ([]Decl, []Ref) {
	lines := frontLines(src)
	code := blankCSS(lines)

	var decls []Decl
	var refs []Ref
	seen := map[string]bool{}
	var stack [][]int // por bloco aberto: índices das declarações que ele contém

	push := func(d Decl) {
		decls = append(decls, d)
		if n := len(stack); n > 0 {
			stack[n-1] = append(stack[n-1], len(decls)-1)
		}
	}
	closeBlock := func(line int) {
		n := len(stack)
		if n == 0 {
			return
		}
		for _, k := range stack[n-1] {
			if decls[k].EndLine < line {
				decls[k].EndLine = line
			}
		}
		stack = stack[:n-1]
	}

	for i, ln := range code {
		lineno := i + 1
		if tooLong(lines[i]) {
			continue
		}
		start := 0
		for j := 0; j < len(ln); j++ {
			c := ln[j]
			if c != '{' && c != '}' && c != ';' {
				continue
			}
			seg := strings.TrimSpace(ln[start:j])
			start = j + 1
			switch c {
			case '{':
				stack = append(stack, nil) // o bloco existe mesmo sem declaração própria (@media, &:hover)
				if seg != "" && !strings.HasPrefix(seg, "@") {
					for _, d := range cssSelectorDecls(seg, lineno) {
						push(d)
					}
				}
			case '}':
				closeBlock(lineno)
			case ';':
				if d, ok := cssVarDecl(seg, lineno); ok {
					push(d)
				}
			}
		}
		for _, m := range reCSSVarUse.FindAllStringSubmatch(ln, -1) {
			addRef(&refs, seen, lineno, m[1], "references")
		}
	}
	for i := range decls {
		if decls[i].EndLine == 0 { // seletor fora de bloco ou arquivo truncado
			decls[i].EndLine = decls[i].Line
		}
	}
	return decls, refs
}

// cssSelectorDecls quebra "a .x, b#y:hover" em um nó por classe/id, sem repetir o mesmo nome na mesma regra.
func cssSelectorDecls(seg string, lineno int) []Decl {
	var out []Decl
	seen := map[string]bool{}
	for _, part := range strings.Split(seg, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		for _, m := range reCSSSel.FindAllStringSubmatch(p, -1) {
			kind := "css_class"
			if m[1] == "#" {
				kind = "css_id"
			}
			key := kind + " " + m[2]
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Decl{Line: lineno, EndLine: lineno, Name: m[2], Kind: kind, Sig: signature(p), Exported: true})
		}
	}
	return out
}

func cssVarDecl(seg string, lineno int) (Decl, bool) {
	if !strings.HasPrefix(seg, "--") {
		return Decl{}, false
	}
	name := strings.TrimSpace(strings.SplitN(seg, ":", 2)[0])
	if len(name) < 3 {
		return Decl{}, false
	}
	return Decl{Line: lineno, EndLine: lineno, Name: name, Kind: "css_var", Sig: signature(seg), Exported: true}, true
}

// blankCSS apaga comentários (/* */ e //) e o conteúdo das strings — uma string pode conter { } ; e
// desbalancear a contagem de blocos.
func blankCSS(lines []string) []string {
	out := make([]string, len(lines))
	inComment := false
	for i, ln := range lines {
		var b strings.Builder
		for j := 0; j < len(ln); j++ {
			c := ln[j]
			if inComment {
				if c == '*' && j+1 < len(ln) && ln[j+1] == '/' {
					inComment = false
					j++
				}
				continue
			}
			if c == '/' && j+1 < len(ln) {
				if ln[j+1] == '*' {
					inComment = true
					j++
					continue
				}
				// "//" só é comentário no começo da linha (SCSS/LESS): no meio, url(//cdn/x.png) seria truncada
				if ln[j+1] == '/' && strings.TrimSpace(ln[:j]) == "" {
					break
				}
			}
			if c == '"' || c == '\'' {
				b.WriteByte(c)
				j++
				for j < len(ln) && ln[j] != c {
					if ln[j] == '\\' && j+1 < len(ln) {
						j++
					}
					j++
				}
				if j < len(ln) {
					b.WriteByte(ln[j])
				}
				continue
			}
			b.WriteByte(c)
		}
		out[i] = b.String()
	}
	return out
}

// ---- JSX/TSX ---------------------------------------------------------------------------------------
//
// langTSX existe para não mexer em langJS: .ts/.js continuam com a extração byte a byte de sempre, e só .tsx/.jsx
// ganham o que é de JSX (componente/hook, props na assinatura, arestas "renders" e className). As regras de linha
// são as mesmas — scanTSX chama Scan(src, langJS) e acrescenta por cima.

var (
	langTSX = &Lang{Name: "tsx", custom: scanTSX}

	// className="a b" e className={'a b'} (e class= de Preact/SVG): só literal. Template/clsx dinâmico fica de fora
	// porque o valor não está entre aspas — melhor não responder do que inventar.
	reJSClassName = regexp.MustCompile(`(?:^|[^\w])class(?:Name)?\s*=\s*(?:\{\s*)?["']([^"']*)["']`)
)

// scanTSX roda as regras de .ts/.js e promove a componente/hook o que só se sabe olhando o corpo ou o nome.
func scanTSX(src []byte) ([]Decl, []Ref) {
	decls, refs := Scan(src, langJS)
	lines := frontLines(src)
	code, _ := cleanLines(lines, langJS) // sem comentário e sem conteúdo de string: serve para achar tags
	raw := stripJSComments(lines)        // comentário fora, string dentro: é onde vive o className

	for i := range decls {
		if decls[i].Kind != "func" || decls[i].Name == "" {
			continue
		}
		switch {
		case isHookName(decls[i].Name):
			decls[i].Kind = "hook"
		case isUpper(decls[i].Name[0]) && hasJSX(code, decls[i].Line, decls[i].EndLine):
			decls[i].Kind = "component"
		default:
			continue
		}
		if sig, ok := jsxSig(decls[i].Name, decls[i].Kind, lines, decls[i].Line); ok {
			decls[i].Sig = sig
		}
	}

	seen := map[string]bool{}
	for i, ln := range code {
		lineno := i + 1
		if tooLong(lines[i]) {
			continue
		}
		for _, r := range componentRefs(ln, lineno) {
			addRef(&refs, seen, lineno, r.Target, r.Rel)
		}
		for _, m := range reJSClassName.FindAllStringSubmatch(raw[i], -1) {
			for _, c := range classTokens(m[1]) {
				addRef(&refs, seen, lineno, c, "references")
			}
		}
		for _, m := range reCSSVarUse.FindAllStringSubmatch(raw[i], -1) {
			addRef(&refs, seen, lineno, m[1], "references")
		}
	}
	return decls, refs
}

// isHookName: "useX" com X maiúsculo (o hook do React); "user", "used" e afins ficam de fora.
func isHookName(n string) bool {
	return strings.HasPrefix(n, "use") && len(n) > 3 && isUpper(n[3])
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// hasJSX diz se o corpo (linhas [start, end], 1-based) tem marcação. Basta um fechamento "</" ou um "/>" ou um
// fragmento "<>": toda função que devolve JSX tem pelo menos um deles, e nenhum deles aparece em expressão comum
// (comparação "a < b" e divisão "a / b" ficam sem o caractere colado).
func hasJSX(code []string, start, end int) bool {
	if start < 1 {
		start = 1
	}
	if end > len(code) {
		end = len(code)
	}
	for i := start - 1; i < end; i++ {
		l := code[i]
		if strings.Contains(l, "</") || strings.Contains(l, "/>") || strings.Contains(l, "<>") {
			return true
		}
	}
	return false
}

// jsxSig monta "component Card({title}: Props)" / "hook useThing(id)" com a lista de parâmetros da própria
// assinatura (que pode estar quebrada em várias linhas). Devolve ok=false quando o parâmetro não vem entre
// parênteses — "const C = props => ..." fica com a assinatura da varredura de linha, que já é exata.
func jsxSig(name, kind string, lines []string, start int) (string, bool) {
	if start < 1 || start > len(lines) {
		return "", false
	}
	var b strings.Builder
	depth, opened := 0, false
	for i := start - 1; i < len(lines) && i < start+40; i++ {
		for j := 0; j < len(lines[i]); j++ {
			c := lines[i][j]
			switch {
			case c == '(':
				depth++
				if depth == 1 {
					opened = true
					continue
				}
			case c == ')':
				depth--
				if depth == 0 {
					return kind + " " + name + "(" + signature(b.String()) + ")", true
				}
			case c == '=' && j+1 < len(lines[i]) && lines[i][j+1] == '>':
				if !opened {
					return "", false // props fora de parênteses: a lista não está delimitada
				}
			case c == '{' && depth == 0 && !opened:
				return "", false // corpo antes de qualquer parâmetro: não há lista de props
			}
			if opened {
				b.WriteByte(c)
			}
		}
		if !opened && i > start-1 {
			break // continuou para a linha seguinte sem achar "(": não é assinatura multi-linha
		}
	}
	return "", false
}

// stripJSComments tira // e /* */ preservando o conteúdo das strings (é dentro delas que está o className).
// Template literal é descartado inteiro: nunca tem classe literal, e assim uma crase não engole o resto do arquivo.
func stripJSComments(lines []string) []string {
	out := make([]string, len(lines))
	block, tmpl := false, false
	for i, ln := range lines {
		var b strings.Builder
		for j := 0; j < len(ln); j++ {
			c := ln[j]
			switch {
			case block:
				if c == '*' && j+1 < len(ln) && ln[j+1] == '/' {
					block = false
					j++
				}
				continue
			case tmpl:
				if c == '\\' && j+1 < len(ln) {
					j++
					continue
				}
				if c == '`' {
					tmpl = false
				}
				continue
			}
			if c == '/' && j+1 < len(ln) {
				if ln[j+1] == '/' {
					break
				}
				if ln[j+1] == '*' {
					block = true
					j++
					continue
				}
			}
			if c == '`' {
				tmpl = true
				continue
			}
			if c == '"' || c == '\'' {
				b.WriteByte(c)
				j++
				for j < len(ln) && ln[j] != c {
					if ln[j] == '\\' && j+1 < len(ln) {
						j++
					}
					b.WriteByte(ln[j])
					j++
				}
				if j < len(ln) {
					b.WriteByte(ln[j])
				}
				continue
			}
			b.WriteByte(c)
		}
		out[i] = b.String()
	}
	return out
}

// ---- HTML ----------------------------------------------------------------------------------------

func scanHTML(src []byte) ([]Decl, []Ref) { return scanMarkup(src, false) }

func scanRazor(src []byte) ([]Decl, []Ref) {
	return scanMarkup(src, true)
}

// scanMarkup extrai de HTML/Razor: ids (nós), classes usadas (referências), getElementById (referências),
// <script src>/<link href> locais (arestas de arquivo) e, no Razor, tags de componente (<Card>, <A.B>).
func scanMarkup(src []byte, razor bool) ([]Decl, []Ref) {
	lines := frontLines(src)
	code := blankMarkup(lines, razor)
	if razor {
		code = blankRazorCode(code)
	}

	var decls []Decl
	var refs []Ref
	seen := map[string]bool{}
	for i, ln := range code {
		lineno := i + 1
		if tooLong(lines[i]) {
			continue
		}
		if m := reHTMLID.FindStringSubmatchIndex(ln); m != nil {
			id := strings.TrimSpace(ln[m[2]:m[3]])
			if id != "" && !strings.ContainsAny(id, "{}@%$") {
				decls = append(decls, Decl{Line: lineno, EndLine: lineno, Name: id, Kind: "html_id",
					Sig: tagAt(ln, m[0]), Exported: true})
			}
		}
		for _, m := range reHTMLClass.FindAllStringSubmatch(ln, -1) {
			for _, c := range classTokens(m[1]) {
				addRef(&refs, seen, lineno, c, "references")
			}
		}
		for _, re := range []*regexp.Regexp{reScriptSrc, reLinkHref} {
			if m := re.FindStringSubmatch(ln); m != nil {
				if t, ok := localAsset(m[1], razor); ok {
					addRef(&refs, seen, lineno, t, "html_ref")
				}
			}
		}
		for _, m := range reElemByID.FindAllStringSubmatch(ln, -1) {
			addRef(&refs, seen, lineno, m[1], "references")
		}
		if razor {
			for _, r := range componentRefs(ln, lineno) {
				addRef(&refs, seen, lineno, r.Target, r.Rel)
			}
		}
	}
	return decls, refs
}

// tagAt devolve a tag que contém a posição i (para a assinatura do id no outline).
func tagAt(line string, i int) string {
	start := strings.LastIndex(line[:i], "<")
	if start < 0 {
		start = 0
	}
	if end := strings.Index(line[start:], ">"); end >= 0 {
		return signature(line[start : start+end+1])
	}
	return signature(line[start:])
}

// ClassTokens quebra class="a b" em nomes; qualquer coisa com interpolação é descartada (não inventar).
func classTokens(v string) []string {
	var out []string
	for _, f := range strings.Fields(v) {
		if strings.ContainsAny(f, "{}@%$") {
			continue
		}
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || r == '_' }) != 0 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// localAsset aceita só referência local: fora ficam http(s), //, data:, mailto:, âncora e qualquer
// url com interpolação (@/{{ }}/%/$, típica de Razor e de template). Em Razor "~/x" é a raiz do site.
func localAsset(raw string, razor bool) (string, bool) {
	s := strings.TrimSpace(raw)
	if razor {
		s = strings.TrimPrefix(s, "~/")
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" || strings.ContainsAny(s, "{}@%$") {
		return "", false
	}
	l := strings.ToLower(s)
	for _, p := range []string{"http:", "https:", "//", "data:", "mailto:", "javascript:", "tel:", "ftp:", "#"} {
		if strings.HasPrefix(l, p) {
			return "", false
		}
	}
	return s, true
}

// componentRefs acha tags de componente (<Card>, <A.B>, <Card .../>) e devolve a referência "renders".
// O alvo é o último segmento do nome (<A.B> → B), a mesma convenção de pkg.Func, para callers/def casarem.
// Tag de fechamento é ignorada (é o mesmo render) e um "<" precedido de letra é genérico (a<B, Array<Card>).
func componentRefs(line string, lineno int) []Ref {
	var out []Ref
	for _, m := range reTagName.FindAllStringSubmatchIndex(line, -1) {
		at := m[0]
		if at > 0 && isWordByte(line[at-1]) {
			continue
		}
		if line[m[2]:m[3]] == "/" {
			continue
		}
		name := line[m[4]:m[5]]
		if !unicode.IsUpper(rune(name[0])) {
			continue
		}
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, Ref{Line: lineno, Target: name, Rel: "renders"})
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// blankMarkup apaga <!-- --> e, no Razor, @* *@.
func blankMarkup(lines []string, razor bool) []string {
	out := make([]string, len(lines))
	inHTML, inRazor := false, false
	for i, ln := range lines {
		var b strings.Builder
		for j := 0; j < len(ln); j++ {
			switch {
			case inHTML:
				if strings.HasPrefix(ln[j:], "-->") {
					inHTML = false
					j += 2
				}
				continue
			case inRazor:
				if strings.HasPrefix(ln[j:], "*@") {
					inRazor = false
					j++
				}
				continue
			}
			if razor && strings.HasPrefix(ln[j:], "@*") {
				if k := strings.Index(ln[j+2:], "*@"); k >= 0 {
					j += 2 + k + 1
					continue
				}
				inRazor = true
				j++
				continue
			}
			if strings.HasPrefix(ln[j:], "<!--") {
				if k := strings.Index(ln[j+4:], "-->"); k >= 0 {
					j += 4 + k + 2
					continue
				}
				inHTML = true
				j += 3
				continue
			}
			b.WriteByte(ln[j])
		}
		out[i] = b.String()
	}
	return out
}

// blankRazorCode apaga os blocos @code/@functions/@{ ... }: é C#, não marcação (o corpo é ignorado).
func blankRazorCode(code []string) []string {
	depth, waiting := 0, false
	for i, ln := range code {
		if waiting { // "@code" sozinho: o "{" vem na linha seguinte
			code[i] = ""
			if strings.Contains(ln, "{") {
				waiting = false
				depth = strings.Count(ln, "{") - strings.Count(ln, "}")
			}
			continue
		}
		if depth <= 0 {
			if !reRazorCode.MatchString(strings.TrimSpace(ln)) {
				continue
			}
			code[i] = ""
			if strings.Contains(ln, "{") {
				depth = strings.Count(ln, "{") - strings.Count(ln, "}")
			} else {
				waiting = true
			}
			continue
		}
		depth += strings.Count(ln, "{") - strings.Count(ln, "}")
		code[i] = ""
	}
	return code
}
