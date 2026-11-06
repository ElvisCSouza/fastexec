// code_nav.go: tool code_outline. Lista as declarações (funções, tipos, classes, métodos, rotas) de um arquivo ou
// procura onde um símbolo é declarado numa árvore, com scanners de linha em Go puro (sem CGO, sem índice).
// Resposta "L<linha>: <declaração>": ~50–150 tokens em vez do arquivo inteiro por read_file.
package tools

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

type CodeOutlineArgs struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Max    int    `json:"max"`
}

const (
	outlineDefaultMax  = 200
	outlineMaxLineLen  = 400     // linha maior que isso é minificada/gerada: ignorada
	outlineMaxFileSize = 2 << 20 // arquivo maior que 2 MB não é varrido na árvore
	outlineMaxFiles    = 20000
	outlineSigMax      = 160
	utf8BOM            = "\xef\xbb\xbf"
)

// declaration é uma declaração achada: linha 1-based, nome (para o filtro de símbolo) e a assinatura enxuta.
type declaration struct {
	Line int
	Name string
	Sig  string
}

type langSpec struct {
	rules       []*regexp.Regexp // cada regra tem o grupo (?P<name>...)
	routes      bool             // procura também rotas HTTP ("/path")
	hashComment bool             // '#' inicia comentário de linha
	slashCommnt bool             // '//' e blocos /* */
}

