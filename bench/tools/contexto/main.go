// contexto: oráculos em go/ast para a suíte de recuperação de contexto do bench (estrato 'contexto').
// Nada de heurística textual: tudo sai da árvore sintática, então o gabarito é estável entre execuções.
//
// Uso:
//
//	contexto testcallers <raiz> <alvo>        arquivo:TestNome (chamada direta ou 1 nível indireto), um por linha
//	contexto ripple <raiz> <alvo>             arquivos .go não-teste com chamada direta ao alvo, um por linha
//	contexto traceorigin <raiz> <trace>       arquivo:linha do primeiro frame de panic sob a raiz
//	contexto commentrefs <raiz> <rel> <linha> símbolos exportados do arquivo citados no comentário daquela linha
//	contexto pickcomment <raiz> <min> <max>   arquivo:linha do 1º comentário (TODO/Deprecated de preferência)
//	                                          que cita entre min e max símbolos exportados do próprio arquivo
//
// A correspondência é por nome simples (como em tools/callers): métodos entram pelo nome do método,
// pacotes diferentes com o mesmo nome de função contam juntos. Os nomes são os únicos estáveis o
// bastante para o modelo poder responder sem saber o pacote.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type site struct {
	file string // caminho relativo à raiz, com '/'
	fn   string // função que contém o sítio (métodos como Tipo.método)
	test bool   // o arquivo é _test.go
}

type fnKey struct{ file, fn string }

type repo struct {
	callsByCallee map[string]map[site]bool  // nome do alvo -> sítios que o chamam diretamente
	calleesByFn   map[fnKey]map[string]bool // função -> nomes que ela chama
}

func scanRepo(root string) (*repo, error) {
	r := &repo{callsByCallee: map[string]map[site]bool{}, calleesByFn: map[fnKey]map[string]bool{}}
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		isTest := strings.HasSuffix(rel, "_test.go")
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				t := fd.Recv.List[0].Type
				if s, ok := t.(*ast.StarExpr); ok {
					t = s.X
				}
				if id, ok := t.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
			}
			k := fnKey{rel, name}
			if r.calleesByFn[k] == nil {
				r.calleesByFn[k] = map[string]bool{}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var callee string
				switch fn := c.Fun.(type) {
				case *ast.Ident:
					callee = fn.Name
				case *ast.SelectorExpr:
					callee = fn.Sel.Name
				}
				if callee == "" {
					return true
				}
				r.calleesByFn[k][callee] = true
				if r.callsByCallee[callee] == nil {
					r.callsByCallee[callee] = map[site]bool{}
				}
				r.callsByCallee[callee][site{rel, name, isTest}] = true
				return true
			})
		}
		return nil
	})
	return r, err
}

func mustScan(root string) *repo {
	r, err := scanRepo(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro ao varrer", root, ":", err)
		os.Exit(2)
	}
	return r
}

// callersOf devolve, por nome, os nomes de função que chamam o alvo (para o salto de 1 nível).
func (r *repo) callerNames(target string) map[string]bool {
	out := map[string]bool{}
	for s := range r.callsByCallee[target] {
		out[s.fn] = true
	}
	return out
}

// testCallers devolve os testes que alcançam o alvo: chamada direta ou via uma função intermediária
// (um nível), inclusive quando o intermediário vive em arquivo não-teste.
func (r *repo) testCallers(target string) []string {
	targetCallers := r.callerNames(target)
	var out []string
	for k, callees := range r.calleesByFn {
		if !k.fileIsTest() || !strings.HasPrefix(k.fn, "Test") {
			continue
		}
		hit := callees[target]
		if !hit {
			for c := range callees {
				if c != target && targetCallers[c] {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, k.file+":"+k.fn)
		}
	}
	sort.Strings(out)
	return out
}

func cmdTestCallers(root, target string) {
	fmt.Println(strings.Join(mustScan(root).testCallers(target), "\n"))
}

// rippleFiles devolve os arquivos não-teste com pelo menos um sítio de chamada direta ao alvo.
func (r *repo) rippleFiles(target string) []string {
	files := map[string]bool{}
	for s := range r.callsByCallee[target] {
		if !s.test {
			files[s.file] = true
		}
	}
	var out []string
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// rippleSites conta os sítios de chamada direta em arquivos não-teste (um arquivo pode ter vários).
func (r *repo) rippleSites(target string) int {
	n := 0
	for s := range r.callsByCallee[target] {
		if !s.test {
			n++
		}
	}
	return n
}

// cmdPickTargets lista as funções exportadas declaradas em arquivos não-teste cuja contagem de testes
// que as alcançam está em [min,max]. A ordem (caminho do arquivo, depois linha da declaração) é o que
// define qual alvo cada tarefa escolhe — a escolha fica auditável e estável entre execuções.
func cmdPickTargets(root string, min, max int) {
	r := mustScan(root)
	fset := token.NewFileSet()
	type decl struct {
		file, fn string
		line     int
	}
	var decls []decl
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || !fd.Name.IsExported() {
				continue
			}
			decls = append(decls, decl{rel, fd.Name.Name, fset.Position(fd.Pos()).Line})
		}
		return nil
	})
	for _, d := range decls {
		n := len(r.testCallers(d.fn))
		if n < min || n > max {
			continue
		}
		fmt.Printf("%s:%d %s tests=%d files=%d sites=%d\n", d.file, d.line, d.fn, n, len(r.rippleFiles(d.fn)), r.rippleSites(d.fn))
	}
}

