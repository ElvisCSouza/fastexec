package graph

// csharp.go: parser de C# (.cs) e dos arquivos de projeto do .NET (.csproj/.sln) por linhas + contagem de chaves, no
// estilo dos scanners de linha do internal/codescan — mas com léxico e escopos próprios, por dois motivos:
//
//   - léxico: o C# tem strings verbatim (@"...", onde "" é a aspa escapada), interpoladas ($"...", $@"...") e cruas
//     ("""..."""), além de comentário // e /* */; sem isso, um "{" ou um "Nome(" dentro de texto viraria declaração
//     ou aresta falsa.
//   - escopos: em C# só se declara em namespace ou dentro de um tipo. Sabendo em que escopo a linha está, declaração
//     e chamada se separam sem resolução de tipos (que o grafo não faz, em linguagem nenhuma).
//
// O nome guardado é o SIMPLES (PedidoService, ListarAsync), como em todos os outros parsers: callers/def/impact
// casam por nome (query.go usa lastName). A identidade qualificada sai do rótulo do tipo que envolve a chamada
// (typeKindsSQL, a mesma via do Go) e do símbolo kind='namespace', que é o que permite resolver `using` no impact.
// Linhas, fim de bloco e assinatura seguem o contrato do codescan.Decl.

import (
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/codescan"
)

const (
	csBOM     = "\xef\xbb\xbf"
	csMaxLine = 600 // linha maior que isso é gerada/minificada: nem declaração nem aresta (igual ao codescan)
)

// IsCSharp: arquivo que o parser deste arquivo entende (.cs de código e .csproj/.sln de projeto).
func IsCSharp(rel string) bool {
	switch strings.ToLower(filepath.Ext(filepath.Base(rel))) {
	case ".cs", ".csproj", ".sln":
		return true
	}
	return false
}

func isCSProject(rel string) bool {
	switch strings.ToLower(filepath.Ext(filepath.Base(rel))) {
	case ".csproj", ".sln":
		return true
	}
	return false
}

var (
	reCSNamespaceFS = regexp.MustCompile(`^namespace\s+([\w.]+)\s*;$`)
	reCSNamespaceBl = regexp.MustCompile(`^namespace\s+([\w.]+)\s*\{?$`)

	// tipo: atributos, modificadores e a palavra-chave; o nome vem logo depois (record struct/class incluídos).
	reCSType = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|abstract|sealed|partial|readonly|unsafe|new|file|ref)\s+)*(class|struct|interface|record|enum)\s+(?:(?:class|struct)\s+)?(\w+)`)

	reCSDelegate = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|unsafe|new)\s+)*delegate\s+[^;{=]*?(\w+)\s*(?:<[^>]*>)?\s*\(`)

	// membro com lista de parâmetros: método, construtor (sem tipo de retorno) ou operador (o nome sai antes do '(').
	reCSMember = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|virtual|override|abstract|async|sealed|extern|unsafe|new|partial|readonly|required|volatile)\s+)*(?:[\w.<>\[\],?@]+\s+)?(\w+)\s*(?:<[^()<>]*>)?\s*\(`)

	// propriedade: tipo, nome e então o bloco de acesso ou o corpo de expressão.
	reCSProperty = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|virtual|override|abstract|sealed|new|readonly|required|unsafe|extern)\s+)*(?:[\w.<>\[\],?@]+\s+)(\w+)\s*(?:\{|=>)`)

	// campo: exige modificador; só os const/static entram no grafo (os demais são estado interno, ruído no índice).
	reCSField = regexp.MustCompile(`^(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|const|readonly|volatile|new|unsafe|required)\s+)+[\w.<>\[\],?@]+\s+(\w+)\s*(?:=|;|,)`)

	reCSUsing = regexp.MustCompile(`^(?:global\s+)?using\s+(?:static\s+)?(?:\w+\s*=\s*)?([\w.]+)\s*;`)
	reCSCall  = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*\(`)
	reCSNew   = regexp.MustCompile(`\bnew\s+([\w.]+)\s*[({<[]`)

	reCSProjRef = regexp.MustCompile(`(?is)<ProjectReference\b[^>]*\bInclude\s*=\s*"([^"]+)"`)
	reCSlnProj  = regexp.MustCompile(`(?m)^Project\("[^"]*"\)\s*=\s*"[^"]*",\s*"([^"]+)"`)

	// palavras que o regex de chamada confundiria com nome: controle, pseudo-chamadas e palavras-chave do C#.
	csNotCall = map[string]bool{"if": true, "for": true, "foreach": true, "while": true, "switch": true, "catch": true,
		"using": true, "lock": true, "fixed": true, "return": true, "throw": true, "new": true, "sizeof": true,
		"typeof": true, "nameof": true, "default": true, "checked": true, "unchecked": true, "await": true,
		"yield": true, "when": true, "where": true, "get": true, "set": true, "init": true, "add": true, "remove": true,
		"case": true, "do": true, "else": true, "in": true, "is": true, "as": true, "stackalloc": true,
		"delegate": true, "operator": true, "goto": true, "base": true, "this": true, "out": true, "ref": true,
		"params": true, "catch_when": true}
)

// ParseCSharp devolve as declarações de um arquivo C#/.csproj/.sln; ok=false para outra extensão.
func ParseCSharp(rel string, src []byte) ([]codescan.Decl, bool) {
	d, _, ok := parseCSharp(rel, src)
	return d, ok
}

func parseCSharp(rel string, src []byte) ([]codescan.Decl, []codescan.Ref, bool) {
	if !IsCSharp(rel) {
		return nil, nil, false
	}
	text := strings.TrimPrefix(string(src), csBOM)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if isCSProject(rel) {
		return parseCSProject(rel, text), csProjectRefs(rel, text), true
	}
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" { // sem a última linha vazia: o fim do namespace é a última linha de código
		lines = lines[:n-1]
	}
	clean, inside := csCleanLines(lines)
	p := &csParser{total: len(lines)}
	p.run(lines, clean, inside)
	for i := range p.decls {
		if p.decls[i].EndLine < p.decls[i].Line {
			p.decls[i].EndLine = p.decls[i].Line
		}
	}
	return p.decls, p.refs, true
}

