package graph

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolated aponta o banco do grafo para um arquivo temporário, como o fixture faz.
func isolated(t *testing.T) {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitRepo é um repositório de verdade (init + commit): só aí existe o estado barato (HEAD + porcelano) que o Sync
// usa para pular a varredura. Sem git, todo Sync varre o disco.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível: o estado barato do Sync não pode ser exercitado")
	}
	root := t.TempDir()
	writeTree(t, root, files)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-qm", "init")
	return root
}

// TestSyncRecordsAndUsesRepoState: a passada completa grava o estado do repositório, e a seguinte com o mesmo estado
// responde do estado gravado sem varrer o disco. O contador de arquivos do estado é a prova: adulterado, ele volta
// como veio — sinal de que a varredura não aconteceu.
func TestSyncRecordsAndUsesRepoState(t *testing.T) {
	isolated(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
		"b.go": "package p\n\nfunc B() { A() }\n",
	})

	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.OverBudget || st.Fresh {
		t.Fatalf("primeira passada indexa de verdade: %+v", st)
	}
	if st.Files != 2 {
		t.Fatalf("esperava 2 arquivos indexados: %+v", st)
	}

	d, err := store()
	if err != nil {
		t.Fatal(err)
	}
	key := repoKey(root)
	head, work, ok := gitState(root)
	if !ok {
		t.Fatal("o repositório de teste precisa responder ao git")
	}
	m, ok := readMeta(d, key)
	if !ok || m.head != head || m.work != work {
		t.Fatalf("a passada completa tem de gravar o estado atual: %+v ok=%v (head=%q work=%q)", m, ok, head, work)
	}

	// sentinela: só um acerto no estado gravado devolve este número
	if _, err := d.Exec(`UPDATE code_meta SET files = 4242 WHERE repo_root = ?`, key); err != nil {
		t.Fatal(err)
	}
	st, err = Sync(root, SyncBudget)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Fresh || st.Files != 4242 {
		t.Fatalf("estado igual tem de responder do code_meta, sem varredura: %+v", st)
	}
}

// TestSyncReindexesOnExternalChange: arquivo editado por fora muda o estado; o Sync reindexa, a resposta seguinte já
// reflete o repositório e o estado gravado é o novo.
func TestSyncReindexesOnExternalChange(t *testing.T) {
	isolated(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
	})
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	d, err := store()
	if err != nil {
		t.Fatal(err)
	}
	key := repoKey(root)
	_, workBefore, _ := gitState(root)

	// edição por fora, fora do git (o porcelano muda)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc A() {}\n\nfunc A2() { A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Sync(root, SyncBudget)
	if err != nil {
		t.Fatal(err)
	}
	if st.OverBudget || st.Changed != 1 {
		t.Fatalf("a edição por fora tem de entrar no índice: %+v", st)
	}
	if defs, err := Definitions(root, "A2"); err != nil || len(defs) != 1 {
		t.Fatalf("o símbolo novo tem de estar no índice: %+v err=%v", defs, err)
	}
	_, workAfter, _ := gitState(root)
	if workAfter == workBefore {
		t.Fatal("o porcelano tinha de mudar com a edição")
	}
	if m, ok := readMeta(d, key); !ok || m.work != workAfter {
		t.Fatalf("o estado gravado tem de ser o novo: %+v ok=%v", m, ok)
	}
}

// TestSyncOverBudget: estourado o teto, a passada para e o estado gravado é apagado — índice possivelmente
// incompleto nunca é dado como em dia (quem chamou volta à busca real).
func TestSyncOverBudget(t *testing.T) {
	isolated(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
	})
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	d, err := store()
	if err != nil {
		t.Fatal(err)
	}
	key := repoKey(root)
	if _, ok := readMeta(d, key); !ok {
		t.Fatal("a passada completa tinha de gravar o estado")
	}

	// muda o repositório (senão o estado igual responde antes de olhar o teto) e chama com teto impossível
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\n\nfunc B() { A() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Sync(root, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if !st.OverBudget {
		t.Fatalf("com teto de 1ns a passada tem de se declarar incompleta: %+v", st)
	}
	if _, ok := readMeta(d, key); ok {
		t.Fatal("passada incompleta não pode deixar estado gravado: a próxima chamada pularia o que falta")
	}
}

// TestIndexedGatesOnExistingIndex: o índice só existe depois de uma passada (é o que autoriza o grep a responder
// do grafo sem criar índice para quem nunca usou).
func TestIndexedGatesOnExistingIndex(t *testing.T) {
	isolated(t)
	root := gitRepo(t, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})
	if Indexed(root) {
		t.Fatal("sem passada nenhuma o repositório não tem índice")
	}
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	if !Indexed(root) {
		t.Fatal("depois da passada o índice existe")
	}
}

