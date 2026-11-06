// Package codescan: scanners de linha em Go puro (sem CGO) que extraem declarações e referências de código-fonte.
// Usado pelo code_outline (sem índice) e pelo grafo de código (internal/graph) nas linguagens fora do Go.
// Baseado em nome, sem resolução de tipos. Este arquivo: regras por linguagem; scan.go: a varredura.
package codescan

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Lang reúne as regras de uma linguagem.
type Lang struct {
	Name         string
	rules        []rule
	routes       bool             // procura rotas HTTP ("/caminho")
	hashComment  bool             // '#' inicia comentário
	slashComment bool             // '//' e /* */
	tripleQuote  bool             // strings """ e ''' multilinha (Python)
	backtick     bool             // strings `...` multilinha (JS template, Go raw)
	backtickEsc  bool             // '\' escapa dentro da crase (JS template); em Go a crase é crua, sem escape
	indentBlocks bool             // fim de bloco por indentação (Python); senão por chaves
	imports      []*regexp.Regexp // grupo 1 = módulo importado
	importNames  *regexp.Regexp   // grupo 1 = lista de nomes importados ("a, b as c")
}

type rule struct {
	re   *regexp.Regexp // grupo (?P<name>...)
	kind func(line string) string
}

func fixed(k string) func(string) string { return func(string) string { return k } }

func indentedKind(top, nested string) func(string) string {
	return func(line string) string {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			return nested
		}
		return top
	}
}

func goFuncKind(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "func (") {
		return "method"
	}
	return "func"
}

func goTypeKind(line string) string {
	switch {
	case strings.Contains(line, " struct"):
		return "struct"
	case strings.Contains(line, " interface"):
		return "interface"
	}
	return "type"
}

var (
	reHTTPRoute = regexp.MustCompile(`(?:\.|@\w+\.)(?i:get|post|put|patch|delete|options|head|all|route|handle|handlefunc)\(\s*["'` + "`" + `](?P<name>/[^"'` + "`" + `]*)`)
	reCall      = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*\(`) // sem prefixo: chamadas aninhadas "a(b(" casam as duas

	// palavras que o regex de método ou de chamada confundiria com nome ("if (x) {", "return (a)")
	notAName = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true,
		"return": true, "else": true, "do": true, "with": true, "def": true, "class": true, "elif": true, "in": true,
		"not": true, "and": true, "or": true, "lambda": true, "typeof": true, "sizeof": true, "try": true,
		"except": true, "assert": true, "yield": true, "await": true, "async": true, "import": true, "from": true,
		"require": true, "export": true, "func": true, "go": true, "defer": true, "case": true, "new": true}

	langGo = &Lang{Name: "go", slashComment: true, backtick: true, routes: true, rules: []rule{
		{regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?(?P<name>\w+)`), goFuncKind},
		{regexp.MustCompile(`^type\s+(?P<name>\w+)\b`), goTypeKind},
	}}

	langJS = &Lang{Name: "js", slashComment: true, backtick: true, backtickEsc: true, routes: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*(?P<name>[\w$]+)`), fixed("func")},
			{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(?P<name>[\w$]+)`), fixed("class")},
			{regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?interface\s+(?P<name>[\w$]+)`), fixed("interface")},
			{regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?type\s+(?P<name>[\w$]+)\s*(?:<[^=]*>)?\s*=`), fixed("type")},
			{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(?P<name>[\w$]+)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:function\b|(?:\([^)]*\)|[\w$]+)\s*(?::\s*[^=]+?)?\s*=>)`), fixed("func")},
			{regexp.MustCompile(`^\s+(?:(?:public|private|protected|static|readonly|async|override|abstract|get|set)\s+)*(?P<name>#?[\w$]+)\s*(?:<[^>]*>)?\s*\([^)]*\)\s*(?::\s*[^{=;]+)?\s*\{\s*$`), fixed("method")},
		},
		imports: []*regexp.Regexp{
			regexp.MustCompile(`\bfrom\s+['"]([^'"]+)['"]`),
			regexp.MustCompile(`\brequire\(\s*['"]([^'"]+)['"]`),
			regexp.MustCompile(`\bimport\(\s*['"]([^'"]+)['"]`),
			regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`),
		},
		importNames: regexp.MustCompile(`^\s*import\s+(?:type\s+)?(?:[\w$]+\s*,\s*)?\{([^}]*)\}`),
	}

	langPy = &Lang{Name: "py", hashComment: true, tripleQuote: true, indentBlocks: true, routes: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:async\s+)?def\s+(?P<name>\w+)`), indentedKind("func", "method")},
			{regexp.MustCompile(`^\s*class\s+(?P<name>\w+)`), fixed("class")},
		},
		imports: []*regexp.Regexp{
			regexp.MustCompile(`^\s*from\s+([\w.]+)\s+import\b`),
			regexp.MustCompile(`^\s*import\s+([\w.]+)`),
		},
		importNames: regexp.MustCompile(`^\s*from\s+[\w.]+\s+import\s+\(?([^)#]+)`),
	}

	langSh = &Lang{Name: "sh", hashComment: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*function\s+(?P<name>[\w.:-]+)`), fixed("func")},
			{regexp.MustCompile(`^\s*(?P<name>[A-Za-z_][\w.:-]*)\s*\(\)\s*(?:\{.*)?$`), fixed("func")},
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*(?:source|\.)\s+([^\s;|&]+)`)},
	}

	langPS1 = &Lang{Name: "ps1", hashComment: true,
		rules:   []rule{{regexp.MustCompile(`(?i)^\s*function\s+(?P<name>[\w-]+)`), fixed("func")}},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*\.\s+([^\s;|&]+)`), regexp.MustCompile(`(?i)\bImport-Module\s+([^\s;|&]+)`)},
	}

	langByExt = map[string]*Lang{
		".go": langGo,
		".ts": langJS, ".tsx": langJS, ".mts": langJS, ".cts": langJS,
		".js": langJS, ".jsx": langJS, ".mjs": langJS, ".cjs": langJS,
		".py": langPy,
		".sh": langSh, ".bash": langSh, ".zsh": langSh,
		".ps1": langPS1, ".psm1": langPS1,
	}
)

// LangFor devolve a linguagem pela extensão; nil para tipo não suportado ou arquivo minificado.
func LangFor(path string) *Lang {
	name := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(name, ".min.js") {
		return nil
	}
	return langByExt[strings.ToLower(filepath.Ext(name))]
}

// Supported lista as extensões aceitas (para mensagens de erro).
const Supported = ".go, .ts/.tsx/.js/.jsx/.mjs/.cjs, .py, .sh/.bash, .ps1"

func exported(l *Lang, kind, name, line string) bool {
	switch l.Name {
	case "go":
		r := []rune(name)
		return len(r) > 0 && unicode.IsUpper(r[0])
	case "js":
		if kind == "method" {
			return !strings.HasPrefix(name, "#") && !strings.Contains(line, "private ")
		}
		return strings.HasPrefix(strings.TrimSpace(line), "export")
	case "py":
		return !strings.HasPrefix(name, "_")
	}
	return true
}
