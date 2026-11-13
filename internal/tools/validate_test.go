package tools

import (
	"os"
	"strings"
	"testing"
	"time"
)

// create grava um arquivo NOVO pelo mesmo caminho do MCP (write_file sem overwrite).
func (f *lspFixture) create(name, content string) string {
	f.t.Helper()
	msg, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: f.path(name), Content: content})
	if err != nil {
		f.t.Fatalf("write_file %s: %v", name, err)
	}
	return msg
}

// Arquivo novo com sintaxe quebrada: erro exato da camada 1, com a linha de código, e a camada 2 não roda.
func TestValidateNewFileSyntaxError(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	msg := f.create("n.go", "package main\n\nfunc F() {\n\tx := 1\n")
	if !strings.Contains(msg, "✗ n.go:") || !strings.Contains(msg, "│ ") {
		t.Fatalf("syntax error with code line missing, got:\n%s", msg)
	}
	if strings.Contains(msg, "compila") || strings.Contains(msg, "pendentes") {
		t.Fatalf("with a syntax error the semantic layer must not speak, got:\n%s", msg)
	}
}

// Arquivo novo correto e servidor quente: "✓ compila". Warning do servidor nunca aparece.
func TestValidateNewFileCompilesAndWarningsNeverShow(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	msg := f.create("n.go", "package main\n\nfunc F() {}\n//WARN unused thing\n")
	if !strings.Contains(msg, "✓ compila") {
		t.Fatalf("want ✓ compila, got:\n%s", msg)
	}
	if strings.Contains(msg, "unused thing") {
		t.Fatalf("a warning leaked, got:\n%s", msg)
	}
}

// Arquivo novo: TODO erro do servidor conta como novo (não há "antes") e leva a linha de código.
func TestValidateNewFileSemanticError(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	msg := f.create("n.go", "package main\n\nfunc F() {}\n//ERR undefined: conta\n")
	if !strings.Contains(msg, "✗ n.go:4") || !strings.Contains(msg, "undefined: conta") {
		t.Fatalf("semantic error on a new file missing, got:\n%s", msg)
	}
	if strings.Contains(msg, "✓ compila") {
		t.Fatalf("must not claim it compiles, got:\n%s", msg)
	}
}

// Servidor frio/mudo: a resposta nunca é silêncio.
func TestValidateColdServerSaysPending(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{Hang: []string{"textDocument/diagnostic"}, NoPush: true}).start()
	start := time.Now()
	msg := f.create("n.go", "package main\n\nfunc F() {}\n")
	if !strings.Contains(msg, "✓ sintaxe · tipos pendentes (servidor carregando)") {
		t.Fatalf("pending status missing, got:\n%s", msg)
	}
	if time.Since(start) > lspAutoDiagBudget+2*time.Second {
		t.Fatalf("the edit waited past its budget: %v", time.Since(start))
	}
}

// Arquivo fora de qualquer projeto: só sintaxe, sem erro de tipo inventado.
func TestValidateFileOutsideProjectOnlySyntax(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	if err := os.Remove(f.path("go.mod")); err != nil {
		t.Fatal(err)
	}
	msg := f.create("n.go", "package main\n\nfunc F() {}\n//ERR undefined: conta\n")
	if strings.Contains(msg, "undefined: conta") {
		t.Fatalf("a type error from a loose file must not be shown, got:\n%s", msg)
	}
	if !strings.Contains(msg, "arquivo fora do projeto") {
		t.Fatalf("status missing, got:\n%s", msg)
	}
}

// Sem git mas com go.mod: o marcador prova, de forma determinística, que é um projeto Go; a camada 2 roda.
func TestValidateWithoutGitUsesProjectMarkers(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	if err := os.RemoveAll(f.path(".git")); err != nil {
		t.Fatal(err)
	}
	msg := f.create("n.go", "package main\n\nfunc F() {}\n//ERR undefined: conta\n")
	if !strings.Contains(msg, "undefined: conta") {
		t.Fatalf("semantic layer must run on a marker-rooted project without git, got:\n%s", msg)
	}
	if strings.Contains(msg, "fora do projeto") {
		t.Fatalf("a go.mod project must not be reported as outside any project, got:\n%s", msg)
	}
}

