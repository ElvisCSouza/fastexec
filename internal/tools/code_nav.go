// code_nav.go: tool code_outline. Lista as declarações (funções, tipos, classes, métodos, rotas) de um arquivo ou
// procura onde um símbolo é declarado numa árvore, sem índice, com os scanners de internal/codescan.
// Resposta "L<linha>: <declaração>": ~50–150 tokens em vez do arquivo inteiro por read_file.
package tools

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
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
		doc := codescan.IsDoc(args.Path)
		if lang == nil && !doc {
			return "", unsupportedErr(args.Path)
		}
		content, err := handleReadFileInner(ReadFileArgs{Host: args.Host, Path: args.Path})
		if err != nil {
			return "", fmt.Errorf("%v (on remote hosts code_outline takes a file; a directory runs only on host 'local')", err)
		}
		if doc {
			return outlineDoc("", args.Path, []byte(content), args.Symbol, max), nil
		}
		return outlineSource(args.Path, []byte(content), lang, args.Symbol, max), nil
	}

	path := expandHome(args.Path)
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		if codescan.IsDoc(path) {
			src, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			root, rel := docRepo(path)
			out := outlineDoc(root, rel, src, args.Symbol, max)
			if args.Symbol != "" && strings.HasPrefix(out, "(no declaration matching") {
				out += outlineElsewhere(path, args.Symbol, max)
			}
			return out, nil
		}
		lang := codescan.LangFor(path)
		if lang == nil {
			return "", unsupportedErr(path)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		shown, ends := outlineSpans(path, src, lang, args.Symbol)
		out := renderFileEnds(shown, max, args.Symbol, ends)
		if args.Symbol != "" && strings.HasPrefix(out, "(no declaration matching") {
			return out + outlineElsewhere(path, args.Symbol, max), nil
		}
		// O outline mostra só os cabeçalhos; o corpo da declaração mostrada é onde vivem os `x := ...`.
		// A linha "types:" fecha esse buraco sem obrigar outra leitura.
		return out + outlineTypeLine(path, src, shown, ends, max), nil
	}
	return outlineTree(path, args.Symbol, max)
}

// outlineElsewhere: o símbolo não está no arquivo pedido (o chute mais comum: o chamador, não o declarante). Procura-o
// no repositório que contém o arquivo (ancestral com .git; sem repositório, a pasta do arquivo) e devolve onde está
// declarado, poupando uma chamada de descoberta. Vazio se não achar.
func outlineElsewhere(file, symbol string, max int) string {
	root := filepath.Dir(file)
	for d := root; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			root = d
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	found, err := outlineTree(root, symbol, max)
	if err != nil || strings.HasPrefix(found, "(no declaration") {
		return ""
	}
	return "\n[FastExec] Not declared in this file; declared elsewhere in the repo (paths relative to " + root + "):\n" + found
}

func unsupportedErr(path string) error {
	return fmt.Errorf("unsupported file type %q (supported: %s, %s)", filepath.Ext(path), codescan.Supported, codescan.DocSupported)
}

// outlineDoc lista as seções de um Markdown (os nós do grafo de documentação) e, se houver referência quebrada,
// acrescenta a linha "docs: N broken refs". root vazio (host remoto, ou arquivo fora de repositório) mostra só as
// seções: sem índice não há âncora para conferir.
func outlineDoc(root, rel string, src []byte, symbol string, max int) string {
	decls, broken := graph.DocOutline(root, rel, src)
	ends := make(map[int]int, len(decls))
	for _, d := range decls {
		if d.EndLine > d.Line {
			ends[d.Line] = d.EndLine
		}
	}
	out := renderFileEnds(filterDecls(decls, symbol), max, symbol, ends)
	if broken != "" {
		out += "\n" + broken
	}
	return out
}