var (
	reGoFunc = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?(?P<name>\w+)`)
	reGoType = regexp.MustCompile(`^type\s+(?P<name>\w+)\b`)

	reJSFunc   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*(?P<name>[\w$]+)`)
	reJSClass  = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(?P<name>[\w$]+)`)
	reJSIface  = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?interface\s+(?P<name>[\w$]+)`)
	reJSType   = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?type\s+(?P<name>[\w$]+)\s*(?:<[^=]*>)?\s*=`)
	reJSArrow  = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(?P<name>[\w$]+)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:function\b|(?:\([^)]*\)|[\w$]+)\s*(?::\s*[^=]+?)?\s*=>)`)
	reJSMethod = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static|readonly|async|override|abstract|get|set)\s+)*(?P<name>#?[\w$]+)\s*(?:<[^>]*>)?\s*\([^)]*\)\s*(?::\s*[^{=;]+)?\s*\{\s*$`)

	rePyDef   = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(?P<name>\w+)`)
	rePyClass = regexp.MustCompile(`^\s*class\s+(?P<name>\w+)`)

	reShFunc    = regexp.MustCompile(`^\s*function\s+(?P<name>[\w.:-]+)`)
	reShParens  = regexp.MustCompile(`^\s*(?P<name>[A-Za-z_][\w.:-]*)\s*\(\)\s*(?:\{.*)?$`)
	rePSFunc    = regexp.MustCompile(`(?i)^\s*function\s+(?P<name>[\w-]+)`)
	reHTTPRoute = regexp.MustCompile(`(?:\.|@\w+\.)(?i:get|post|put|patch|delete|options|head|all|route|handle|handlefunc)\(\s*["'` + "`" + `](?P<name>/[^"'` + "`" + `]*)`)

	// palavras de controle que o regex de método confundiria com declaração ("if (x) {")
	jsNotMethod = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true, "return": true, "else": true, "do": true, "with": true}

	langGo  = &langSpec{rules: []*regexp.Regexp{reGoFunc, reGoType}, routes: true, slashCommnt: true}
	langJS  = &langSpec{rules: []*regexp.Regexp{reJSFunc, reJSClass, reJSIface, reJSType, reJSArrow, reJSMethod}, routes: true, slashCommnt: true}
	langPy  = &langSpec{rules: []*regexp.Regexp{rePyDef, rePyClass}, routes: true, hashComment: true}
	langSh  = &langSpec{rules: []*regexp.Regexp{reShFunc, reShParens}, hashComment: true}
	langPS1 = &langSpec{rules: []*regexp.Regexp{rePSFunc}, hashComment: true}

	langByExt = map[string]*langSpec{
		".go": langGo,
		".ts": langJS, ".tsx": langJS, ".mts": langJS, ".cts": langJS,
		".js": langJS, ".jsx": langJS, ".mjs": langJS, ".cjs": langJS,
		".py": langPy,
		".sh": langSh, ".bash": langSh, ".zsh": langSh,
		".ps1": langPS1, ".psm1": langPS1,
	}
)

// HandleCodeOutline atende a tool: arquivo → outline (filtrado por symbol, se houver); diretório (só host local) →
// declarações de todos os arquivos suportados, pulando diretórios de build/dependência.
func HandleCodeOutline(args CodeOutlineArgs) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("parameter 'path' is required")
	}
	max := args.Max
	if max <= 0 {
		max = outlineDefaultMax
	}
	target, err := ResolveTargetHost(args.Host, args.Path)
	if err != nil {
		return "", err
	}

	if !target.IsLocal {
		lang := langFor(args.Path)
		if lang == nil {
			return "", unsupportedErr(args.Path)
		}
		content, err := HandleReadFile(ReadFileArgs{Host: args.Host, Path: args.Path})
		if err != nil {
			return "", fmt.Errorf("%v (on remote hosts code_outline takes a file; a directory runs only on host 'local')", err)
		}
		decls := filterDecls(scanDecls(strings.NewReader(content), lang), args.Symbol)
		return renderFile(decls, max, args.Symbol), nil
	}

	path := expandHome(args.Path)
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		lang := langFor(path)
		if lang == nil {
			return "", unsupportedErr(path)
		}
		decls, err := scanFile(path, lang)
		if err != nil {
			return "", err
		}
		return renderFile(filterDecls(decls, args.Symbol), max, args.Symbol), nil
	}
	return outlineTree(path, args.Symbol, max)
}

func unsupportedErr(path string) error {
	return fmt.Errorf("unsupported file type %q (supported: .go, .ts/.tsx/.js/.jsx/.mjs/.cjs, .py, .sh/.bash, .ps1)", filepath.Ext(path))
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func langFor(path string) *langSpec {
	name := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(name, ".min.js") {
		return nil
	}
	return langByExt[strings.ToLower(filepath.Ext(name))]
}

func scanFile(path string, lang *langSpec) ([]declaration, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return scanDecls(f, lang), nil
}

// scanDecls varre linha a linha: pula comentários e linhas gigantes; a primeira regra que casar define a declaração.
func scanDecls(r io.Reader, lang *langSpec) []declaration {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	var (
		out       []declaration
		inBlock   bool
		lineNo    int
		firstLine = true
	)
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if firstLine {
			line = strings.TrimPrefix(line, utf8BOM)
			firstLine = false
		}
		if len(line) > outlineMaxLineLen {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if lang.slashCommnt {
			if inBlock {
				if strings.Contains(trimmed, "*/") {
					inBlock = false
				}
				continue
			}
			if strings.HasPrefix(trimmed, "/*") {
				inBlock = !strings.Contains(trimmed[2:], "*/")
				continue
			}
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "* ") {
				continue
			}
		}
		if lang.hashComment && strings.HasPrefix(trimmed, "#") {
			continue
		}
		if d, ok := matchDecl(line, trimmed, lang); ok {
			d.Line = lineNo
			out = append(out, d)
		}
	}
	return out
}

func matchDecl(line, trimmed string, lang *langSpec) (declaration, bool) {
	for _, re := range lang.rules {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[re.SubexpIndex("name")]
		if re == reJSMethod && jsNotMethod[name] {
			continue
		}
		return declaration{Name: name, Sig: signature(trimmed)}, true
	}
	if lang.routes {
		if m := reHTTPRoute.FindStringSubmatch(line); m != nil {
			return declaration{Name: m[reHTTPRoute.SubexpIndex("name")], Sig: signature(trimmed)}, true
		}
	}
	return declaration{}, false
}

// signature tira a chave de abertura do fim e corta assinaturas longas.
func signature(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "{"))
	if len(s) > outlineSigMax {
		s = s[:outlineSigMax] + "…"
	}
	return s
}

func filterDecls(decls []declaration, symbol string) []declaration {
	if symbol == "" {
		return decls
	}
	q := strings.ToLower(symbol)
	var out []declaration
	for _, d := range decls {
		if strings.Contains(strings.ToLower(d.Name), q) {
			out = append(out, d)
		}
	}
	return out
}

func renderFile(decls []declaration, max int, symbol string) string {
	if len(decls) == 0 {
		if symbol != "" {
			return fmt.Sprintf("(no declaration matching %q)", symbol)
		}
		return "(no declarations found)"
	}
	var b strings.Builder
	for i, d := range decls {
		if i == max {
			fmt.Fprintf(&b, "[FastExec Guard] %d more declarations; pass 'symbol' or a higher 'max'.\n", len(decls)-max)
			break
		}
		fmt.Fprintf(&b, "L%d: %s\n", d.Line, d.Sig)
	}
	return strings.TrimRight(b.String(), "\n")
}

type fileDecls struct {
	rel   string
	decls []declaration
}

// outlineTree varre a árvore em paralelo (um worker por CPU) e devolve "caminho:L<n>: assinatura", ordenado por caminho.
func outlineTree(root, symbol string, max int) (string, error) {
	var files []string
	capped := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // diretório ilegível não derruba a busca
		}
		if d.IsDir() {
			if p != root && isNoiseDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if langFor(p) == nil || isNoiseFile(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > outlineMaxFileSize {
			return nil
		}
		if len(files) >= outlineMaxFiles {
			capped = true
			return filepath.SkipAll
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}

	results := make([]fileDecls, len(files))
	var wg sync.WaitGroup
	next := make(chan int)
	workers := runtime.NumCPU()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				decls, err := scanFile(files[i], langFor(files[i]))
				if err != nil {
					continue
				}
				rel, rerr := filepath.Rel(root, files[i])
				if rerr != nil {
					rel = files[i]
				}
				results[i] = fileDecls{rel: filepath.ToSlash(rel), decls: filterDecls(decls, symbol)}
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].rel < results[j].rel })
	var b strings.Builder
	total, shown := 0, 0
	for _, r := range results {
		for _, d := range r.decls {
			total++
			if shown < max {
				fmt.Fprintf(&b, "%s:L%d: %s\n", r.rel, d.Line, d.Sig)
				shown++
			}
		}
	}
	if total == 0 {
		if symbol != "" {
			return fmt.Sprintf("(no declaration matching %q in %d files)", symbol, len(files)), nil
		}
		return fmt.Sprintf("(no declarations found in %d files)", len(files)), nil
	}
	if total > shown {
		fmt.Fprintf(&b, "[FastExec Guard] %d more declarations; narrow with 'symbol', a subfolder or a higher 'max'.\n", total-shown)
	}
	if capped {
		fmt.Fprintf(&b, "[FastExec Guard] stopped after %d files; point 'path' at a subfolder.\n", outlineMaxFiles)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
