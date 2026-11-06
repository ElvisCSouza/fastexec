package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outlineOf(t *testing.T, name, src, symbol string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: p, Symbol: symbol})
	if err != nil {
		t.Fatalf("HandleCodeOutline: %v", err)
	}
	return out
}

func TestCodeOutlineGo(t *testing.T) {
	src := "package x\n\n// func Fake() in comment\ntype Server struct {\n\tn int\n}\n\ntype Runner interface {\n\tRun() error\n}\n\nfunc HandleReadFile(args ReadFileArgs) (string, error) {\n\treturn \"\", nil\n}\n\nfunc (s *Server) Run() error {\n\treturn nil\n}\n\n/*\nfunc Hidden() {}\n*/\nfunc init() { http.HandleFunc(\"/health\", h) }\n"
	got := outlineOf(t, "a.go", src, "")
	want := []string{
		"L4: type Server struct",
		"L8: type Runner interface",
		"L12: func HandleReadFile(args ReadFileArgs) (string, error)",
		"L16: func (s *Server) Run() error",
		"L23: func init() { http.HandleFunc(\"/health\", h) }",
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("outline Go:\n%s\nquer:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestCodeOutlineTS(t *testing.T) {
	src := strings.Join([]string{
		"export interface User { id: string }",
		"export type Id = string | number;",
		"export async function loadUser(id: Id): Promise<User> {",
		"  if (id) {",
		"  }",
		"}",
		"export const toId = (x: number): Id => String(x);",
		"export default class UserService {",
		"  private async fetchAll(limit: number): Promise<User[]> {",
		"  }",
		"  for (const x of y) {",
		"  }",
		"}",
		"router.get('/users/:id', handler);",
	}, "\n")
	got := outlineOf(t, "u.ts", src, "")
	for _, want := range []string{
		"L1: export interface User { id: string }",
		"L2: export type Id = string | number;",
		"L3: export async function loadUser(id: Id): Promise<User>",
		"L7: export const toId = (x: number): Id => String(x);",
		"L8: export default class UserService",
		"L9: private async fetchAll(limit: number): Promise<User[]>",
		"L14: router.get('/users/:id', handler);",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
	for _, bad := range []string{"L4:", "L11:"} {
		if strings.Contains(got, bad) {
			t.Errorf("controle de fluxo virou declaração (%s):\n%s", bad, got)
		}
	}
}

func TestCodeOutlinePythonShellPS(t *testing.T) {
	py := outlineOf(t, "m.py", "# def nope():\nclass Repo:\n    async def get(self, id):\n        pass\n\n@app.route('/items')\ndef items():\n    pass\n", "")
	for _, want := range []string{"L2: class Repo:", "L3: async def get(self, id):", "L6: @app.route('/items')", "L7: def items():"} {
		if !strings.Contains(py, want) {
			t.Errorf("py: faltou %q em:\n%s", want, py)
		}
	}
	if strings.Contains(py, "nope") {
		t.Errorf("py: comentário virou declaração:\n%s", py)
	}
	sh := outlineOf(t, "s.sh", "#!/bin/bash\ndeploy() {\n  echo\n}\nfunction backup_db {\n}\n", "")
	if sh != "L2: deploy()\nL5: function backup_db" {
		t.Errorf("sh:\n%s", sh)
	}
	ps := outlineOf(t, "p.ps1", "Function Get-Thing {\n}\n", "")
	if ps != "L1: Function Get-Thing" {
		t.Errorf("ps1:\n%s", ps)
	}
}

func TestCodeOutlineSymbolFilterAndErrors(t *testing.T) {
	src := "package x\nfunc HandleReadFile() {}\nfunc HandleWriteFile() {}\nfunc other() {}\n"
	if got := outlineOf(t, "a.go", src, "readfile"); got != "L2: func HandleReadFile() {}" {
		t.Errorf("filtro: %q", got)
	}
	if got := outlineOf(t, "a.go", src, "nada"); got != `(no declaration matching "nada")` {
		t.Errorf("sem match: %q", got)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "x.rb")
	_ = os.WriteFile(p, []byte("def a; end"), 0o644)
	if _, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: p}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("extensão não suportada deveria falhar: %v", err)
	}
}

func TestCodeOutlineTreeSkipsNoise(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/tools/files.go", "package tools\nfunc HandleReadFile() {}\n")
	write("cmd/main.go", "package main\nfunc main() {}\n")
	write("node_modules/lib/index.js", "function HandleReadFile() {}\n")
	write("vendor/x/y.go", "package y\nfunc HandleReadFile() {}\n")
	write(".git/hooks/h.sh", "HandleReadFile() {\n}\n")
	write("web/app.min.js", "function HandleReadFile(){}\n")

	got, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: root, Symbol: "HandleReadFile"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "internal/tools/files.go:L2: func HandleReadFile() {}" {
		t.Errorf("busca na árvore:\n%s", got)
	}
	all, _ := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: root, Max: 1})
	if !strings.HasPrefix(all, "cmd/main.go:L2: func main() {}") || !strings.Contains(all, "1 more declarations") {
		t.Errorf("outline da árvore com max=1:\n%s", all)
	}
}

