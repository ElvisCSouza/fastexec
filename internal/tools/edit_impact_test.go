package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// edit_impact_test.go cobre o rodapé "[graph] ..." da resposta de edit_file/write_file (Parte A da task
// 20261112-edit-impacto) e o aviso de deleção do exec (Parte B, exec_delete_impact_test.go).
//
// Os testes chamam editImpact direto na maioria dos casos: é a função que decide o texto, e assim a expectativa
// fica byte a byte em vez de "contém". Dois testes de fiação (Wiring) provam que edit_file e write_file de fato
// anexam o aviso na resposta.

// --- fixture ---------------------------------------------------------------

// fetchRepo monta um repositório git com a.go (declara Fetch), dois chamadores fora dele (b.go, c.go) e um teste
// (a_test.go), e indexa tudo. O grafo fica num banco temporário.
//
//	a.go:3  func Fetch(n int) error {   b.go:3  func B() { Fetch(1) }
//	a.go:4  	return nil                 c.go:3  func C() { Fetch(2) }
//	a_test.go:5  func TestFetch(t *testing.T) { Fetch(3) }
func fetchRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"a.go":      fetchOld,
		"b.go":      "package p\n\nfunc B() { Fetch(1) }\n",
		"c.go":      "package p\n\nfunc C() { Fetch(2) }\n",
		"a_test.go": "package p\n\nimport \"testing\"\n\nfunc TestFetch(t *testing.T) { Fetch(3) }\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return impactRepo(t, files)
}

// impactRepo cria o repositório, aponta o grafo para um banco temporário e indexa. Sem o Refresh o repositório
// existe mas não está indexado — que é justamente o caso (d).
func impactRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
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
	if _, err := graph.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return root
}

const (
	// fetchOld é o conteúdo de a.go no índice: o corpo numa linha própria, para o caso (b) mexer só no corpo.
	fetchOld = `package p

func Fetch(n int) error {
	return nil
}
`
	// fetchNewSig muda só o cabeçalho: a assinatura.
	fetchNewSig = `package p

func Fetch(n int, s string) error {
	return nil
}
`
	// fetchNewBody mexe só no corpo. O Sig do grafo é o cabeçalho, então nada muda para quem chama.
	fetchNewBody = `package p

func Fetch(n int) error {
	return errSentinel
}
`
	// fetchRenamed troca o nome: o símbolo antigo sumiu.
	fetchRenamed = `package p

func FetchAll(n int) error {
	return nil
}
`

	// fetchWant é o aviso esperado quando a assinatura de Fetch muda: b.go e c.go antes do arquivo de teste, que é
	// contado à parte.
	fetchWant = "[graph] Fetch signature changed: 3 use(s) in 3 file(s) may need update: " +
		"b.go:3, c.go:3, a_test.go:5 (tests: 1). By name, not type-checked."
)

// --- (a) assinatura mudou: avisa com os locais -----------------------------

func TestEditImpactSignatureChanged(t *testing.T) {
	root := fetchRepo(t, nil)
	got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchNewSig)
	if got != fetchWant {
		t.Errorf("aviso:\n%q\nquer:\n%q", got, fetchWant)
	}
}

// --- (b) só o corpo: nenhum byte a mais -----------------------------------

func TestEditImpactBodyOnlySilent(t *testing.T) {
	root := fetchRepo(t, nil)
	if got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchNewBody); got != "" {
		t.Errorf("mudança de corpo não deveria avisar nada, veio %q", got)
	}
}

// --- (c) removido/renomeado ------------------------------------------------

func TestEditImpactRenamed(t *testing.T) {
	root := fetchRepo(t, nil)
	got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchRenamed)
	want := strings.Replace(fetchWant, "Fetch signature changed", "Fetch removed/renamed", 1)
	if got != want {
		t.Errorf("aviso de rename:\n%q\nquer:\n%q", got, want)
	}
}

// Também não dispara em comentário, formatação, símbolo novo ou edição que não mexe em declaração.
func TestEditImpactDoesNotFireOnCosmeticChange(t *testing.T) {
	root := fetchRepo(t, nil)
	cases := []struct {
		name     string
		old, neu string
	}{
		{"comentário", fetchOld, "// nota\n" + fetchOld},
		{"formatação", fetchOld, strings.Replace(fetchOld, "func Fetch(n int) error {", "func Fetch(n int) error   {", 1)},
		{"símbolo novo", fetchOld, fetchOld + "\nfunc Extra() {}\n"},
		{"igual", fetchOld, fetchOld},
	}
	for _, c := range cases {
		if got := editImpact("", filepath.Join(root, "a.go"), c.old, c.neu); got != "" {
			t.Errorf("%s: não deveria avisar, veio %q", c.name, got)
		}
	}
}

// --- (d) repositório sem índice: nem aviso, nem graph.db -------------------