type csPending struct {
	decl  int    // índice em csParser.decls
	scope string // "namespace" ou "type" (o que a chave de abertura vai virar)
}

type csScope struct {
	kind  string // "namespace", "type", "member" ou "block"
	name  string // nome do tipo, quando kind == "type"
	decl  int    // declaração que abriu o escopo (-1 para bloco solto)
	depth int    // profundidade de chaves logo após a abertura
}

type csParser struct {
	decls   []codescan.Decl
	refs    []codescan.Ref
	ns      string
	stack   []csScope
	pend    []csPending
	depth   int
	total   int
	declAt  map[int]string
	curLine int
}

func (p *csParser) run(lines, clean []string, inside []bool) {
	for i := range lines {
		p.curLine = i + 1
		if !inside[i] {
			p.declLine(i+1, lines[i], clean[i])
		}
		p.braceLine(i+1, clean[i])
		p.refLine(i+1, clean[i])
	}
	p.pend = nil
	for len(p.stack) > 0 {
		p.closeScope(p.total)
	}
}

func (p *csParser) top() string {
	if len(p.stack) == 0 {
		return ""
	}
	return p.stack[len(p.stack)-1].kind
}

func (p *csParser) ownerType() string {
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].kind == "type" {
			return p.stack[i].name
		}
	}
	return ""
}

func (p *csParser) add(d codescan.Decl, scope string, open bool) {
	p.decls = append(p.decls, d)
	if p.declAt == nil {
		p.declAt = map[int]string{}
	}
	p.declAt[d.Line] = d.Name
	if scope != "" && open {
		p.pend = append(p.pend, csPending{decl: len(p.decls) - 1, scope: scope})
	}
}

// braceLine mantém a pilha de escopos: a chave que abre consome a declaração pendente (namespace, tipo ou membro) e
// a que fecha carimba o fim do bloco na declaração daquele escopo.
func (p *csParser) braceLine(n int, cl string) {
	for i := 0; i < len(cl); i++ {
		switch cl[i] {
		case '{':
			p.depth++
			if k := len(p.pend) - 1; k >= 0 {
				pd := p.pend[k]
				p.pend = p.pend[:k]
				p.stack = append(p.stack, csScope{kind: pd.scope, name: p.decls[pd.decl].Name, decl: pd.decl, depth: p.depth})
			} else {
				p.stack = append(p.stack, csScope{kind: "block", decl: -1, depth: p.depth})
			}
		case '}':
			if len(p.stack) > 0 && p.stack[len(p.stack)-1].depth == p.depth {
				p.closeScope(n)
			}
			p.depth--
		}
	}
}

