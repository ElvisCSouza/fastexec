package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

func TestWaitReportIncremental(t *testing.T) {
	key := "test|job1"
	first := waitReport("job1", key, "a\nb\nc", false, "")
	if !strings.Contains(first, "a\nb\nc") {
		t.Fatalf("first wait must show the log: %q", first)
	}
	same := waitReport("job1", key, "a\nb\nc", false, "")
	if !strings.Contains(same, "no new output since the last wait") || strings.Contains(same, "\na\n") {
		t.Fatalf("repeat wait must not resend the log: %q", same)
	}
	more := waitReport("job1", key, "b\nc\nd\ne", true, "exited")
	if !strings.Contains(more, "finished: exited") || !strings.Contains(more, "(2 new line(s) since the last wait; job(action='tail') for the full log)\nd\ne") || strings.Contains(more, "\nb\n") {
		t.Fatalf("only new lines expected: %q", more)
	}
}

func TestNearMiss(t *testing.T) {
	file := "func a() {\n\tx := 1\n\ty := 2\n\treturn x + y\n}\n"
	h := nearMiss(file, "func a() {\n\tx := 1\n\ty := 3\n\treturn x + y\n}")
	if !strings.Contains(h, "lines 1-5 (4/5 lines equal)") || !strings.Contains(h, "file:   y := 2") {
		t.Fatalf("closest window: %q", h)
	}
	if h := nearMiss(file, "func a() {\n    x := 1\n    y := 2\n"); !strings.Contains(h, "whitespace/indentation") {
		t.Fatalf("indentation only: %q", h)
	}
	if h := nearMiss(`path := "C:\dir"`+"\n", `path := "C:\\dir"`); !strings.Contains(h, "over-escapes backslashes") {
		t.Fatalf("backslashes: %q", h)
	}
	if h := nearMiss(file, "nothing like this at all"); h != "" {
		t.Fatalf("no hint expected: %q", h)
	}
}

func TestEditFileNotFoundShowsNearMiss(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	_ = os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644)
	_, err := HandleEditFile(EditFileArgs{Path: p, Target: "one\nTWO-x\nthree", Replacement: "z"})
	if err == nil || !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), "closest match is lines 1-3") {
		t.Fatalf("near-miss missing: %v", err)
	}
}

func TestSuggestPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Config.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "server.go"), []byte(""), 0o644)
	got := suggestMissing(filepath.Join(dir, "config.json"))
	if len(got) == 0 || !strings.HasSuffix(got[0].path, "Config.json") {
		t.Fatalf("case difference: %v", got)
	}
	got = suggestMissing(filepath.Join(dir, "servr.go"))
	if len(got) == 0 || !strings.HasSuffix(got[0].path, "server.go") {
		t.Fatalf("typo: %v", got)
	}
	got = suggestMissing(filepath.Join(dir, "nope", "deeper", "x.txt"))
	if len(got) != 1 || got[0].note != "nearest existing folder" {
		t.Fatalf("nearest parent: %v", got)
	}
	_, err := HandleReadFile(ReadFileArgs{Path: filepath.Join(dir, "servr.go")})
	if err == nil || !strings.Contains(err.Error(), "not found; did you mean:") || !strings.Contains(err.Error(), "server.go") {
		t.Fatalf("read_file must suggest: %v", err)
	}
}

// TestSugestaoPeloSimboloDoGrafo é o caso CS8: o nome do arquivo pedido não existe e NÃO parecido com nenhum nome
// da pasta — quem sabe onde ele mora é o índice, que registrou o símbolo. O conteúdo do outro arquivo não é
// devolvido: só o caminho candidato.
func TestSugestaoPeloSimboloDoGrafo(t *testing.T) {
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "src", "Formatting", "Display", "LevelOutputFormat.cs")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "namespace X\n{\n    public class Casing\n    {\n        public string Format(int level) { return \"\"; }\n    }\n}\n"
	if err := os.WriteFile(real, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Refresh(root); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "src", "Formatting", "Display", "Casing.cs")
	out, err := HandleReadFile(ReadFileArgs{Path: missing})
	if err == nil {
		t.Fatalf("arquivo inexistente não pode devolver conteúdo: %q", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found; did you mean:") || !strings.Contains(msg, "LevelOutputFormat.cs") || !strings.Contains(msg, "(declares Casing)") {
		t.Fatalf("sugestão pelo símbolo ausente: %v", err)
	}
	if strings.Contains(out, "public class Casing") {
		t.Fatal("o conteúdo de outro arquivo não pode ser devolvido")
	}
}

// TestSemCandidatosMantemAMensagem: sem nada parecido e sem símbolo no índice, a mensagem é a de antes (nenhuma
// linha "did you mean" vazia).
func TestSemCandidatosMantemAMensagem(t *testing.T) {
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Refresh(root); err != nil {
		t.Fatal(err)
	}
	_, err := HandleReadFile(ReadFileArgs{Path: filepath.Join(root, "zzzqqqwwxy.md")})
	if err == nil || !strings.Contains(err.Error(), "zzzqqqwwxy.md") {
		t.Fatalf("mensagem de sempre esperada: %v", err)
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("sem candidatos não pode haver sugestão: %v", err)
	}
}

func TestEditBatchHint(t *testing.T) {
	if editBatchHint("c#1", "f.go", 1) != "" {
		t.Fatal("first edit: no hint")
	}
	if !strings.Contains(editBatchHint("c#1", "f.go", 1), "edits=[") {
		t.Fatal("second edit in a row: hint")
	}
	if editBatchHint("c#1", "f.go", 1) != "" {
		t.Fatal("hint only once per file")
	}
	if editBatchHint("c#2", "g.go", 1) != "" || editBatchHint("c#3", "g.go", 1) != "" {
		t.Fatal("different channels must not count together")
	}
}
