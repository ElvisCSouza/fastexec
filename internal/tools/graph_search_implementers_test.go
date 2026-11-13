package tools

import (
	"strings"
	"testing"
)

// TestWithImplementersLine: a linha "implemented by ... +13" (cortada) vira a lista completa dos tipos marcados como
// (implements) nos grupos; sem marca, ou em lista corrida, o corpo fica como veio.
func TestWithImplementersLine(t *testing.T) {
	in := strings.Join([]string{
		"=== callers of 'ISink' (exact, csharp-ls) ===",
		"implemented by: A (src/a/A.cs:3), ... +13",
		"- src/a/A.cs (3): 17 in A.A (implements); 19,21 in A.A",
		"- src/a/B.cs (2): 4,9 in B.M; 30 in B.N (implements)",
		"- src/a/C.cs (1): 5 in C.M",
		"Total: 6 references in 3 files.",
	}, "\n")
	got := withImplementersLine(in)
	want := "implementers (2, complete, exact): A (src/a/A.cs:17), B (src/a/B.cs:30)"
	if !strings.Contains(got, want) {
		t.Fatalf("faltou %q em:\n%s", want, got)
	}
	if strings.Contains(got, "+13") || strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Errorf("a linha cortada deve ser trocada, sem linha a mais:\n%s", got)
	}

	flat := "=== callers of 'X' (exact, csharp-ls) ===\n- a.cs:3 in A.M (implements)\nTotal: 1 references in 1 files.\n"
	if got := withImplementersLine(flat); got != flat {
		t.Errorf("lista corrida nao muda:\n%s", got)
	}
	if got := withImplementersLine("sem marcas\n- a.cs (1): 3 in A.M"); got != "sem marcas\n- a.cs (1): 3 in A.M" {
		t.Errorf("sem marca nao muda: %q", got)
	}
}