func (p *csParser) closeScope(n int) {
	if len(p.stack) == 0 {
		return
	}
	sc := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	if sc.decl >= 0 && p.decls[sc.decl].EndLine < n {
		p.decls[sc.decl].EndLine = n
	}
}

// declLine reconhece uma declaração. Dentro de membro (corpo de método, lambda, bloco solto) não há declaração: só
// ali se separa declaração de chamada.
func (p *csParser) declLine(n int, raw, cl string) {
	t := strings.TrimSpace(cl)
	if t == "" || len(raw) > csMaxLine {
		return
	}
	inType := p.top() == "type"
	if len(p.pend) > 0 && p.pend[len(p.pend)-1].scope == "member" && strings.HasSuffix(t, ";") {
		p.pend = p.pend[:len(p.pend)-1] // assinatura fechada sem corpo (`M()\n    => x;`): não espera mais chave
	}
	if !inType {
		if m := reCSNamespaceFS.FindStringSubmatch(t); m != nil {
			p.ns = m[1]
			p.decls = append(p.decls, codescan.Decl{Line: n, EndLine: p.total, Name: m[1], Kind: "namespace",
				Sig: "namespace " + m[1], Exported: true})
			return
		}
		if m := reCSNamespaceBl.FindStringSubmatch(t); m != nil {
			p.ns = m[1]
			p.add(codescan.Decl{Line: n, Name: m[1], Kind: "namespace", Sig: "namespace " + m[1], Exported: true},
				"namespace", true)
			return
		}
	}
	if m := reCSType.FindStringSubmatchIndex(t); m != nil {
		name := t[m[4]:m[5]]
		p.add(codescan.Decl{Line: n, Name: name, Kind: t[m[2]:m[3]], Sig: csSignature(raw),
			Exported: csExported(t[:m[2]])}, "type", !strings.HasSuffix(t, ";"))
		p.baseRefs(n, t[m[5]:])
		return
	}
	if !inType {
		if m := reCSDelegate.FindStringSubmatchIndex(t); m != nil {
			p.add(codescan.Decl{Line: n, Name: t[m[2]:m[3]], Kind: "delegate", Sig: csSignature(raw),
				Exported: csExported(t[:m[2]])}, "", false)
			return
		}
		// lista de bases na linha seguinte (`public class Foo` / `    : Bar, IFoo`): o tipo ainda não abriu chave
		if len(p.pend) > 0 && p.pend[len(p.pend)-1].scope == "type" && strings.HasPrefix(t, ":") {
			p.baseRefs(n, t)
		}
		return
	}
	if m := reCSMember.FindStringSubmatchIndex(t); m != nil {
		name := t[m[2]:m[3]]
		kind := "method"
		if name == p.ownerType() {
			kind = "constructor"
		}
		p.add(codescan.Decl{Line: n, Name: name, Kind: kind, Sig: csSignature(raw), Exported: csExported(t[:m[2]])},
			"member", !strings.HasSuffix(t, ";"))
		return
	}
	if m := reCSProperty.FindStringSubmatchIndex(t); m != nil {
		p.add(codescan.Decl{Line: n, Name: t[m[2]:m[3]], Kind: "property", Sig: csSignature(raw),
			Exported: csExported(t[:m[2]])}, "member", !strings.HasSuffix(t, ";") && strings.Contains(t, "{"))
		return
	}
	if m := reCSField.FindStringSubmatchIndex(t); m != nil {
		mods := " " + t[:m[2]] + " "
		if !strings.Contains(mods, " const ") && !strings.Contains(mods, " static ") {
			return // campo de instância: estado interno, não entra no índice
		}
		p.add(codescan.Decl{Line: n, Name: t[m[2]:m[3]], Kind: "field", Sig: csSignature(raw),
			Exported: csExported(t[:m[2]])}, "", false)
	}
}

// baseRefs emite a lista de bases do tipo como arestas: herança da classe base e implementação das interfaces
// (a convenção I+ maiúscula é a do próprio .NET; sem resolução de tipos não há como ir além disso).
func (p *csParser) baseRefs(n int, rest string) {
	for _, b := range csBaseList(rest) {
		rel := "inherits"
		if isIfaceName(b) {
			rel = "implements"
		}
		p.refs = append(p.refs, codescan.Ref{Line: n, Target: b, Rel: rel})
	}
}

