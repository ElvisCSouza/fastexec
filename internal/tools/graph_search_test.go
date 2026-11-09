package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIdentSearch(t *testing.T) {
	yes := map[string]string{
		`grep -rn HandleReadFile .`:               "HandleReadFile",
		`grep -rn "func HandleReadFile" internal`: "HandleReadFile",
		`rg 'HandleReadFile\('`:                   "HandleReadFile",
		`rg -n -t go \bloadUser\b src`:            "loadUser",
		`git grep -n PedidoService`:               "PedidoService",
		`findstr /s /n HandleExec *.go`:           "HandleExec",
	}
	for cmd, want := range yes {
		r, ok := parseIdentSearch(cmd)
		if !ok || r.pattern != want {
			t.Errorf("%q: got %+v ok=%v, want %s", cmd, r, ok, want)
		}
	}
	no := []string{
		`grep -n HandleReadFile files.go`,          // não recursivo
		`grep -rn "connection refused" .`,          // texto
		`grep -rln HandleReadFile .`,               // -l: lista de arquivos
		`grep -rvn HandleReadFile .`,               // -v
		`grep -rin handlereadfile .`,               // -i
		`grep -rn HandleReadFile . | wc -l`,        // pipeline
		`grep -rn --include=*.md HandleReadFile .`, // só docs
		`findstr /n HandleExec *.go`,               // sem /s
		`ls -la`,
	}
	for _, cmd := range no {
		if r, ok := parseIdentSearch(cmd); ok {
			t.Errorf("%q must not be a graph search: %+v", cmd, r)
		}
	}
}

func TestGraphSearchAnswersAndRepeats(t *testing.T) {
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "pkg", "a.go"), []byte("package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n"), 0o644)

	args := ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: repo, Caller: "test#gs1"}
	out, handled, note := tryGraphSearch(args, args.Command)
	if !handled || note != "" {
		t.Fatalf("expected a graph answer, got handled=%v note=%q", handled, note)
	}
	if !strings.HasPrefix(out, GraphMarkSearch) || !strings.Contains(out, "pkg/a.go:3-5") || !strings.Contains(out, "main.go:4") {
		t.Fatalf("graph answer:\n%s", out)
	}
	_, handled, note = tryGraphSearch(args, args.Command)
	if handled || !strings.Contains(note, GraphMarkRepeat) {
		t.Fatalf("repeat must run the raw search: handled=%v note=%q", handled, note)
	}

	// identificador que não é declarado no código: busca normal
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn NotDeclaredAnywhere .", Cwd: repo, Caller: "test#gs1"}, "grep -rn NotDeclaredAnywhere ."); handled {
		t.Fatal("unknown identifier must fall back to the raw search")
	}
	// fora de repositório git: busca normal
	plain := t.TempDir()
	_ = os.WriteFile(filepath.Join(plain, "a.go"), []byte("package x\nfunc CalcTotal() {}\n"), 0o644)
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: plain}, "grep -rn CalcTotal ."); handled {
		t.Fatal("outside a git repository the search must run as usual")
	}
}