// TestAnswerQueries: os dados crus da resposta pronta — declaração com corpo, contagem de referências por arquivo,
// arquivos de teste pela convenção e busca por nome de arquivo.
func TestAnswerQueries(t *testing.T) {
	isolated(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{
		"store/store.go":  "package store\n\nfunc NewEntry(n string) Entry {\n\treturn Entry{Name: n}\n}\n\ntype Entry struct{ Name string }\n",
		"app/app.go":      "package app\n\nfunc Run() {\n\tNewEntry(\"x\")\n\tNewEntry(\"y\")\n}\n",
		"app/app_test.go": "package app\n\nfunc TestRun(t *testing.T) { NewEntry(\"z\") }\n",
		"cmd/main.go":     "package main\n\nfunc main() { Run() }\n",
		"docs/notes.md":   "NewEntry\n",
	})
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}

	defs, err := Definitions(root, "NewEntry")
	if err != nil || len(defs) != 1 {
		t.Fatalf("esperava uma declaração: %+v err=%v", defs, err)
	}
	if defs[0].File != "store/store.go" || defs[0].Kind != "func" || !strings.Contains(defs[0].Sig, "func NewEntry") {
		t.Fatalf("declaração inesperada: %+v", defs[0])
	}

	body := SymbolBody(root, defs[0], 40)
	if !strings.Contains(body, "return Entry{Name: n}") {
		t.Fatalf("o corpo é o que o agente pediria em seguida: %q", body)
	}
	if SymbolBody(root, defs[0], 1) != "" {
		t.Fatal("corpo maior que o teto não é entregue: vira faixa de linhas")
	}

	counts, err := CallerFileCounts(root, "NewEntry")
	if err != nil {
		t.Fatal(err)
	}
	if counts["app/app.go"] != 2 {
		t.Fatalf("as referências são contadas por arquivo: %+v", counts)
	}
	if !TestFile("app/app_test.go") || TestFile("app/app.go") {
		t.Fatal("arquivo de teste é pela convenção do nome/diretório")
	}

	found := FindFiles(root, "store.go", 10)
	if len(found) != 1 || found[0] != "store/store.go" {
		t.Fatalf("busca por nome de arquivo: %+v", found)
	}
	// o curinga do LIKE é literal: '%' não pode virar "qualquer coisa"
	if got := FindFiles(root, "%", 10); len(got) != 0 {
		t.Fatalf("'%%' tem de ser procurado como caractere, não como curinga: %+v", got)
	}
	// homônimo: o nome existe em dois lugares e a resposta tem de poder dizer isso
	writeTree(t, root, map[string]string{"other/o.go": "package o\n\nfunc NewEntry() int { return 1 }\n"})
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	if defs, err = Definitions(root, "NewEntry"); err != nil || len(defs) != 2 {
		t.Fatalf("esperava duas declarações (homônimo): %+v err=%v", defs, err)
	}
}

// TestSyncReindexesOnExtractorChange: o arquivo está parado no disco, mas o índice foi gravado por outra versão do
// extrator. O frescor por (mtime, hash) pularia justamente esse arquivo, e é por isso que a versão do extrator faz
// parte do estado: a passada seguinte reextrai tudo uma vez — sem isso uma correção de parser nunca chegaria a um
// repositório já indexado.
func TestSyncReindexesOnExtractorChange(t *testing.T) {
	isolated(t)
	root := gitRepo(t, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	d, err := store()
	if err != nil {
		t.Fatal(err)
	}
	key := repoKey(root)
	// índice de um extrator anterior: o símbolo que ele extraiu some (é o que a correção nova traria de volta) e o
	// estado não tem a versão atual.
	if _, err := d.Exec(`DELETE FROM code_symbols WHERE repo_root = ?`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE code_meta SET ev = ? WHERE repo_root = ?`, extractorVersion-1, key); err != nil {
		t.Fatal(err)
	}

	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Changed != 1 {
		t.Fatalf("índice de outra versão tem de ser reextraído mesmo com o arquivo parado: %+v", st)
	}
	if defs, err := Definitions(root, "A"); err != nil || len(defs) != 1 {
		t.Fatalf("o símbolo perdido tinha de voltar: %+v err=%v", defs, err)
	}
	m, ok := readMeta(d, key)
	if !ok || m.ev != extractorVersion {
		t.Fatalf("a passada completa grava a versão do extrator: %+v ok=%v", m, ok)
	}
	// com a versão certa gravada, o arquivo parado volta a ser pulado (a marca é de uma passada só).
	st, err = Refresh(root)
	if err != nil || st.Changed != 0 || !st.Fresh {
		t.Fatalf("com a versão certa o arquivo parado é pulado: %+v %v", st, err)
	}
}
