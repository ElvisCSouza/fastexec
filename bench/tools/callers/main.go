// callers: lists, per target function name, the enclosing functions of direct call sites in non-test .go files.
// usage: callers <root> [target]   (no target: prints candidates ranked by number of distinct caller files)
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	root := os.Args[1]
	target := ""
	if len(os.Args) > 2 {
		target = os.Args[2]
	}
	fset := token.NewFileSet()
	type site struct{ file, fn string }
	calls := map[string]map[site]bool{}
	decls := map[string]int{} // free functions and methods declared in the repo, by name
	files := 0
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil
		}
		files++
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			decls[name]++
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				t := fd.Recv.List[0].Type
				if s, ok := t.(*ast.StarExpr); ok {
					t = s.X
				}
				if id, ok := t.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
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
				if calls[callee] == nil {
					calls[callee] = map[site]bool{}
				}
				calls[callee][site{rel, name}] = true
				return true
			})
		}
		return nil
	})
	if target != "" {
		var out []string
		for s := range calls[target] {
			out = append(out, s.file+":"+s.fn)
		}
		sort.Strings(out)
		fmt.Println(strings.Join(out, "\n"))
		return
	}
	type cand struct {
		name           string
		sites, nfiles int
	}
	var cs []cand
	for name, m := range calls {
		if decls[name] != 1 {
			continue
		}
		fs := map[string]bool{}
		for s := range m {
			fs[s.file] = true
		}
		cs = append(cs, cand{name, len(m), len(fs)})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].nfiles > cs[j].nfiles || cs[i].nfiles == cs[j].nfiles && cs[i].sites > cs[j].sites })
	fmt.Printf("%d non-test files\n", files)
	for _, c := range cs[:min(len(cs), 40)] {
		fmt.Printf("%-28s callers=%-3d files=%d\n", c.name, c.sites, c.nfiles)
	}
}
