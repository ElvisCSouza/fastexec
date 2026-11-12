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
	cppRaw       bool             // strings cruas R"delim(...)delim" multilinha (C++)
	imports      []*regexp.Regexp // grupo 1 = módulo importado
	importNames  *regexp.Regexp   // grupo 1 = lista de nomes importados ("a, b as c")
	// custom substitui a varredura por linha quando as regras não dão conta: CSS/HTML/Razor têm
	// vários seletores por linha, atributos no meio da tag e aninhamento. Como é campo de Lang,
	// quem consome (code_outline, grafo) usa a mesma extração para a mesma extensão.
	custom func(src []byte) ([]Decl, []Ref)
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
		"require": true, "export": true, "func": true, "go": true, "defer": true, "case": true, "new": true,
		"defined": true, "decltype": true, "alignof": true, "alignas": true, "static_assert": true, "noexcept": true,
		"__declspec": true, "__attribute__": true, "operator": true, "throw": true, "delete": true}

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

	// C e C++: definições (não protótipos) de funções e métodos, classes/structs/unions/enums, #define, using.
	// Função no nível do arquivo: linha sem recuo, tipo + nome + '(' e sem ';' no fim (Class::Metodo vira método).
	// Recuada (método dentro da classe): exige tipo antes do nome e ')' no fim, sem '=' nem ';'.
	langC = &Lang{Name: "c", slashComment: true, cppRaw: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*#\s*define\s+(?P<name>\w+)`), fixed("macro")},
			{regexp.MustCompile(`^\s*(?:template\s*<.*>\s*)?(?:typedef\s+)?(?:class|struct|union|enum(?:\s+(?:class|struct))?)\s+(?:__declspec\([^)]*\)\s+|alignas\([^)]*\)\s+|[A-Z_]{3,}\s+)*(?P<name>\w+)\s*(?:final\s*)?(?::[^;{]*)?\{?\s*$`), cTypeKind},
			{regexp.MustCompile(`^\s*using\s+(?P<name>\w+)\s*=`), fixed("type")},
			{regexp.MustCompile(`^(?:template\s*<.*>\s*)?(?:[\w:<>,~\[\]]+[*&]*\s+)*[\w:<>,~\[\]]+(?:\s+|\s*[*&]+\s*)(?:\w+::)*(?P<name>~?\w+)\s*\((?:[^;]*|.*\)\s*(?:const\s*)?(?:noexcept\s*)?\{.*)$`), cFuncKind},
			{regexp.MustCompile(`^(?:\w+::)+(?P<name>~?\w+)\s*\([^;]*$`), fixed("method")}, // construtor/destrutor fora da classe
			{regexp.MustCompile(`^\s+(?:(?:virtual|static|inline|explicit|constexpr|friend|STDMETHODCALLTYPE|WINAPI|CALLBACK|__stdcall)\s+)*(?:[\w:<>,*&\[\]]+\s+)+[*&]*\s*(?P<name>~?\w+)\s*\([^;=]*\)\s*(?:const\s*)?(?:noexcept\s*)?(?:override\s*)?(?:final\s*)?(?:\{.*|//.*)?$`), cFuncKind},
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*#\s*include\s*[<"]([^>"]+)[>"]`)},
	}

	// C#: tipos (class/struct/interface/record/enum) e métodos/construtores com modificador de acesso. Expressão
	// "=> x;" e propriedades ficam de fora; o fim do bloco sai das chaves.
	langCS = &Lang{Name: "cs", slashComment: true, routes: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|abstract|sealed|partial|readonly|unsafe|new|file|ref)\s+)*(?:class|struct|interface|record(?:\s+(?:struct|class))?|enum)\s+(?P<name>\w+)`), kwKind},
			{regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|virtual|override|abstract|async|sealed|extern|unsafe|new|partial|readonly)\s+)+[\w<>\[\],.?]+(?:<[^()]*>)?\??\s+(?P<name>\w+)\s*(?:<[^>()]*>)?\s*\([^;]*$`), fixed("method")},
			{regexp.MustCompile(`^\s*(?:public|private|protected|internal|static)\s+(?P<name>[A-Z]\w*)\s*\([^;]*$`), fixed("method")}, // construtor
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*using\s+(?:static\s+)?([\w.]+)\s*;`)},
	}

	// Java: tipos e métodos com modificador ou tipo de retorno; anotações (@Override) ficam na linha anterior.
	langJava = &Lang{Name: "java", slashComment: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:@\w+\s+)*(?:(?:public|private|protected|static|final|abstract|sealed|non-sealed|strictfp)\s+)*(?:class|interface|enum|record|@interface)\s+(?P<name>\w+)`), kwKind},
			{regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)+(?:<[^>]+>\s+)?[\w<>\[\],.?]+\s+(?P<name>\w+)\s*\([^;]*$`), fixed("method")},
			{regexp.MustCompile(`^\s*(?:public|private|protected)\s+(?P<name>[A-Z]\w*)\s*\([^;]*$`), fixed("method")}, // construtor
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*import\s+(?:static\s+)?([\w.]+)`)},
	}

	// Kotlin: class/interface/object (inclusive data/sealed/enum class), fun (inclusive extensão T.nome) e typealias.
	langKt = &Lang{Name: "kt", slashComment: true, tripleQuote: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:(?:public|private|protected|internal|open|abstract|sealed|data|enum|annotation|inner|value|inline|companion|actual|expect|fun)\s+)*(?:class|interface|object)\s+(?P<name>\w+)`), kwKind},
			{regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|open|abstract|override|inline|suspend|operator|infix|tailrec|external|actual|expect|final)\s+)*fun\s+(?:<[^>]*>\s*)?(?:[\w.<>?, ]+\.)?(?P<name>\w+)\s*\(`), indentedKind("func", "method")},
			{regexp.MustCompile(`^\s*(?:(?:public|private|internal)\s+)?typealias\s+(?P<name>\w+)`), fixed("type")},
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`^\s*import\s+([\w.]+)`)},
	}

	// Zig: fn (pub/export/extern/inline; dentro de struct vira método), const X = struct/enum/union/opaque/error,
	// e blocos test "nome". Fim do bloco pelas chaves.
	langZig = &Lang{Name: "zig", slashComment: true,
		rules: []rule{
			{regexp.MustCompile(`^\s*(?:pub\s+)?(?:(?:export|inline|noinline)\s+|extern\s+(?:"\w+"\s+)?)*fn\s+(?P<name>[A-Za-z_]\w*)\s*\(`), indentedKind("func", "method")},
			{regexp.MustCompile(`^\s*(?:pub\s+)?const\s+(?P<name>[A-Za-z_]\w*)\s*(?::[^=]+)?=\s*(?:extern\s+|packed\s+)?(?:struct|enum|union|opaque|error)\b`), zigTypeKind},
			{regexp.MustCompile(`^\s*test\s+"(?P<name>[^"]+)"`), fixed("test")},
		},
		imports: []*regexp.Regexp{regexp.MustCompile(`@import\(\s*"([^"]+)"`)},
	}

	langByExt = map[string]*Lang{
		".zig": langZig,
		".cs":  langCS, ".java": langJava, ".kt": langKt, ".kts": langKt,
		".c": langC, ".h": langC, ".cc": langC, ".cpp": langC, ".cxx": langC, ".hpp": langC, ".hh": langC, ".hxx": langC, ".inl": langC,
		".go": langGo,
		".ts": langJS, ".tsx": langTSX, ".mts": langJS, ".cts": langJS,
		".js": langJS, ".jsx": langTSX, ".mjs": langJS, ".cjs": langJS,
		".py": langPy,
		".sh": langSh, ".bash": langSh, ".zsh": langSh,
		".ps1": langPS1, ".psm1": langPS1,
		".css": langCSS, ".scss": langCSS, ".less": langCSS,
		".html": langHTML, ".htm": langHTML,
		".razor": langRazor, ".cshtml": langRazor,
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
const Supported = ".go, .ts/.tsx/.js/.jsx/.mjs/.cjs, .py, .sh/.bash, .ps1, .c/.h/.cpp/.cc/.hpp, .cs, .java, .kt/.kts, .zig, .css/.scss/.less, .html/.htm, .razor/.cshtml"

// zigTypeKind: struct, enum, union, opaque ou error (conjunto de erros).
func zigTypeKind(line string) string {
	for _, k := range []string{"struct", "enum", "union", "opaque", "error"} {
		if strings.Contains(line, k) {
			return k
		}
	}
	return "type"
}

// kwKind devolve a palavra-chave de tipo da linha (class, struct, interface, record, enum, object).
func kwKind(line string) string {
	t := " " + strings.TrimSpace(line) + " "
	for _, k := range []string{"record", "enum", "interface", "struct", "object", "class"} {
		if strings.Contains(t, " "+k+" ") {
			return k
		}
	}
	return "class"
}

// cKeywords: palavra que abre a linha e mostra que ela é comando, não declaração ("return f(x)", "else g(").
var cKeywords = map[string]bool{"return": true, "else": true, "delete": true, "new": true, "case": true, "throw": true,
	"goto": true, "co_return": true, "co_await": true, "co_yield": true, "if": true, "for": true, "while": true, "switch": true,
	"do": true, "sizeof": true, "using": true, "typedef": true, "namespace": true, "operator": true, "static_assert": true,
	"decltype": true, "defined": true, "#if": true, "#elif": true}

func cFuncKind(line string) string {
	t := strings.TrimSpace(line)
	first := t
	if i := strings.IndexAny(t, " \t("); i >= 0 {
		first = t[:i]
	}
	// ':' e ',' no começo = lista de inicialização de construtor (": a(x), b(y)")
	if cKeywords[first] || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ":") || strings.HasPrefix(t, ",") || strings.Contains(t, "operator") {
		return ""
	}
	pre := t
	if i := strings.Index(t, "("); i >= 0 {
		pre = t[:i]
	}
	if strings.Contains(pre, "=") || strings.Contains(pre, "(") || strings.Contains(pre, ".") || strings.Contains(pre, "->") {
		return ""
	}
	if strings.Contains(pre, "::") || (len(line) > 0 && (line[0] == ' ' || line[0] == '\t')) {
		return "method"
	}
	return "func"
}

func cTypeKind(line string) string {
	t := strings.TrimSpace(line)
	if strings.HasSuffix(t, ";") {
		return "" // declaração adiantada (struct X;)
	}
	for _, k := range []string{"enum", "union", "class", "struct"} {
		if strings.Contains(" "+t+" ", " "+k+" ") {
			return k
		}
	}
	return "type"
}

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
	case "c":
		return !strings.HasPrefix(strings.TrimSpace(line), "static ")
	case "cs", "java":
		return strings.Contains(line, "public ")
	case "kt":
		return !strings.Contains(line, "private ") && !strings.Contains(line, "internal ")
	case "zig":
		return strings.HasPrefix(strings.TrimSpace(line), "pub ") || strings.Contains(line, "export ")
	}
	return true
}