// FASTEXEC_LSP=off desliga tudo: nenhuma linha nova.
func TestValidateOffMeansNothing(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{})
	t.Setenv("FASTEXEC_LSP", "off")
	msg := f.create("n.go", "package main\n\nfunc F() {\n")
	if strings.Contains(msg, "\n") {
		t.Fatalf("FASTEXEC_LSP=off must add no line, got:\n%q", msg)
	}
}

// Erro que já existia antes da edição não aparece de novo; o que a edição criou, sim.
func TestValidateSyntaxPreexistingNotRepeated(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	f.write("a.go", "package main\n\nfunc F() {\n")
	msg := f.edit("a.go", "package main\n\nfunc F() {\n\t// comentário\n")
	if strings.Contains(msg, "✗ ") {
		t.Fatalf("a pre-existing syntax error was announced again, got:\n%s", msg)
	}
	if strings.Contains(msg, "✓") {
		t.Fatalf("a file with a pre-existing syntax error must not claim ✓, got:\n%s", msg)
	}
}

// Mais de 3 erros: três linhas e "+N erros".
func TestValidateCapsErrors(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	body := "package main\n\nfunc F() {}\n" +
		"//ERR e1\n//ERR e2\n//ERR e3\n//ERR e4\n//ERR e5\n"
	msg := f.create("n.go", body)
	if got := strings.Count(msg, "✗ "); got != validateMaxErrs {
		t.Fatalf("want %d error lines, got %d:\n%s", validateMaxErrs, got, msg)
	}
	if !strings.Contains(msg, "+2 erros") {
		t.Fatalf("want +2 erros, got:\n%s", msg)
	}
}

// D8: o erro que a edição causou em OUTRO arquivo sai com "(efeito da sua edição)"; o que já era erro lá, não.
func TestValidateDependentsShowOnlyNewErrors(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{}).start()
	f.write("a.go", "package main\n\nfunc ParseLine() {}\n")
	f.write("b.go", "package main\n\nfunc one() { ParseLine() }\n//ERR old problem\n")
	f.write("c.go", "package main\n\nfunc two() { ParseLine() }\n")
	a := &autoEdit{host: "local", abs: f.path("a.go"), root: f.root, rel: "a.go", client: f.client, lang: "go",
		delta: declDelta{kind: declSigned, name: "ParseLine"}}
	a.captureDeps([]string{f.path("b.go"), f.path("c.go")})
	// a edição quebra os dois chamadores
	f.write("b.go", "package main\n\nfunc one() { ParseLine() }\n//ERR old problem\n//ERR not enough arguments in call to ParseLine\n")
	f.write("c.go", "package main\n\nfunc two() { ParseLine() }\n//ERR not enough arguments in call to ParseLine\n")
	if un := a.checkDependents(); un != 0 {
		t.Fatalf("all dependents should have been verified, %d were not", un)
	}
	joined := strings.Join(a.extra, "\n")
	if strings.Contains(joined, "old problem") {
		t.Fatalf("a pre-existing error in a dependent was blamed on the edit:\n%s", joined)
	}
	if !strings.Contains(joined, "b.go:") || !strings.Contains(joined, "c.go:") ||
		strings.Count(joined, "(efeito da sua edição)") != 2 {
		t.Fatalf("dependent errors missing:\n%s", joined)
	}
}

// Entrega tardia: sai uma vez, na próxima chamada.
func TestTakeLateDeliversOnce(t *testing.T) {
	TakeLate() // limpa o que outro teste deixou
	queueLate("⏱ diagnóstico tardio x.go: ✓ compila (tipos verificados)")
	if got := TakeLate(); !strings.Contains(got, "diagnóstico tardio") {
		t.Fatalf("late item missing: %q", got)
	}
	if got := TakeLate(); got != "" {
		t.Fatalf("a late item must be delivered once, second take: %q", got)
	}
}

func TestSrcLineTrimsAndCaps(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := srcLine("a\n\t  "+long+"  \nc\n", 2)
	if r := []rune(got); len(r) != validateCodeMax || !strings.HasSuffix(got, "…") {
		t.Fatalf("code line not capped to %d: %d %q", validateCodeMax, len(r), got)
	}
	if srcLine("a\nb\n", 9) != "" {
		t.Fatal("a line past the end must be empty")
	}
}