// csBaseList lê o resto da linha depois do nome do tipo: pula os genéricos e o construtor primário (record) e
// devolve a lista de bases, sem argumentos de tipo e sem cláusulas where.
func csBaseList(rest string) []string {
	i := 0
	skip := func() {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
	}
	skip()
	if i < len(rest) && rest[i] == '<' {
		i = csSkipBalanced(rest, i, '<', '>')
	}
	skip()
	if i < len(rest) && rest[i] == '(' {
		i = csSkipBalanced(rest, i, '(', ')')
	}
	skip()
	if i >= len(rest) || rest[i] != ':' {
		return nil
	}
	tail := rest[i+1:]
	if j := strings.IndexAny(tail, "{;"); j >= 0 {
		tail = tail[:j]
	}
	if j := strings.Index(tail, " where "); j >= 0 {
		tail = tail[:j]
	}
	var out []string
	for _, part := range strings.Split(tail, ",") {
		part = strings.TrimSpace(part)
		if j := strings.IndexByte(part, '<'); j >= 0 {
			part = strings.TrimSpace(part[:j])
		}
		if part == "" {
			continue
		}
		out = append(out, lastName(part))
	}
	return out
}

func csSkipBalanced(s string, i int, open, close byte) int {
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func isIfaceName(n string) bool {
	return len(n) > 1 && n[0] == 'I' && n[1] >= 'A' && n[1] <= 'Z'
}

// refLine emite as arestas de saída: imports (`using`), chamadas (`Nome(` e `.Nome(`) e criação de tipo (`new T(`).
func (p *csParser) refLine(n int, cl string) {
	if strings.TrimSpace(cl) == "" {
		return
	}
	if m := reCSUsing.FindStringSubmatch(cl); m != nil {
		p.refs = append(p.refs, codescan.Ref{Line: n, Target: m[1], Rel: "imports"})
		return
	}
	attr := csAttrMask(cl)
	for _, loc := range reCSCall.FindAllStringSubmatchIndex(cl, -1) {
		name := cl[loc[2]:loc[3]]
		if csNotCall[name] || p.declAt[n] == name || csAfterNew(cl, loc[2]) || attr[loc[2]] {
			continue
		}
		p.refs = append(p.refs, codescan.Ref{Line: n, Target: name, Rel: "calls"})
	}
	for _, loc := range reCSNew.FindAllStringSubmatchIndex(cl, -1) {
		if name := lastName(cl[loc[2]:loc[3]]); name != "" && !csNotCall[name] && !attr[loc[0]] {
			p.refs = append(p.refs, codescan.Ref{Line: n, Target: name, Rel: "references"})
		}
	}
}

// csAttrMask marca os bytes que estão dentro de um atributo `[...]`: o nome ali não é chamada nem criação de tipo
// (`[Obsolete("x")]` não vira aresta para Obsolete). Só conta como atributo o colchete que abre no começo da linha
// ou logo depois de '(' ou ',' — é a forma dos atributos em C#; assim `a[i]` e `arr[f(x)]` seguem sendo lidos.
func csAttrMask(cl string) []bool {
	mask := make([]bool, len(cl))
	depth := 0
	attr := false
	for i := 0; i < len(cl); i++ {
		switch cl[i] {
		case '[':
			if depth == 0 {
				prev := byte(0)
				for j := i - 1; j >= 0; j-- {
					if cl[j] == ' ' || cl[j] == '\t' {
						continue
					}
					prev = cl[j]
					break
				}
				attr = prev == 0 || prev == '(' || prev == ','
			}
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				attr = false
			}
		}
		mask[i] = attr && depth > 0
	}
	return mask
}

// csAfterNew: a chamada é o tipo de um `new T(` — já sai como "references", não como "calls".
func csAfterNew(cl string, i int) bool {
	j := i
	for j > 0 && (cl[j-1] == ' ' || cl[j-1] == '\t') {
		j--
	}
	if j < 3 || cl[j-3:j] != "new" {
		return false
	}
	return j == 3 || !csIdentByte(cl[j-4])
}

func csIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// csExported: público é o que sai do assembly; internal/protected/private não.
func csExported(prefix string) bool {
	return strings.Contains(prefix, "public ")
}

func csSignature(raw string) string {
	return clip(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "{")))
}

