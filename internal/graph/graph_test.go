package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture monta um repositório com dois pacotes Go, um main, chamadas cruzadas e um par TS que se importa.
func fixture(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.com/demo\n",
		"store/store.go": `package store

// Entry é um registro.
type Entry struct {
	Name string
}

type Saver interface {
	Save(e Entry) error
}

func NewEntry(n string) Entry {
	return Entry{Name: n}
}

func (e Entry) Valid() bool {
	return e.Name != ""
}

func internalOnly() {}
`,
		"store/disk.go": `package store

func persist() { internalOnly() }
`,
		"app/app.go": `package app

import "example.com/demo/store"

func Run() {
	e := store.NewEntry("x")
	_ = e.Valid()
	helper()
}

func helper() {}
`,
		"app/other.go": `package app

func Other() {
	helper()
}
`,
		"cmd/main.go": `package main

import "example.com/demo/app"

func main() {
	app.Run()
}
`,
		"web/lib/util.ts":           "export function fmt(x: number): string {\n  return String(x);\n}\n",
		"web/main.ts":               "import { fmt } from './lib/util';\nconsole.log(fmt(1));\n",
		"node_modules/x/index.js":   "function helper() {}\nhelper();\n",
		"vendor/example.com/v/v.go": "package v\nfunc helper() {}\n",
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

func body(s string) string {
	if i := strings.Index(s, "\n[index]"); i >= 0 {
		return s[:i]
	}
	return s
}

func TestCallersExatos(t *testing.T) {
	root := fixture(t)
	got, err := Callers(root, "helper", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== callers of 'helper' ===\n- app/app.go:8 in Run (calls)\n- app/other.go:4 in Other (calls)\nTotal: 2 references in 2 files."
	if body(got) != want {
		t.Errorf("callers helper:\n%s\nquer:\n%s", got, want)
	}
	got, _ = Callers(root, "store.NewEntry", 0, 1)
	if body(got) != "=== callers of 'NewEntry' ===\n- app/app.go:6 in Run (calls)\nTotal: 1 references in 1 files." {
		t.Errorf("callers NewEntry:\n%s", got)
	}
	got, err = Callers(root, "fmt", 0, 1)
	if err != nil {
		t.Fatalf("callers fmt: %v", err)
	}
	if !strings.Contains(got, "- web/main.ts:1 (references)") || !strings.Contains(got, "- web/main.ts:2 (calls)") {
		t.Errorf("callers fmt (TS):\n%s", got)
	}
}

func TestDefinition(t *testing.T) {
	root := fixture(t)
	got, _ := Definition(root, "Entry")
	if body(got) != "=== definition of 'Entry' ===\n- store/store.go:4-6 (struct)\n  type Entry struct" {
		t.Errorf("def Entry:\n%s", got)
	}
	got, _ = Definition(root, "Valid")
	if !strings.Contains(got, "- store/store.go:16-18 (method)\n  func (e Entry) Valid() bool") {
		t.Errorf("def Valid:\n%s", got)
	}
	got, _ = Definition(root, "Entr")
	if !strings.Contains(got, "similar: Entry") {
		t.Errorf("sugestão:\n%s", got)
	}
}

func TestImpactArquivo(t *testing.T) {
	root := fixture(t)
	got, _ := Impact(root, "store/store.go", 0)
	want := "=== impact of 'store/store.go' ===\n- app/app.go (uses: NewEntry, Valid)\n- store/disk.go (uses: internalOnly)\nTotal: 2 files."
	if body(got) != want {
		t.Errorf("impact store.go:\n%s\nquer:\n%s", got, want)
	}
	got, _ = Impact(root, filepath.Join(root, "web", "lib", "util.ts"), 0)
	if body(got) != "=== impact of 'web/lib/util.ts' ===\n- web/main.ts (uses: fmt)\nTotal: 1 files." {
		t.Errorf("impact util.ts:\n%s", got)
	}
	got, _ = Impact(root, "helper", 0)
	if !strings.Contains(got, "- app/app.go (1 refs)") || !strings.Contains(got, "- app/other.go (1 refs)") {
		t.Errorf("impact símbolo:\n%s", got)
	}
}

func TestRuidoForaDoIndice(t *testing.T) {
	root := fixture(t)
	got, _ := Callers(root, "helper", 0, 1)
	if strings.Contains(got, "node_modules") || strings.Contains(got, "vendor") {
		t.Errorf("ruído indexado:\n%s", got)
	}
}

func TestIncremental(t *testing.T) {
	root := fixture(t)
	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Changed != 7 {
		t.Errorf("primeira indexação: Changed=%d, quer 7 (sem node_modules/vendor)", st.Changed)
	}
	st, _ = Refresh(root)
	if st.Changed != 0 || st.Removed != 0 {
		t.Errorf("sem mudança deveria ser no-op: %+v", st)
	}

	other := filepath.Join(root, "app", "other.go")
	if err := os.WriteFile(other, []byte("package app\n\nfunc Other() {\n\thelper()\n\thelper()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(other, future, future)
	st, _ = Refresh(root)
	if st.Changed != 1 {
		t.Errorf("1 arquivo mudou: Changed=%d", st.Changed)
	}
	got, _ := Callers(root, "helper", 0, 1)
	if !strings.Contains(got, "Total: 3 references in 2 files.") {
		t.Errorf("depois da mudança:\n%s", got)
	}

	if err := os.Remove(filepath.Join(root, "store", "disk.go")); err != nil {
		t.Fatal(err)
	}
	st, _ = Refresh(root)
	if st.Removed != 1 {
		t.Errorf("arquivo removido: Removed=%d", st.Removed)
	}

	// reindexação de 1 arquivo: média de 50 execuções abaixo de 5 ms (média, porque no Windows o relógio do Go
	// avança por tick do sistema e uma execução isolada cabe dentro de um tick)
	const n = 50
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := ReindexFile(root, other); err != nil {
			t.Fatal(err)
		}
	}
	avg := time.Since(start) / n
	t.Logf("ReindexFile média: %v", avg)
	if avg >= 5*time.Millisecond {
		t.Errorf("reindexação de 1 arquivo levou %v em média (meta < 5 ms)", avg)
	}
}

func TestIsolamentoEntreRepos(t *testing.T) {
	a := fixture(t)
	b := t.TempDir()
	_ = os.Mkdir(filepath.Join(b, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(b, "x.go"), []byte("package x\nfunc helper() {}\nfunc y() { helper() }\n"), 0o644)
	if _, err := Refresh(b); err != nil {
		t.Fatal(err)
	}
	got, _ := Callers(a, "helper", 0, 1)
	if strings.Contains(got, "x.go") {
		t.Errorf("repo A viu símbolo do repo B:\n%s", got)
	}
	got, _ = Callers(b, "helper", 0, 1)
	if body(got) != "=== callers of 'helper' ===\n- x.go:3 in y (calls)\nTotal: 1 references in 1 files." {
		t.Errorf("repo B:\n%s", got)
	}
}

// pastaSolta cria uma pasta que não está dentro de nenhum repositório git (senão o teste não se aplica) e devolve
// o caminho do arquivo escrito. É o cenário M2/M3: o agente chama o grafo numa pasta avulsa.
func pastaSolta(t *testing.T, rel, src string) (dir, file string) {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	dir = t.TempDir()
	file = filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if root, ok := ResolveRepoRoot(file); ok {
		t.Skipf("ambiente com .git acima do TempDir (%s): teste não se aplica", root)
	}
	return dir, file
}

func TestPastaAvulsaIndexaEAtualiza(t *testing.T) {
	dir, file := pastaSolta(t, "script.go", "package x\n\nfunc A() {}\n\nfunc B() { A() }\n")

	root, ok := ResolveStandaloneRoot(file) // o caminho é um arquivo: a pasta avulsa é a que o contém
	if !ok || root != dir {
		t.Fatalf("ResolveStandaloneRoot(%s) = %q,%v", file, root, ok)
	}
	st, err := Refresh(root)
	if err != nil {
		t.Fatalf("pasta avulsa pequena deveria indexar: %v", err)
	}
	if st.Files != 1 || st.Changed != 1 || st.Fresh || st.OverBudget {
		t.Fatalf("passada inicial: %+v", st)
	}
	got, err := Callers(root, "A", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "script.go:5") {
		t.Errorf("callers na pasta avulsa:\n%s", got)
	}
	if n := TrackedFiles(root); n != 1 {
		t.Errorf("TrackedFiles = %d", n)
	}

	// Sem git não há estado barato: o frescor é a comparação (mtime, hash) de cada arquivo, e a segunda passada sem
	// mudança não reindexa nada.
	if st, err := Refresh(root); err != nil || st.Changed != 0 || !st.Fresh {
		t.Fatalf("segunda passada sem mudança: %+v %v", st, err)
	}

	// Arquivo editado: reindexa na passada seguinte, sem tocar em git (mtime forçado à frente porque o relógio do
	// Windows tem granularidade grossa e o teste roda em milissegundos).
	novo := "package x\n\nfunc A() {}\n\nfunc B() { A() }\n\nfunc C() { A() }\n"
	if err := os.WriteFile(file, []byte(novo), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatal(err)
	}
	if st, err := Refresh(root); err != nil || st.Changed != 1 {
		t.Fatalf("arquivo mudado deveria reindexar: %+v %v", st, err)
	}
	if got, _ := Callers(root, "A", 0, 1); !strings.Contains(got, "script.go:7") {
		t.Errorf("nova chamada não apareceu:\n%s", got)
	}

	// Um arquivo apagado sai do índice, e ReindexFile também age na pasta avulsa.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if st, err := Refresh(root); err != nil || st.Removed != 1 {
		t.Fatalf("arquivo apagado deveria sair: %+v %v", st, err)
	}
	if err := ReindexFile(root, file); err != nil {
		t.Fatalf("ReindexFile na pasta avulsa: %v", err)
	}
	Touch(file) // o hook de gravação nunca cria índice: aqui o índice existe, mas o arquivo sumiu — não pode explodir
	time.Sleep(touchDebounce + 100*time.Millisecond)
}

func TestPastaAvulsaAcimaDoLimiteRecusa(t *testing.T) {
	dir, _ := pastaSolta(t, "a.go", "package x\n\nfunc A() {}\n")
	for _, n := range []string{"b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("package x\n\nfunc B() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := maxStandaloneFiles
	maxStandaloneFiles = 2
	t.Cleanup(func() { maxStandaloneFiles = old })

	_, err := Refresh(dir)
	if err == nil || !strings.Contains(err.Error(), "not inside a git repository") || !IsNotInRepo(err) {
		t.Fatalf("pasta avulsa acima do limite deveria recusar: %v", err)
	}
	if _, err := Reindex(dir); err == nil || !IsNotInRepo(err) {
		t.Errorf("Reindex também deveria recusar: %v", err)
	}
	if err := ReindexFile(dir, filepath.Join(dir, "a.go")); err == nil || !IsNotInRepo(err) {
		t.Errorf("ReindexFile também deveria recusar: %v", err)
	}
	d, _ := store()
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM code_files`).Scan(&n)
	if n != 0 {
		t.Errorf("pasta recusada não pode deixar arquivo no índice (%d)", n)
	}

	// O teto de bytes vale pelo mesmo motivo (a varredura sem .gitignore lê o disco inteiro).
	maxStandaloneFiles = old
	oldB := maxStandaloneBytes
	maxStandaloneBytes = 8
	t.Cleanup(func() { maxStandaloneBytes = oldB })
	if _, err := Refresh(dir); err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("pasta acima do teto de bytes deveria recusar: %v", err)
	}
}

// TestRepositorioGitNaoMuda: dentro de git o caminho é o de sempre — a raiz achada pelo .git nunca é "avulsa" e o
// estado (HEAD, porcelano) continua dispensando a varredura.
func TestRepositorioGitNaoMuda(t *testing.T) {
	root := fixture(t)
	if _, ok := ResolveStandaloneRoot(root); ok {
		t.Errorf("raiz de repositório git não pode ser tratada como pasta avulsa")
	}
	if st, err := Refresh(root); err != nil || st.Changed == 0 {
		t.Fatalf("primeira passada: %+v %v", st, err)
	}
	if st, err := Refresh(root); err != nil || !st.Fresh {
		t.Fatalf("repositório git sem mudança deveria responder do estado: %+v %v", st, err)
	}
}

func TestLimiteDeArquivosRecusaIndexar(t *testing.T) {
	root := fixture(t)
	old := maxRepoFiles
	maxRepoFiles = 3
	t.Cleanup(func() { maxRepoFiles = old })
	_, err := Refresh(root)
	if err == nil || !strings.Contains(err.Error(), "[FastExec Graph]") || !strings.Contains(err.Error(), ".gitignore") {
		t.Fatalf("repositório acima do limite deveria ser recusado: %v", err)
	}
	if _, err := Callers(root, "helper", 0, 1); err == nil {
		t.Error("consulta também deveria recusar (indexa antes)")
	}
	d, _ := store()
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM code_files WHERE repo_root = ?`, repoKey(root)).Scan(&n)
	if n != 0 {
		t.Errorf("nada deveria ter sido indexado (%d arquivos)", n)
	}
}

func TestCleanRemoveSoORepositorioPedido(t *testing.T) {
	a := fixture(t)
	b := t.TempDir()
	_ = os.Mkdir(filepath.Join(b, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(b, "x.go"), []byte("package x\nfunc helper() {}\nfunc y() { helper() }\n"), 0o644)
	if _, err := Refresh(a); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(b); err != nil {
		t.Fatal(err)
	}

	st, err := Clean(a)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 7 || st.Symbols == 0 || st.Edges == 0 {
		t.Errorf("Clean(a) removeu %+v", st)
	}
	repos, _ := Repos()
	if len(repos) != 1 || !strings.Contains(repos[0], "(1 files)") {
		t.Errorf("depois do Clean(a) só o repo b deveria restar: %v", repos)
	}
	got, _ := Callers(b, "helper", 0, 1)
	if !strings.Contains(got, "- x.go:3 in y (calls)") {
		t.Errorf("repo b afetado pelo Clean(a):\n%s", got)
	}

	// repositório apagado do disco: Clean pelo caminho ainda limpa
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	st, err = Clean(b)
	if err != nil || st.Files != 1 {
		t.Errorf("Clean de repo apagado: %+v %v", st, err)
	}
	if repos, _ := Repos(); len(repos) != 0 {
		t.Errorf("banco deveria estar vazio: %v", repos)
	}
}

func TestResolveRepoRoot(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, ".git"), 0o755)
	deep := filepath.Join(root, "a", "b")
	_ = os.MkdirAll(deep, 0o755)
	got, ok := ResolveRepoRoot(filepath.Join(deep, "naoexiste.go"))
	if !ok || got != root {
		t.Errorf("ResolveRepoRoot = %q, %v; quer %q", got, ok, root)
	}
}

func TestTouchReindexaArquivoGravado(t *testing.T) {
	root := fixture(t)
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "app", "novo.go")
	_ = os.WriteFile(p, []byte("package app\nfunc Novo() { helper() }\n"), 0o644)
	Touch(p)
	time.Sleep(touchDebounce + 400*time.Millisecond)
	d, _ := store()
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM code_symbols WHERE repo_root = ? AND name = 'Novo'`, repoKey(root)).Scan(&n)
	if n != 1 {
		t.Errorf("Touch não indexou o arquivo novo (n=%d)", n)
	}
}
