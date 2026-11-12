package tools

// code_graph_pasta_avulsa_test.go: o grafo numa pasta que não é repositório git (M2/M3 do ciclo 4). A pasta pequena
// é indexada como avulsa e o retorno AVISA; a grande demais continua recusada com a mensagem de sempre, com o
// caminho que o agente pediu; dentro de git nada muda (sem aviso).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// pastaSemGit monta a pasta avulsa e pula o teste quando o ambiente tem .git acima do TempDir (aí não é avulsa).
func pastaSemGit(t *testing.T, files map[string]string) string {
	t.Helper()
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	dir := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if root, ok := graph.ResolveRepoRoot(dir); ok {
		t.Skipf("ambiente com .git acima do TempDir (%s): teste não se aplica", root)
	}
	return dir
}

func TestCodeGraphPastaAvulsaAvisa(t *testing.T) {
	dir := pastaSemGit(t, map[string]string{"a.go": "package x\n\nfunc A() {}\n\nfunc B() { A() }\n"})

	out, err := HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: dir})
	if err != nil {
		t.Fatalf("pasta avulsa pequena deveria ser indexada: %v", err)
	}
	const aviso = "[graph] not a git repository: indexed the folder as standalone (1 files); results reflect the files on disk now.\n"
	if !strings.HasPrefix(out, aviso) {
		t.Fatalf("aviso da pasta avulsa ausente:\n%s", out)
	}
	if !strings.Contains(out, "a.go:5") {
		t.Fatalf("resposta do grafo esperada:\n%s", out)
	}

	// Segunda chamada com o índice em dia: o aviso continua (o agente precisa saber que aquilo não é um repositório).
	out, err = HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: dir})
	if err != nil || !strings.HasPrefix(out, aviso) {
		t.Fatalf("aviso deve acompanhar toda resposta:\n%s %v", out, err)
	}

	// index continua funcionando na pasta avulsa.
	if out, err = HandleCodeGraph(CodeGraphArgs{Action: "index", RepoPath: dir}); err != nil || !strings.HasPrefix(out, aviso) {
		t.Fatalf("action index na pasta avulsa:\n%s %v", out, err)
	}
}

func TestCodeGraphPastaAvulsaGrandeRecusaAMesmaMensagem(t *testing.T) {
	dir := pastaSemGit(t, map[string]string{
		"a.go": "package x\n\nfunc A() {}\n",
		"b.go": "package x\n\nfunc B() {}\n",
		"c.go": "package x\n\nfunc C() {}\n",
	})
	t.Cleanup(graph.SetStandaloneLimits(2, 0))

	_, err := HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: dir})
	want := "'" + dir + "' is not inside a git repository: code_graph only indexes git repositories (outside them use code_outline or exec)"
	if err == nil || err.Error() != want {
		t.Fatalf("recusa de sempre esperada:\n%v", err)
	}
	// A mensagem cita o caminho pedido, não a pasta resolvida: é o que o agente reconhece.
	_, err = HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: filepath.Join(dir, "b.go")})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "b.go")) {
		t.Fatalf("recusa deve citar o caminho pedido: %v", err)
	}
}

func TestCodeGraphForaDePastaRecusa(t *testing.T) {
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	missing := filepath.Join(t.TempDir(), "nao", "existe")
	_, err := HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: missing})
	if err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("caminho inexistente deveria recusar: %v", err)
	}
}

// TestCodeGraphRepositorioGitSemAviso é a regressão: dentro de git a resposta sai como sempre saiu.
func TestCodeGraphRepositorioGitSemAviso(t *testing.T) {
	root := pastaSemGit(t, map[string]string{"a.go": "package x\n\nfunc A() {}\n\nfunc B() { A() }\n"})
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := HandleCodeGraph(CodeGraphArgs{Action: "callers", Symbol: "A", RepoPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "standalone") {
		t.Fatalf("repositório git não pode receber o aviso:\n%s", out)
	}
	if !strings.Contains(out, "a.go:5") {
		t.Fatalf("resposta do grafo esperada:\n%s", out)
	}
}
