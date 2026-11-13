package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// csLogger monta uma classe com construtor homônimo e sobrecargas; a e t são os trechos que cada teste varia.
func csLogger(forString, forType string) string {
	return `public sealed class Logger
{
    internal Logger(int a)
    {
    }

    ` + forString + `

    ` + forType + `

    public ILogger ForContext<T>() => ForContext(typeof(T));
}
`
}

func parseCS(t *testing.T, src string) []string {
	t.Helper()
	d, _, ok := graph.ParseFile("a.cs", []byte(src))
	if !ok {
		t.Fatal("C# must be parseable")
	}
	var out []string
	for _, x := range d {
		out = append(out, x.Name)
	}
	return out
}

const (
	forString = "public ILogger ForContext(string a)\n    {\n        return Make(a);\n    }"
	forType   = "public ILogger ForContext(Type t)\n    {\n        return Make(t);\n    }"
)

// Sobrecargas e construtor com o nome da classe: editar só um corpo não é mudança de assinatura de ninguém.
func TestChangedDeclsOverloadsBodyEditIsNotASignatureChange(t *testing.T) {
	old := csLogger(forString, forType)
	neu := csLogger(strings.Replace(forString, "Make(a)", "Make(a + \"x\")", 1), forType)
	parseCS(t, old)
	od, _, _ := graph.ParseFile("a.cs", []byte(old))
	nd, _, _ := graph.ParseFile("a.cs", []byte(neu))
	if got := changedDecls(od, nd); len(got) != 0 {
		t.Fatalf("a body edit must not report signature changes, got %v", got)
	}
}

// Mudar de fato o cabeçalho de UMA sobrecarga continua sendo detectado.
func TestChangedDeclsOverloadSignatureChangeIsDetected(t *testing.T) {
	old := csLogger(forString, forType)
	neu := csLogger(strings.Replace(forString, "string a)", "string a, bool x)", 1), forType)
	od, _, _ := graph.ParseFile("a.cs", []byte(old))
	nd, _, _ := graph.ParseFile("a.cs", []byte(neu))
	got := changedDecls(od, nd)
	if len(got) != 1 || got[0].name != "ForContext" || got[0].removed {
		t.Fatalf("one overload header changed, want a single ForContext change, got %v", got)
	}
}
