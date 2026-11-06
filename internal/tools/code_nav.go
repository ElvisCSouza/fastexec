// code_nav.go: tool code_outline. Lista as declarações (funções, tipos, classes, métodos, rotas) de um arquivo ou
// procura onde um símbolo é declarado numa árvore, sem índice, com os scanners de internal/codescan.
// Resposta "L<linha>: <declaração>": ~50–150 tokens em vez do arquivo inteiro por read_file.
package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"homelab/fastexec/internal/codescan"
)

type CodeOutlineArgs struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Max    int    `json:"max"`
}

const (
	outlineDefaultMax  = 200
	outlineMaxFileSize = 2 << 20 // arquivo maior que 2 MB não é varrido na árvore
	outlineMaxFiles    = 20000
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
		lang := codescan.LangFor(args.Path)
		if lang == nil {
			return "", unsupportedErr(args.Path)
		}
		content, err := HandleReadFile(ReadFileArgs{Host: args.Host, Path: args.Path})
		if err != nil {
			return "", fmt.Errorf("%v (on remote hosts code_outline takes a file; a directory runs only on host 'local')", err)
		}
		decls, _ := codescan.Scan([]byte(content), lang)
		return renderFile(filterDecls(decls, args.Symbol), max, args.Symbol), nil
	}

	path := expandHome(args.Path)
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		lang := codescan.LangFor(path)
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
	return fmt.Errorf("unsupported file type %q (supported: %s)", filepath.Ext(path), codescan.Supported)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func scanFile(path string, lang *codescan.Lang) ([]codescan.Decl, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decls, _ := codescan.Scan(src, lang)
	return decls, nil
}

func filterDecls(decls []codescan.Decl, symbol string) []codescan.Decl {
	if symbol == "" {
		return decls
	}
	q := strings.ToLower(symbol)
	var out []codescan.Decl
	for _, d := range decls {
		if strings.Contains(strings.ToLower(d.Name), q) {
			out = append(out, d)
		}
	}
	return out
}

func renderFile(decls []codescan.Decl, max int, symbol string) string {
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
	decls []codescan.Decl
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
		if codescan.LangFor(p) == nil || isNoiseFile(d.Name()) {
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
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				decls, err := scanFile(files[i], codescan.LangFor(files[i]))
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