// parseCSProject lê .csproj/.sln: o arquivo vira o símbolo do projeto (kind 'project') e cada ProjectReference
// (ou cada Project do .sln) vira uma aresta "projref" para o NOME do projeto referenciado — é o que permite ao
// impact mostrar os projetos dependentes.
func parseCSProject(rel, text string) []codescan.Decl {
	base := filepath.Base(rel)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	what := "csproj " + rel
	if strings.EqualFold(filepath.Ext(base), ".sln") {
		what = "solution " + rel
	}
	lines := strings.Split(text, "\n")
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" { // sem a última linha vazia: o fim é a última linha de conteúdo
		lines = lines[:len(lines)-1]
	}
	return []codescan.Decl{{Line: 1, EndLine: len(lines), Name: name, Kind: "project", Sig: what, Exported: true}}
}

// csProjectRefs devolve as arestas "projref" do arquivo de projeto: cada ProjectReference (ou cada Project do .sln)
// aponta para o NOME do projeto referenciado, não para o caminho — é o nome que liga um projeto ao outro.
func csProjectRefs(rel, text string) []codescan.Ref {
	re := reCSProjRef
	if strings.EqualFold(filepath.Ext(filepath.Base(rel)), ".sln") {
		re = reCSlnProj
	}
	var refs []codescan.Ref
	for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
		p := strings.ReplaceAll(text[loc[2]:loc[3]], "\\", "/")
		pb := filepath.Base(p)
		ext := filepath.Ext(pb)
		if ext == "" {
			// pasta de solução (`Project("{2150E333-…}") = "SolutionItems", "SolutionItems", "…"`): aparece na mesma
			// forma de um projeto, mas o segundo campo é um nome de pasta, não um arquivo de projeto com extensão.
			continue
		}
		if target := strings.TrimSuffix(pb, ext); target != "" {
			refs = append(refs, codescan.Ref{Line: 1 + strings.Count(text[:loc[2]], "\n"), Target: target, Rel: "projref"})
		}
	}
	return refs
}

const (
	csNone = iota
	csBlockComment
	csVerbatim
	csRawString
)

// csCleanLines devolve cada linha sem comentário e sem o conteúdo das strings (aspas preservadas) e se a linha começa
// dentro de comentário de bloco ou de string multilinha. É o que impede que texto vire símbolo ou aresta.
func csCleanLines(lines []string) ([]string, []bool) {
	clean := make([]string, len(lines))
	inside := make([]bool, len(lines))
	state := csNone
	rawN := 0
	var b strings.Builder
	for n, line := range lines {
		inside[n] = state != csNone
		b.Reset()
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch state {
			case csBlockComment:
				if c == '*' && i+1 < len(line) && line[i+1] == '/' {
					state = csNone
					i++
				}
				continue
			case csVerbatim:
				if c == '"' {
					if i+1 < len(line) && line[i+1] == '"' { // "" dentro de verbatim é aspa escapada
						i++
						continue
					}
					state = csNone
					b.WriteByte('"')
				}
				continue
			case csRawString:
				if strings.HasPrefix(line[i:], strings.Repeat(`"`, rawN)) {
					state = csNone
					b.WriteByte('"')
					i += rawN - 1
				}
				continue
			}
			switch {
			case c == '/' && i+1 < len(line) && line[i+1] == '/':
				i = len(line) // o resto é comentário
			case c == '/' && i+1 < len(line) && line[i+1] == '*':
				state = csBlockComment
				i++
			case c == '"' && csQuoteRun(line, i) >= 3:
				rawN = csQuoteRun(line, i)
				state = csRawString
				b.WriteByte('"')
				i += rawN - 1
			case c == '$' && i+1 < len(line) && line[i+1] == '"' && csQuoteRun(line, i+1) >= 3:
				rawN = csQuoteRun(line, i+1)
				state = csRawString
				b.WriteByte('"')
				i += rawN
			case strings.HasPrefix(line[i:], `$@"`), strings.HasPrefix(line[i:], `@$"`):
				state = csVerbatim
				b.WriteByte('"')
				i += 2
			case c == '@' && i+1 < len(line) && line[i+1] == '"':
				state = csVerbatim
				b.WriteByte('"')
				i++
			case c == '$' && i+1 < len(line) && line[i+1] == '"':
				b.WriteByte('"')
				i = csSkipString(line, i+1)
				b.WriteByte('"')
			case c == '"' || c == '\'':
				b.WriteByte(c)
				i = csSkipString(line, i)
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		}
		clean[n] = b.String()
	}
	return clean, inside
}

func csQuoteRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '"' {
		n++
	}
	return n
}

// csSkipString devolve o índice da aspa (ou apóstrofo) que fecha a string aberta em i; sem fechamento, o fim da linha.
func csSkipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == q {
			return j
		}
	}
	return len(s) - 1
}