// docRepo devolve a raiz do repositório e o caminho relativo de um Markdown; fora de repositório, o caminho inteiro.
func docRepo(path string) (string, string) {
	if root, ok := graph.ResolveRepoRoot(path); ok {
		if rel, err := filepath.Rel(root, path); err == nil {
			return root, filepath.ToSlash(rel)
		}
		return root, filepath.ToSlash(path)
	}
	return "", filepath.ToSlash(path)
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
	return scanFileFor(path, lang, "")
}

// scanFileFor: com symbol, o arquivo que não contém o texto (sem diferenciar maiúsculas) nem é analisado: filterDecls
// só aceitaria declarações cujo nome o contém, então o resultado é o mesmo, sem o custo do scanner (CPU da árvore).
func scanFileFor(path string, lang *codescan.Lang, symbol string) ([]codescan.Decl, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if symbol != "" && !bytes.Contains(bytes.ToLower(src), []byte(strings.ToLower(symbol))) {
		return nil, nil
	}
	decls := outlineDecls(path, src, lang)
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

// outlineDecls: declarações de um arquivo para o code_outline. C#/.csproj/.sln têm parser próprio no internal/graph
// (namespace, propriedades, campos, projeto); as demais linguagens seguem nos scanners do codescan.
func outlineDecls(path string, src []byte, lang *codescan.Lang) []codescan.Decl {
	if graph.IsCSharp(path) {
		if decls, ok := graph.ParseCSharp(path, src); ok {
			return decls
		}
	}
	decls, _ := codescan.Scan(src, lang)
	return decls
}

// outlineSource renders one file as "L<start>-L<end>: <declaration>". The end
// comes from the scanner (balanced braces, or indentation in Python); when the
// scanner has none, the line before the next declaration is used.
func outlineSource(path string, src []byte, lang *codescan.Lang, symbol string, max int) string {
	decls, ends := outlineSpans(path, src, lang, symbol)
	return renderFileEnds(decls, max, symbol, ends)
}

// outlineSpans devolve as declarações a mostrar e o fim (1-based) de cada uma.
//
// O fim é calculado sobre TODAS as declarações do arquivo, e só depois o filtro por símbolo é aplicado:
// filtrar primeiro faria o fim de cada declaração esticar até a próxima que casa, engolindo o arquivo.
func outlineSpans(path string, src []byte, lang *codescan.Lang, symbol string) ([]codescan.Decl, map[int]int) {
	all := outlineDecls(path, src, lang)
	var ends map[int]int
	if graph.IsCSharp(path) {
		ends = map[int]int{} // o parser de C# carimba o fim de cada bloco; o palpite pelo vizinho só piora
	} else {
		ends = declEnds(src, all)
	}
	for _, d := range all {
		if d.EndLine > d.Line {
			ends[d.Line] = d.EndLine
		}
	}
	return filterDecls(all, symbol), ends
}

func declEnds(src []byte, decls []codescan.Decl) map[int]int {
	lines := strings.Split(string(src), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	ends := make(map[int]int, len(decls))
	for i, d := range decls {
		end := len(lines)
		if i+1 < len(decls) {
			end = decls[i+1].Line - 1
		}
		for end > d.Line {
			t := strings.TrimSpace(lines[end-1])
			if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
				end--
				continue
			}
			break
		}
		ends[d.Line] = end
	}
	return ends
}

func renderFile(decls []codescan.Decl, max int, symbol string) string {
	return renderFileEnds(decls, max, symbol, nil)
}

func renderFileEnds(decls []codescan.Decl, max int, symbol string, ends map[int]int) string {
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
		if e, ok := ends[d.Line]; ok && e > d.Line {
			fmt.Fprintf(&b, "L%d-L%d: %s\n", d.Line, e, d.Sig)
		} else {
			fmt.Fprintf(&b, "L%d: %s\n", d.Line, d.Sig)
		}
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
		if codescan.LangFor(p) == nil || isNoiseFile(d.Name()) || codescan.IsNoiseCSharp(p) {
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
				decls, err := scanFileFor(files[i], codescan.LangFor(files[i]), symbol)
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