// BenchmarkCodeOutlineRepo: busca de símbolo na árvore real do próprio repositório.
func BenchmarkCodeOutlineRepo(b *testing.B) {
	root, _ := filepath.Abs("../..")
	for i := 0; i < b.N; i++ {
		if _, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: root, Symbol: "HandleExec"}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNeedsGrepExcludes(t *testing.T) {
	sim := []string{
		"grep -rn HandleReadFile .",
		"grep -r foo",
		"grep -Rni 'a b' ./",
		"grep --recursive -e foo .",
		"grep -rn foo . 2>/dev/null",
		"grep -rn -A 3 foo .",
	}
	nao := []string{
		"grep -n foo file.go",
		"grep -rn foo src",
		"grep -rn --exclude-dir=node_modules foo .",
		"rg foo",
		"git grep foo",
		"grep -rn foo internal/",
	}
	for _, c := range sim {
		if !needsGrepExcludes(shellWords(c)) {
			t.Errorf("deveria injetar: %s", c)
		}
	}
	for _, c := range nao {
		if needsGrepExcludes(shellWords(c)) {
			t.Errorf("não deveria injetar: %s", c)
		}
	}
}

func TestApplyGrepExcludes(t *testing.T) {
	a := ExecArgs{Command: "  grep -rn foo . | head -5"}
	if !applyGrepExcludes(&a) {
		t.Fatal("command: deveria injetar")
	}
	if !strings.HasPrefix(a.Command, "  grep --exclude-dir=.git --exclude-dir=node_modules") || !strings.HasSuffix(a.Command, "--exclude=pnpm-lock.yaml -rn foo . | head -5") {
		t.Errorf("command reescrito errado: %q", a.Command)
	}
	if strings.Contains(a.Command, "{") {
		t.Errorf("não pode usar expansão de chaves (só bash): %q", a.Command)
	}

	b := ExecArgs{Argv: []string{"grep", "-rn", "foo", "."}}
	if !applyGrepExcludes(&b) || b.Argv[0] != "grep" || b.Argv[1] != "--exclude-dir=.git" || b.Argv[len(b.Argv)-1] != "." {
		t.Errorf("argv reescrito errado: %v", b.Argv)
	}

	c := ExecArgs{Command: "grep -n foo main.go"}
	if applyGrepExcludes(&c) || c.Command != "grep -n foo main.go" {
		t.Errorf("busca não recursiva foi alterada: %q", c.Command)
	}

	t.Setenv("FASTEXEC_NO_SEARCH_GUARD", "1")
	d := ExecArgs{Command: "grep -rn foo ."}
	if applyGrepExcludes(&d) {
		t.Error("guard desligado não deveria injetar")
	}
}

func TestIsTextSearchAndTruncate(t *testing.T) {
	cases := map[string]bool{
		"grep -rn foo .":            true,
		"rg HandleExec":             true,
		"git grep -n foo":           true,
		"findstr /s /i foo *.go":    true,
		"cat a.txt | grep 'x|y'":    true,
		"grep -rn foo . | head -50": false,
		"grep -rn foo . | wc -l":    false,
		"ls -la":                    false,
		"echo grep":                 false,
	}
	for c, want := range cases {
		if got := isTextSearch(c); got != want {
			t.Errorf("isTextSearch(%q) = %v, quer %v", c, got, want)
		}
	}

	var lines []string
	for i := 0; i < 55; i++ {
		lines = append(lines, "f.go:1: x")
	}
	cut, total, truncated := truncateSearchOutput(strings.Join(lines, "\n") + "\n")
	if !truncated || total != 55 || strings.Count(cut, "\n") != searchMaxLines {
		t.Errorf("corte: truncated=%v total=%d linhas=%d", truncated, total, strings.Count(cut, "\n"))
	}
	if _, _, truncated := truncateSearchOutput(strings.Join(lines[:40], "\n")); truncated {
		t.Error("40 linhas não deveriam ser cortadas")
	}
	if h := searchTruncatedHint(55); !strings.Contains(h, "55 lines") || !strings.Contains(h, "code_outline") {
		t.Errorf("dica: %q", h)
	}
}
