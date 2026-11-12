package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chainFixture: cadeia D <- C <- B <- A (cada um chama o anterior), um ciclo X <-> Y, uma cadeia curta L <- M1 <- M2
// <- M3 e um teste que chama D. Tudo num arquivo só para os números de linha serem previsíveis.
//
//	 1 package chain		13 func M1() { L() }
//	 3 func D() {}			15 func M2() { M1() }
//	 5 func C() { D() }		17 func M3() { M2() }
//	 7 func B() { C() }		19 func X() { Y() }
//	 9 func A() { B() }		21 func Y() { X() }
//	11 func L() {}
func chainFixture(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"chain/chain.go": `package chain

func D() {}

func C() { D() }

func B() { C() }

func A() { B() }

func L() {}

func M1() { L() }

func M2() { M1() }

func M3() { M2() }

func X() { Y() }

func Y() { X() }
`,
		"chain/chain_test.go": `package chain

import "testing"

func TestD(t *testing.T) {
	D()
}
`,
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCallersCadeiaPorNivel(t *testing.T) {
	root := chainFixture(t)
	cases := []struct {
		depth int
		want  string
	}{
		{1, "=== callers of 'D' ===\ntests: 1 files: chain/chain_test.go\n- chain/chain.go:5 in C (calls)\n" +
			"- chain/chain_test.go:6 in TestD (calls)\nTotal: 2 references in 2 files."},
		{2, "=== callers of 'D' (depth 2) ===\ntests: 1 files: chain/chain_test.go\ndepth 1:\n" +
			"- chain/chain.go:5 in C (calls)\n- chain/chain_test.go:6 in TestD (calls)\ndepth 2:\n" +
			"- chain/chain.go:7 in B (calls)\nTotal: 3 references in 2 files (3 symbols)."},
		{3, "=== callers of 'D' (depth 3) ===\ntests: 1 files: chain/chain_test.go\ndepth 1:\n" +
			"- chain/chain.go:5 in C (calls)\n- chain/chain_test.go:6 in TestD (calls)\ndepth 2:\n" +
			"- chain/chain.go:7 in B (calls)\ndepth 3:\n- chain/chain.go:9 in A (calls)\n" +
			"Total: 4 references in 2 files (4 symbols)."},
	}
	for _, c := range cases {
		got, err := Callers(root, "D", 0, c.depth)
		if err != nil {
			t.Fatalf("depth %d: %v", c.depth, err)
		}
		if body(got) != c.want {
			t.Errorf("depth %d:\n%s\nquer:\n%s", c.depth, body(got), c.want)
		}
	}

	// depth acima do teto para no teto (4), não erra
	got, err := Callers(root, "D", 0, 99)
	if err != nil || !strings.Contains(got, "=== callers of 'D' (depth 4) ===") || !strings.Contains(got, "depth 3:") {
		t.Errorf("depth 99 deveria virar o teto %d:\n%s", MaxCallDepth, got)
	}

	// corte por max: mostra max linhas no total e diz quantas ficaram
	got, err = Callers(root, "D", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "... 2 more (raise 'max')") || strings.Contains(got, "A (calls)") {
		t.Errorf("corte por max=2:\n%s", got)
	}
	if !strings.Contains(got, "Total: 4 references in 2 files") {
		t.Errorf("o total conta tudo, mesmo cortado:\n%s", got)
	}
}

func TestCallersCicloNaoTrava(t *testing.T) {
	root := chainFixture(t)
	got, err := Callers(root, "X", 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== callers of 'X' (depth 4) ===\ndepth 1:\n- chain/chain.go:21 in Y (calls)\nTotal: 1 references in 1 files (1 symbols)."
	if body(got) != want {
		t.Errorf("ciclo X<->Y:\n%s\nquer:\n%s", body(got), want)
	}
}

func TestCallersMarcaArquivoDeTeste(t *testing.T) {
	root := chainFixture(t)
	got, err := Callers(root, "D", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "tests: 1 files: chain/chain_test.go") {
		t.Errorf("faltou a linha de testes:\n%s", got)
	}

	// sem arquivo de teste no resultado a linha não existe (depth 1 fica igual ao de sempre)
	got, err = Callers(root, "L", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "tests:") {
		t.Errorf("sem teste não deveria ter linha 'tests:':\n%s", got)
	}
}

func TestCallersDicaDeDepthSoQuandoVale(t *testing.T) {
	root := chainFixture(t)
	// L: 1 chamador direto, nenhum teste, e mais 2 símbolos escondidos atrás -> a dica aparece
	got, err := Callers(root, "L", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "transitive: depth=3 reaches 2 more symbols.") {
		t.Errorf("faltou a dica em L:\n%s", got)
	}

	// M1: a cadeia acaba no nível 2, nada escondido -> sem dica
	got, err = Callers(root, "M1", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "transitive:") {
		t.Errorf("cadeia curta não deveria ganhar dica:\n%s", got)
	}

	// D: já tem teste marcado, então a pergunta "quais testes" está respondida -> sem dica
	got, err = Callers(root, "D", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "transitive:") {
		t.Errorf("com teste marcado não deveria ganhar dica:\n%s", got)
	}
}

func TestCallersDepthMarcaHomonimo(t *testing.T) {
	root := csFixture(t)
	got, err := Callers(root, "Calcular", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"=== callers of 'Calcular' (depth 3) ===",
		"depth 1:",
		"in AppService.Vincular via _mcmsService -> McmsService (calls)",
		"in AppService.Obscuro via svc -> ? (calls)",
		"NOTE: ambiguous (declared in >1 type, matched by name): Calcular (2 types).",
		"Total: 4 references in 2 files (4 symbols).",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	if strings.Contains(got, "depth 2:") {
		t.Errorf("quem chama Calcular não é chamado por ninguém:\n%s", got)
	}
}

func TestCallersSemChamador(t *testing.T) {
	root := chainFixture(t)
	got, err := Callers(root, "M3", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "(no callers of 'M3'") {
		t.Errorf("símbolo sem chamador:\n%s", got)
	}
}