func TestEditImpactNoIndexNoWarningAndNoDB(t *testing.T) {
	// repositório com .git mas nunca indexado
	dbp := filepath.Join(t.TempDir(), "graph.db")
	graph.SetDBPath(dbp)
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(root, "a.go")
	if err := os.WriteFile(a, []byte(fetchOld), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := editImpact("", a, fetchOld, fetchNewSig); got != "" {
		t.Errorf("repositório sem índice não deveria avisar, veio %q", got)
	}
	if _, err := os.Stat(dbp); err == nil {
		t.Errorf("o aviso criou %s: quem nunca usou o grafo não ganha índice", dbp)
	}

	// e fora de qualquer repositório (sem .git)
	fora := filepath.Join(t.TempDir(), "solta.go")
	if err := os.WriteFile(fora, []byte(fetchOld), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := editImpact("", fora, fetchOld, fetchNewSig); got != "" {
		t.Errorf("fora de repositório não deveria avisar, veio %q", got)
	}
}

// --- (e) homônimo: não afirma o que não sabe -------------------------------

func TestEditImpactAmbiguousName(t *testing.T) {
	// z.go declara outro Fetch; com c.go chamando, ainda há uso a relatar — mas a lista poderia ser do outro símbolo
	root := fetchRepo(t, map[string]string{"z.go": "package p\n\nfunc Fetch(n int) error {\n\treturn nil\n}\n"})
	got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchNewSig)
	want := "[graph] Fetch signature changed: name is ambiguous (2 definitions): check callers with code_graph."
	if got != want {
		t.Errorf("aviso de homônimo:\n%q\nquer:\n%q", got, want)
	}
}

// --- (f) mais de 5 locais: "+K more" --------------------------------------

func TestEditImpactMoreThanFiveSites(t *testing.T) {
	extra := map[string]string{}
	for _, n := range []string{"b1.go", "b2.go", "b3.go", "b4.go", "b5.go", "b6.go"} {
		extra[n] = "package p\n\nfunc " + strings.ToUpper(n[:2]) + "() { Fetch(1) }\n"
	}
	root := fetchRepo(t, extra) // 8 chamadores (b.go, c.go + 6) + a_test.go = 9 locais
	got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchNewSig)
	if !strings.Contains(got, "+4 more") {
		t.Errorf("esperava '+4 more' com 9 locais (5 citados):\n%s", got)
	}
	if !strings.Contains(got, "9 use(s) in 9 file(s)") || !strings.Contains(got, "(tests: 1)") {
		t.Errorf("contagem errada:\n%s", got)
	}
	if n := strings.Count(got, ".go:"); n != 5 {
		t.Errorf("esperava citar 5 locais, citou %d:\n%s", n, got)
	}
}

// --- teto de avisos por chamada -------------------------------------------

func TestEditImpactAtMostThreeWarnings(t *testing.T) {
	old := "package p\n\n"
	neu := "package p\n\n"
	call := "package p\n\nfunc Caller() { F1(); F2(); F3(); F4() }\n"
	for i := 1; i <= 4; i++ {
		old += "func F" + string(rune('0'+i)) + "(n int) error {\n\treturn nil\n}\n\n"
		neu += "func F" + string(rune('0'+i)) + "(n int, s string) error {\n\treturn nil\n}\n\n"
	}
	root := impactRepo(t, map[string]string{"a.go": old, "call.go": call})
	got := editImpact("", filepath.Join(root, "a.go"), old, neu)
	if n := len(strings.Split(got, "\n")); n != editImpactMaxWarn {
		t.Errorf("esperava %d avisos (um por linha), veio %d:\n%s", editImpactMaxWarn, n, got)
	}
}

// --- linguagem sem declaração parseável ------------------------------------

func TestEditImpactNonCodeFileSilent(t *testing.T) {
	root := fetchRepo(t, nil)
	json := filepath.Join(root, "cfg.json")
	if got := editImpact("", json, `{"a":1}`, `{"a":2}`); got != "" {
		t.Errorf("json não deveria avisar, veio %q", got)
	}
	md := filepath.Join(root, "nota.md")
	if got := editImpact("", md, "# a\n", "# b\n"); got != "" {
		t.Errorf("markdown não deveria avisar, veio %q", got)
	}
}

// --- orçamento de tamanho --------------------------------------------------

func TestEditImpactStaysWithinBudget(t *testing.T) {
	extra := map[string]string{}
	for i := 0; i < 12; i++ {
		n := "pasta" + string(rune('a'+i)) + "/chamador_com_nome_bem_longo.go"
		extra[n] = "package p\n\nfunc Chamador() { Fetch(1) }\n"
	}
	root := fetchRepo(t, extra)
	got := editImpact("", filepath.Join(root, "a.go"), fetchOld, fetchNewSig)
	if got == "" {
		t.Fatal("esperava aviso")
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > editImpactBudget {
			t.Errorf("linha com %d chars, teto %d:\n%s", len(l), editImpactBudget, l)
		}
	}
}

// --- fiação: edit_file e write_file anexam o aviso -------------------------

func TestEditFileAppendsImpactWarning(t *testing.T) {
	root := fetchRepo(t, nil)
	a := filepath.Join(root, "a.go")
	out, err := HandleEditFile(EditFileArgs{
		Host:   "local",
		Path:   a,
		Target: "func Fetch(n int) error {",
		// vírgula nova = assinatura nova
		Replacement: "func Fetch(n int, s string) error {",
	})
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if !strings.Contains(out, fetchWant) {
		t.Errorf("a resposta do edit_file não trouxe o aviso:\n%s", out)
	}
}

func TestWriteFileOverExistingAppendsImpactWarning(t *testing.T) {
	root := fetchRepo(t, nil)
	a := filepath.Join(root, "a.go")
	out, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: a, Content: fetchNewSig, Overwrite: true})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(out, fetchWant) {
		t.Errorf("a resposta do write_file não trouxe o aviso:\n%s", out)
	}
}