func (k fnKey) fileIsTest() bool { return strings.HasSuffix(k.file, "_test.go") }

func cmdRipple(root, target string) {
	fmt.Println(strings.Join(mustScan(root).rippleFiles(target), "\n"))
}

var frameRe = regexp.MustCompile(`^\s*(\S+\.go):(\d+)`)

// cmdTraceOrigin imprime o primeiro frame (o mais profundo) cujo arquivo está sob a raiz — é o
// ponto onde o panic nasceu; os frames abaixo dele são o caminho de chamada até main.
func cmdTraceOrigin(root, trace string) {
	b, err := os.ReadFile(trace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro ao ler trace:", err)
		os.Exit(2)
	}
	rootSlash := filepath.ToSlash(strings.TrimRight(root, `/\`)) + "/"
	for _, line := range strings.Split(string(b), "\n") {
		m := frameRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p := filepath.ToSlash(m[1])
		if !strings.HasPrefix(p, rootSlash) {
			continue
		}
		fmt.Printf("%s:%s\n", filepath.Base(p), m[2])
		return
	}
	fmt.Fprintln(os.Stderr, "nenhum frame sob", root, "no trace")
	os.Exit(2)
}

// exportedRefs devolve os nomes exportados declarados no arquivo que aparecem como palavra inteira no comentário.
func exportedRefs(f *ast.File, text string) []string {
	declared := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				declared[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						declared[s.Name.Name] = true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							declared[n.Name] = true
						}
					}
				}
			}
		}
	}
	var out []string
	for name := range declared {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(text) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func parseWithComments(path string) (*ast.File, *token.FileSet, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	return f, fset, err
}

func cmdCommentRefs(root, rel string, line int) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	f, fset, err := parseWithComments(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro ao analisar", p, ":", err)
		os.Exit(2)
	}
	for _, cg := range f.Comments {
		ini := fset.Position(cg.Pos()).Line
		fim := fset.Position(cg.End()).Line
		if line >= ini && line <= fim {
			refs := exportedRefs(f, cg.Text())
			fmt.Println(strings.Join(refs, "\n"))
			return
		}
	}
	fmt.Fprintln(os.Stderr, "nenhum comentário na linha", line, "de", rel)
	os.Exit(2)
}

// isMarkedComment reconhece os comentários de marcação do Agent Retrieval Bench (comment2context):
// // TODO(...) e // Deprecated: (go/ast já entrega o texto sem as barras).
func isMarkedComment(text string) bool {
	return strings.Contains(text, "TODO") || strings.Contains(text, "Deprecated:")
}

// firstComment acha, em ordem de caminho e depois de posição, o primeiro comentário de arquivo
// não-teste que cita entre min e max símbolos exportados do próprio arquivo. marked restringe aos
// comentários de marcação; sem nenhum deles na faixa, a tarefa cai para qualquer comentário.
func firstComment(root string, min, max int, marked bool) (string, bool) {
	fset := token.NewFileSet()
	var out string
	found := false
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found || !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if perr != nil {
			return nil
		}
		for _, cg := range f.Comments {
			txt := cg.Text()
			if marked && !isMarkedComment(txt) {
				continue
			}
			refs := exportedRefs(f, txt)
			if len(refs) < min || len(refs) > max {
				continue
			}
			out = fmt.Sprintf("%s:%d n=%d %s", rel, fset.Position(cg.Pos()).Line, len(refs), strings.Join(refs, ","))
			found = true
			return nil
		}
		return nil
	})
	return out, found
}

func cmdPickComment(root string, min, max int) {
	for _, marked := range []bool{true, false} {
		if s, ok := firstComment(root, min, max, marked); ok {
			fmt.Println(s)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "nenhum comentário citando entre", min, "e", max, "símbolos")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "uso: contexto <testcallers|ripple|picktargets|traceorigin|commentrefs|pickcomment> ...")
		os.Exit(2)
	}
	cmd := os.Args[1]
	root := os.Args[2]
	switch cmd {
	case "testcallers", "ripple":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "uso: contexto", cmd, "<raiz> <alvo>")
			os.Exit(2)
		}
		if cmd == "testcallers" {
			cmdTestCallers(root, os.Args[3])
		} else {
			cmdRipple(root, os.Args[3])
		}
	case "picktargets":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "uso: contexto picktargets <raiz> <min> <max>")
			os.Exit(2)
		}
		min, err1 := strconv.Atoi(os.Args[3])
		max, err2 := strconv.Atoi(os.Args[4])
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "min/max inválidos")
			os.Exit(2)
		}
		cmdPickTargets(root, min, max)
	case "traceorigin":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "uso: contexto traceorigin <raiz> <trace>")
			os.Exit(2)
		}
		cmdTraceOrigin(root, os.Args[3])
	case "commentrefs":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "uso: contexto commentrefs <raiz> <rel> <linha>")
			os.Exit(2)
		}
		line, err := strconv.Atoi(os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, "linha inválida:", os.Args[4])
			os.Exit(2)
		}
		cmdCommentRefs(root, os.Args[3], line)
	case "pickcomment":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "uso: contexto pickcomment <raiz> <min> <max>")
			os.Exit(2)
		}
		min, err1 := strconv.Atoi(os.Args[3])
		max, err2 := strconv.Atoi(os.Args[4])
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "min/max inválidos")
			os.Exit(2)
		}
		cmdPickComment(root, min, max)
	default:
		fmt.Fprintln(os.Stderr, "subcomando desconhecido:", cmd)
		os.Exit(2)
	}
}
