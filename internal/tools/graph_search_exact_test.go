package tools

import (
	"strings"
	"testing"
)

// TestFilterExactToSub: o grep foi pedido numa pasta; a resposta exata vale so para ela (linhas fora saem, testes e
// total sao refeitos), e sem nada dentro da pasta devolve vazio para o caminho de sempre seguir.
func TestFilterExactToSub(t *testing.T) {
	in := strings.Join([]string{
		"=== callers of 'X' (exact, csharp-ls) ===",
		"implemented by: A (src/Lib/A.cs:3)",
		"tests: 2 files: test/T.cs, src/Lib/Tests/U.cs",
		"- src/Lib/A.cs (2): 3,9 in A.M",
		"- src/Other/B.cs (1): 5 in B.N",
		"- test/T.cs (1): 7 in T.Run",
		"Total: 4 references in 3 files.",
	}, "\n")
	if got := filterExactToSub(in, ""); !strings.Contains(got, "src/Other/B.cs") {
		t.Errorf("sem pasta a resposta fica inteira:\n%s", got)
	}
	got := filterExactToSub(in, "src/Lib")
	for _, want := range []string{"=== callers of 'X'", "implemented by: A", "tests: 1 files: src/Lib/Tests/U.cs", "- src/Lib/A.cs (2): 3,9 in A.M", "Total: 2 references in 1 files under src/Lib."} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	for _, bad := range []string{"src/Other/B.cs", "test/T.cs", "Total: 4"} {
		if strings.Contains(got, bad) {
			t.Errorf("nao devia ter %q em:\n%s", bad, got)
		}
	}
	if got := filterExactToSub(in, "src/Nada"); got != "" {
		t.Errorf("pasta sem usos devolve vazio, veio:\n%s", got)
	}
}
