package tools

// bg_index_test.go: o gancho que dispara a indexação de fundo a partir de uma chamada de tool — quem dispara (cwd
// local dentro de repositório git), quem não dispara (host remoto, pasta fora de git) e o silêncio em argumento que
// não diz caminho nenhum.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"homelab/fastexec/internal/graph"
)

// bgRepo cria um repositório git de verdade com um arquivo indexável.
func bgRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível: sem repositório não há indexação de fundo")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

// bgIndexado espera a passada de fundo indexar a raiz (ou desiste no prazo).
func bgIndexado(root string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if graph.TrackedFiles(root) > 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func bgArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestKickBackgroundIndexFromToolCall: um exec com cwd dentro de um repositório git indexa aquele repositório em
// segundo plano, antes de o modelo perguntar qualquer coisa ao grafo.
func TestKickBackgroundIndexFromToolCall(t *testing.T) {
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := bgRepo(t)

	KickBackgroundIndex("exec", bgArgs(t, map[string]any{"command": "go build", "cwd": root}))
	if !bgIndexado(root, 15*time.Second) {
		t.Fatal("a chamada com cwd no repositório não disparou a indexação de fundo")
	}

	// read_file com o caminho de um arquivo do repositório vale como o mesmo sinal (outro repositório: o já
	// indexado não dispara de novo por causa da histerese).
	root2 := bgRepo(t)
	KickBackgroundIndex("read_file", bgArgs(t, map[string]any{"path": filepath.Join(root2, "a.go")}))
	if !bgIndexado(root2, 15*time.Second) {
		t.Fatal("read_file dentro do repositório não disparou a indexação de fundo")
	}
}

// TestKickBackgroundIndexIgnoresRemoteHost: host remoto não indexa nada local — o índice descreve o disco daqui.
func TestKickBackgroundIndexIgnoresRemoteHost(t *testing.T) {
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := bgRepo(t)

	KickBackgroundIndex("exec", bgArgs(t, map[string]any{"command": "ls", "host": "noteserver", "cwd": root}))
	KickBackgroundIndex("read_file", bgArgs(t, map[string]any{"host": "noteserver", "path": "noteserver:" + filepath.ToSlash(filepath.Join(root, "a.go"))}))
	if bgIndexado(root, 500*time.Millisecond) {
		t.Fatal("chamada em host remoto não deveria indexar o disco local")
	}
}

// TestKickBackgroundIndexWithoutPath: argumento sem caminho nenhum não dispara nem reclama (a tool de verdade é
// quem responde ao modelo).
func TestKickBackgroundIndexWithoutPath(t *testing.T) {
	dir := t.TempDir()
	graph.SetDBPath(filepath.Join(dir, "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })

	KickBackgroundIndex("read_file", bgArgs(t, map[string]any{"start_line": 1}))
	KickBackgroundIndex("exec", json.RawMessage(`{`)) // argumento quebrado: silêncio, não pânico
	if _, _, ok := graph.Building(filepath.Join(dir, "nao-existe")); ok {
		t.Fatal("nenhuma passada deveria existir")
	}
}
