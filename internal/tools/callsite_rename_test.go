package tools

import (
	"strings"
	"testing"
)

func TestCallSiteDelta(t *testing.T) {
	ok := func(old, new, name, newName, qual string) {
		t.Helper()
		d, good := callSiteDelta(old, new)
		if !good || d.name != name || d.newName != newName || d.qual != qual || !d.site || d.kind != declRenamed {
			t.Fatalf("want swap %s->%s (qual %q), got %+v ok=%v", name, newName, qual, d, good)
		}
	}
	no := func(old, new string) {
		t.Helper()
		if d, good := callSiteDelta(old, new); good {
			t.Fatalf("must not be a call-site rename: %q -> %q (%+v)", old, new, d)
		}
	}
	ok("a\n    SelfLog.WriteLine(\"x\");\n", "a\n    SelfLog.Write(\"x\");\n", "WriteLine", "Write", "SelfLog")
	// o mesmo par em várias linhas
	ok("SelfLog.WriteLine(1);\nx;\nSelfLog.WriteLine(2);\n", "SelfLog.Write(1);\nx;\nSelfLog.Write(2);\n", "WriteLine", "Write", "SelfLog")
	// coluna em UTF-16: o acento antes do uso conta uma unidade
	d, _ := callSiteDelta("var é = SelfLog.WriteLine(1);", "var é = SelfLog.Write(1);")
	if d.col != 17 {
		t.Fatalf("col = %d, want 17 (the é is two bytes but one UTF-16 unit)", d.col)
	}
	no("public void M() {}", "private void M() {}")      // palavra-chave, sem ponto
	no("a.Foo(1);\nb.Bar(2);", "a.Foo2(1);\nb.Bar2(2);") // pares diferentes
	no("SelfLog.WriteLine(1);", "SelfLog.Write(1, 2);")  // mais que o nome
	no("WriteLine(1);", "Write(1);")                     // chamada sem ponto: sem âncora de membro
	no("SelfLog.WriteLine(1);", "SelfLog.WriteLine(1);") // nada mudou
	no("x.A(1);\n", "x.A(1);\ny;\n")                     // linhas a mais
}

func TestGrepLedger(t *testing.T) {
	noteGrep("c1", `findstr /s /n /c:"SelfLog.WriteLine" *.cs`, "src\\A.cs:3: SelfLog.WriteLine(1)\nsrc\\A.cs:9: SelfLog.WriteLine(2)\nC:\\r\\B.cs:1: SelfLog.WriteLine(3)\n")
	g, ok := grepFor("c1", "SelfLog.WriteLine")
	if !ok || g.lines != 3 || g.files != 2 {
		t.Fatalf("grep note = %+v ok=%v, want 3 lines in 2 files", g, ok)
	}
	noteGrep("c2", `grep -rn "SelfLog\.WriteLine" .`, "a.cs:1:x\n")
	if _, ok := grepFor("c2", "SelfLog.WriteLine"); !ok {
		t.Fatal("escaped dot must match the plain name")
	}
	if _, ok := grepFor("c3", "SelfLog.WriteLine"); ok {
		t.Fatal("another channel must not see it")
	}
	if _, ok := grepFor("", "SelfLog.WriteLine"); ok {
		t.Fatal("unknown identity must not match")
	}
}

const (
	siteLib  = "package main\n\ntype L struct{}\n\nfunc (L) WriteLine() {}\n"
	siteOld  = "package main\n\nfunc main() { L{}.WriteLine() }\n"
	siteNew  = "package main\n\nfunc main() { L{}.Write() }\n"
	siteEdit = "func main() { L{}.WriteLine() }"
)

func siteCtl(def string) toolFakeCtl {
	return toolFakeCtl{
		Definition: def,
		Rename: map[string][]toolFakeEdit{
			"":       {{Line: 2, Start: 18, End: 27, New: "Write"}},
			"lib.go": {{Line: 4, Start: 9, End: 18, New: "Write"}},
		},
	}
}

func siteEditArgs(f *lspFixture) EditFileArgs {
	return EditFileArgs{Host: "local", Path: f.path("a.go"), Target: siteEdit, Replacement: "func main() { L{}.Write() }", Caller: "t-site"}
}

// O modelo troca o nome num ponto de uso: o servidor completa o rename (declaração e demais usos), e como o modelo
// nunca abriu lib.go a resposta não o menciona.
func TestCallSiteRenameCompletesAndOmitsUnseen(t *testing.T) {
	f := newLSPFixture(t, siteCtl("lib.go"))
	f.write("a.go", siteOld)
	f.write("lib.go", siteLib)
	f.gitRepo()
	f.start()

	msg, err := HandleEditFile(siteEditArgs(f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "DONE WriteLine -> Write: your edit was a rename of the member") {
		t.Fatalf("site rename not completed:\n%s", msg)
	}
	if got := f.read("lib.go"); !strings.Contains(got, "func (L) Write()") {
		t.Fatalf("declaration not renamed:\n%s", got)
	}
	if got := f.read("a.go"); got != siteNew {
		t.Fatalf("a.go = %q", got)
	}
	if strings.Contains(msg, "re-read before editing") || strings.Contains(msg, "lib.go") {
		t.Fatalf("lib.go was never opened by the model: it must not be mentioned:\n%s", msg)
	}
	if !strings.Contains(msg, "a.go:3, +1 more") {
		t.Fatalf("the edited file stays in the receipt, the unseen one only counts:\n%s", msg)
	}
	// O número do "compila" é o dos arquivos de código alterados (a.go + lib.go = 2), não um subconjunto.
	if !strings.Contains(msg, "✓ compila: 0 erros nos 2 arquivos de código alterados") {
		t.Fatalf("compile line must count every changed code file:\n%s", msg)
	}
}

// Se o modelo abriu lib.go, a cópia dele ficou velha: aí o aviso de "releia" fica.
func TestCallSiteRenameKeepsNoticeForSeenFiles(t *testing.T) {
	f := newLSPFixture(t, siteCtl("lib.go"))
	f.write("a.go", siteOld)
	f.write("lib.go", siteLib)
	f.gitRepo()
	f.start()
	noteSeen("t-site", "local", f.path("lib.go"))

	msg, err := HandleEditFile(siteEditArgs(f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "files changed: lib.go; re-read before editing them") {
		t.Fatalf("a file the model had open must be announced:\n%s", msg)
	}
}

// O nome novo já é outro membro do tipo (Log.Info -> Log.Debug): a troca é uma escolha, não um rename. Nada além da
// edição do modelo acontece e a resposta não faz barulho.
func TestCallSiteRenameSkippedWhenNewNameExists(t *testing.T) {
	f := newLSPFixture(t, siteCtl("lib.go"))
	f.write("a.go", siteOld)
	f.write("lib.go", siteLib+"\nfunc (L) Write() {}\n")
	f.gitRepo()
	f.start()

	msg, err := HandleEditFile(siteEditArgs(f))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "DONE") || strings.Contains(msg, "not applied") {
		t.Fatalf("clash must be silent and untouched:\n%s", msg)
	}
	if got := f.read("lib.go"); strings.Contains(got, "func (L) Write() {}\n\nfunc (L) Write") || !strings.Contains(got, "WriteLine") {
		t.Fatalf("declaration must stay as it was:\n%s", got)
	}
	if got := f.read("a.go"); got != siteNew {
		t.Fatalf("the model's own edit must stand: %q", got)
	}
}
