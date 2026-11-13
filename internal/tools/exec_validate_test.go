package tools

import (
	"os"
	"strings"
	"testing"
)

// D9: o exec gravou um .go com sintaxe quebrada → linha ✗ ao fim da resposta.
func TestExecWatchFlagsBrokenWrittenFile(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	cmd := "cat > " + f.path("e.go") + " <<'EOF'"
	w := execWatchBegin("local", cmd, f.root)
	if w == nil {
		t.Fatal("a command mentioning a .go file must be watched")
	}
	if err := os.WriteFile(f.path("e.go"), []byte("package main\n\nfunc F() {\n\tx := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := w.Finish()
	if !strings.Contains(got, "✗ e.go:") {
		t.Fatalf("broken file written by exec must be reported, got:\n%q", got)
	}
}

// Arquivo só mencionado (lido) não muda de mtime/tamanho: silêncio.
func TestExecWatchIgnoresUnchangedMention(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	f.write("a.go", "package main\n\nfunc F() {\n")
	w := execWatchBegin("local", "cat "+f.path("a.go"), f.root)
	if got := w.Finish(); got != "" {
		t.Fatalf("unchanged file must add nothing, got:\n%q", got)
	}
}

// Linha sem menção a .go/.cs não é vigiada (custo zero no exec comum).
func TestExecWatchNoMentionNoWatch(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	if w := execWatchBegin("local", "echo oi", f.root); w != nil {
		t.Fatal("no code file mentioned, must not watch")
	}
}

// FASTEXEC_LSP=off desliga.
func TestExecWatchOff(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{})
	t.Setenv("FASTEXEC_LSP", "off")
	if w := execWatchBegin("local", "cat > "+f.path("e.go"), f.root); w != nil {
		t.Fatal("FASTEXEC_LSP=off must disable the watch")
	}
}
