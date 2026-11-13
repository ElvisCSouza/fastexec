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
		"L4-L6: type Server struct",
		"L8-L10: type Runner interface",
		"L12-L14: func HandleReadFile(args ReadFileArgs) (string, error)",
		"L16-L18: func (s *Server) Run() error", // fim real do bloco (chaves balanceadas)
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
		"L3-L6: export async function loadUser(id: Id): Promise<User>",
		"L7: export const toId = (x: number): Id => String(x);",
		"L8-L13: export default class UserService",
		"L9-L10: private async fetchAll(limit: number): Promise<User[]>",
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
	for _, want := range []string{"L2-L4: class Repo:", "L3-L4: async def get(self, id):", "L6: @app.route('/items')", "L7-L8: def items():"} {
		if !strings.Contains(py, want) {
			t.Errorf("py: faltou %q em:\n%s", want, py)
		}
	}
	if strings.Contains(py, "nope") {
		t.Errorf("py: comentário virou declaração:\n%s", py)
	}
	sh := outlineOf(t, "s.sh", "#!/bin/bash\ndeploy() {\n  echo\n}\nfunction backup_db {\n}\n", "")
	if sh != "L2-L4: deploy()\nL5-L6: function backup_db" {
		t.Errorf("sh:\n%s", sh)
	}
	ps := outlineOf(t, "p.ps1", "Function Get-Thing {\n}\n", "")
	if ps != "L1-L2: Function Get-Thing" {
		t.Errorf("ps1:\n%s", ps)
	}
}

func TestCodeOutlineCSharp(t *testing.T) {
	src := strings.Join([]string{
		"using Core.Models;",
		"",
		"namespace MyApp.Services;",
		"",
		"public partial class PedidoService : IRepositorio<Pedido>",
		"{",
		"    public int Id { get; set; }",
		"",
		"    public void Salvar(Pedido item)",
		"    {",
		"        // Comentario: Falso();",
		"        Gravar();",
		"    }",
		"",
		"    private void Gravar() { }",
		"}",
		"",
	}, "\n")
	got := outlineOf(t, "PedidoService.cs", src, "")
	for _, want := range []string{
		"L3-L16: namespace MyApp.Services",
		"L5-L16: public partial class PedidoService : IRepositorio<Pedido>",
		"L7: public int Id { get; set; }",
		"L9-L13: public void Salvar(Pedido item)",
		"L15: private void Gravar() { }",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("outline cs: faltou %q em:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Falso") {
		t.Errorf("outline cs: comentário virou declaração:\n%s", got)
	}
	if got := outlineOf(t, "PedidoService.cs", src, "salvar"); got != "L9-L13: public void Salvar(Pedido item)" {
		t.Errorf("outline cs com filtro: %q", got)
	}
	proj := outlineOf(t, "Core.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n  </PropertyGroup>\n</Project>", "")
	if !strings.HasPrefix(proj, "L1-L5: csproj ") || !strings.HasSuffix(proj, "Core.csproj") {
		t.Errorf("outline csproj: %q", proj)
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
		"grep -rn --exclude-dir=.git foo .", // só .git excluído: bin/obj ainda entram
	}
	nao := []string{
		"grep -n foo file.go",
		"grep -rn foo src",
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

func TestIsMemberSearch(t *testing.T) {
	cases := map[string]bool{
		`findstr /s /n /c:"SelfLog.WriteLine" *.cs`:       true,
		`grep -rn "SelfLog\.WriteLine(" --include=*.cs .`: true,
		`grep -rn "foo.bar" .`:                            false,
		`grep -rn "TODO" src`:                             false,
		`dir /s Program.cs`:                               false,
		`cd /w/Data/x && grep -rn "SelfLog\.WriteLine" --include=*.cs . | head -100`: true,
		`cd /w/Data/x && grep -rn "WriteLine" --include=*.cs src test | grep -v "SelfLog.WriteLine"`: true,
		`grep -rn "SelfLog" test/Serilog.ApprovalTests/Serilog.approved.txt`: false,
	}
	for c, want := range cases {
		if got := isMemberSearch(c); got != want {
			t.Errorf("isMemberSearch(%q) = %v, quer %v", c, got, want)
		}
	}
}

func TestIsCallSearch(t *testing.T) {
	cases := map[string]bool{
		`grep -rn "HandleHandoff(" .`: true,
		`rg 'HandleHandoff\('`:        true,
		`git grep -n "Init("`:         true,
		`grep -rn HandleHandoff .`:    false,
		`echo foo(`:                   false,
		`grep -E "(a|b)" x.txt`:       false,
		`grep -rn "if (" .`:           false,
	}
	for c, want := range cases {
		if got := isCallSearch(c); got != want {
			t.Errorf("isCallSearch(%q) = %v, quer %v", c, got, want)
		}
	}
}

func TestIsEditScript(t *testing.T) {
	cases := map[string]bool{
		`pwsh -NoProfile -Command cd W:\x; (Get-Content a.wh.cpp -Raw) -replace '@version 1', '@version 2' | Set-Content a.wh.cpp`: true,
		`pwsh -Command $t=[IO.File]::ReadAllText('a.cpp'); [IO.File]::WriteAllText('a.cpp', $t.Replace('a','b'))`:                  true,
		`sed -i 's/a/b/' main.go`:              true,
		`sed -n '1,20p' main.go`:               false,
		`Get-Content a.txt | Select -First 5`:  false,
		`(Get-Content a.txt) -replace 'a','b'`: false, // só mostra, não grava
	}
	for c, want := range cases {
		if got := isEditScript(c); got != want {
			t.Errorf("isEditScript(%q) = %v, quer %v", c, got, want)
		}
	}
}

func TestIsDeclSearch(t *testing.T) {
	cases := map[string]bool{
		`findstr /n /c:"void Position" /c:"void Invalidate" GpuNavPane.wh.cpp`:     true,
		`grep -n "func HandleExec" internal/tools/exec.go`:                         true,
		`pwsh -NoProfile -Command Select-String -Path a.cpp -Pattern 'struct Row'`: true,
		`grep -rn "Invalidate(" .`:                                                 false,
		`echo "void Position"`:                                                     false,
		`findstr /n /c:"g_overlayAtom" a.cpp`:                                      false,
	}
	for c, want := range cases {
		if got := isDeclSearch(c); got != want {
			t.Errorf("isDeclSearch(%q) = %v, quer %v", c, got, want)
		}
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
		// PowerShell: Select-Object só limita com -First/-Last/-Index (regressão do T6-B: listagem de 10 KB sem corte)
		`powershell -c "Get-ChildItem -Recurse -Filter *.cs . | Select-String -Pattern 'fee' -List | Select-Object -ExpandProperty Path"`: true,
		`powershell -c "Get-ChildItem -Recurse . | Select-String -Pattern 'fee' | Select-Object -First 30"`:                               false,
		`pwsh -c "Select-String -Path *.cs -Pattern fee | select -Last 5"`:                                                                false,
		`pwsh -c "Select-String -Path *.cs -Pattern fee | Select-Object Path,Line"`:                                                       true,
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
