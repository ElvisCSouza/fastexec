package graph

// parser.go: extrai declarações e referências de um arquivo. Go: go/parser + go/ast da biblioteca padrão (tipos,
// funções, métodos com receiver, chamadas, literais de tipo, funções passadas como valor e imports). Demais
// linguagens: scanners de linha do internal/codescan. Sem resolução de tipos: arestas apontam para nomes.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"homelab/fastexec/internal/codescan"
)

// ParseFile devolve declarações e referências; ok=false para linguagem não suportada ou Go ilegível.
func ParseFile(rel string, src []byte) ([]codescan.Decl, []codescan.Ref, bool) {
	if IsCSharp(rel) {
		return parseCSharp(rel, src) // C# tem parser próprio (csharp.go): léxico e escopos são outros
	}
	l := codescan.LangFor(rel)
	if l == nil {
		return nil, nil, false
	}
	if strings.HasSuffix(strings.ToLower(rel), ".go") {
		return parseGo(rel, src)
	}
	d, r := codescan.Scan(src, l)
	return d, r, true
}

var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
	"bool": true, "byte": true, "rune": true, "string": true, "error": true, "any": true, "uintptr": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
	"true": true, "false": true, "nil": true, "iota": true,
}

func parseGo(rel string, src []byte) ([]codescan.Decl, []codescan.Ref, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if f == nil {
		_ = err
		return nil, nil, false
	}
	line := func(p token.Pos) int { return fset.Position(p).Line }
	text := func(from, to token.Pos) string {
		a, b := fset.Position(from).Offset, fset.Position(to).Offset
		if a < 0 || b > len(src) || a >= b {
			return ""
		}
		return strings.Join(strings.Fields(string(src[a:b])), " ")
	}

	var decls []codescan.Decl
	var refs []codescan.Ref
	for _, imp := range f.Imports {
		refs = append(refs, codescan.Ref{Line: line(imp.Pos()), Target: strings.Trim(imp.Path.Value, "\"`"), Rel: "imports"})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			kind := "func"
			if d.Recv != nil {
				kind = "method"
			}
			decls = append(decls, codescan.Decl{
				Line: line(d.Pos()), EndLine: line(d.End()), Name: d.Name.Name, Kind: kind,
				Sig: clip(text(d.Pos(), d.Type.End())), Exported: d.Name.IsExported(),
			})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, s := range d.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok {
					continue
				}
				kind, word := "type", ""
				switch ts.Type.(type) {
				case *ast.StructType:
					kind, word = "struct", " struct"
				case *ast.InterfaceType:
					kind, word = "interface", " interface"
				}
				start := ts.Pos()
				if len(d.Specs) == 1 {
					start = d.Pos()
				}
				sig := "type " + ts.Name.Name + word
				if word == "" {
					sig = "type " + clip(text(ts.Pos(), ts.End()))
				}
				decls = append(decls, codescan.Decl{
					Line: line(start), EndLine: line(ts.End()), Name: ts.Name.Name, Kind: kind,
					Sig: sig, Exported: ts.Name.IsExported(),
				})
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if name, pos := calleeName(n.Fun); name != "" && !goBuiltins[name] {
				refs = append(refs, codescan.Ref{Line: line(pos), Target: name, Rel: "calls"})
			}
			for _, a := range n.Args {
				// função passada como valor (http.HandleFunc("/x", Handler)): nome exportado ou pkg.Nome
				switch a := a.(type) {
				case *ast.Ident:
					if ast.IsExported(a.Name) {
						refs = append(refs, codescan.Ref{Line: line(a.Pos()), Target: a.Name, Rel: "references"})
					}
				case *ast.SelectorExpr:
					refs = append(refs, codescan.Ref{Line: line(a.Sel.Pos()), Target: a.Sel.Name, Rel: "references"})
				}
			}
		case *ast.CompositeLit:
			if name, pos := typeName(n.Type); name != "" && !goBuiltins[name] {
				refs = append(refs, codescan.Ref{Line: line(pos), Target: name, Rel: "references"})
			}
		}
		return true
	})
	return decls, refs, true
}

// calleeName: f(), pkg.F(), x.M(), F[T](), (f)().
func calleeName(e ast.Expr) (string, token.Pos) {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name, e.Pos()
	case *ast.SelectorExpr:
		return e.Sel.Name, e.Sel.Pos()
	case *ast.IndexExpr:
		return calleeName(e.X)
	case *ast.IndexListExpr:
		return calleeName(e.X)
	case *ast.ParenExpr:
		return calleeName(e.X)
	}
	return "", token.NoPos
}

// typeName: T{}, pkg.T{}, &T{}, []T{}.
func typeName(e ast.Expr) (string, token.Pos) {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name, e.Pos()
	case *ast.SelectorExpr:
		return e.Sel.Name, e.Sel.Pos()
	case *ast.StarExpr:
		return typeName(e.X)
	case *ast.ArrayType:
		return typeName(e.Elt)
	case *ast.IndexExpr:
		return typeName(e.X)
	}
	return "", token.NoPos
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
