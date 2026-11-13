package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSigKey(t *testing.T) {
	long := "    public static void WriteLine(string format, object? arg0 = null, object? arg1 = null)\n"
	if k := sigKey(long); k != "public static void WriteLine(string format, object? arg0 = null, object? arg1 = null)" {
		t.Fatalf("key = %q", k)
	}
	for _, short := range []string{"void M()", "int Count { get; }", "public int X;"} {
		if sigKey(short) != "" {
			t.Fatalf("%q is too short or has no parentheses: must not be a key", short)
		}
	}
}

// O snapshot de API tem a assinatura exata: o servidor troca a linha, mantém LF/CRLF e BOM, e o resto fica.
func TestTextSignatureUpdatesExactLine(t *testing.T) {
	root := t.TempDir()
	src := string([]byte{0xef, 0xbb, 0xbf}) + "namespace X\r\n{\r\n    public static class SelfLog\r\n    {\r\n        public static void WriteLine(string format, object? arg0 = null) { }\r\n        public static void Other(string format, object? arg0 = null) { }\r\n    }\r\n}\r\n"
	p := filepath.Join(root, "api.approved.txt")
	_ = os.WriteFile(p, []byte(src), 0o644)
	key := sigKey("public static void WriteLine(string format, object? arg0 = null)")
	sites := sigMentionSites(root, key, time.Second)
	if len(sites[p]) != 1 || sites[p][0] != 4 {
		t.Fatalf("sites = %v", sites)
	}
	changed, _, err := applySigMentions(sites, "WriteLine", "Write")
	if err != nil || len(changed) != 1 {
		t.Fatalf("apply: %v %v", changed, err)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(src, "WriteLine", "Write", 1)
	if string(got) != want {
		t.Fatalf("file must differ only by the name (BOM and CRLF kept):\n%q\nwant\n%q", got, want)
	}
}

// Linhas demais casando = ambíguo: nada é aplicado.
func TestTextSignatureAmbiguousAppliesNothing(t *testing.T) {
	root := t.TempDir()
	line := "public static void WriteLine(string format, object? arg0 = null)\n"
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Repeat(line, sigMaxLines+1)), 0o644)
	key := sigKey(line)
	if sites := sigMentionSites(root, key, time.Second); sites != nil {
		t.Fatalf("too many matches must not be applied: %v", sites)
	}
}

// Fim a fim: rename pela declaração em C#-like (fixture Go) atualiza a linha do snapshot, e a resposta não manda o
// modelo editá-la.
func TestRenameUpdatesSignatureSnapshot(t *testing.T) {
	f := newLSPFixture(t, renameCtl("ParseRecord"))
	f.write("a.go", "package main\n\nfunc ParseLine(input string, extra int) {}\n")
	f.write("b.go", "package main\n\nfunc main() { ParseLine(\"\", 0) }\n")
	f.write("api.approved.txt", "x\nfunc ParseLine(input string, extra int) {}\n")
	f.gitRepo()
	f.start()
	// o fixture renameCtl usa "func ParseLine() {}"; aqui a declaração é maior: as colunas são as mesmas (coluna 5..14, linha 2)
	msg := f.edit("a.go", "package main\n\nfunc ParseRecord(input string, extra int) {}\n")
	if !strings.Contains(msg, "also updated text that held this member's exact signature") {
		t.Fatalf("snapshot line must be updated by the server:\n%s", msg)
	}
	if got := f.read("api.approved.txt"); !strings.Contains(got, "func ParseRecord(input string, extra int) {}") {
		t.Fatalf("snapshot not updated:\n%s", got)
	}
	if strings.Contains(msg, "only work left") {
		t.Fatalf("nothing is left for the model:\n%s", msg)
	}
}

// O comentário da declaração com a palavra do nome novo não é um membro: o rename a partir do ponto de uso segue.
func TestCallSiteRenameIgnoresNewNameInComments(t *testing.T) {
	f := newLSPFixture(t, siteCtl("lib.go"))
	f.write("a.go", siteOld)
	f.write("lib.go", "package main\n\ntype L struct{}\n\n// Write a message to the log.\nfunc (L) WriteLine() {}\n")
	f.gitRepo()
	f.start()
	msg, err := HandleEditFile(siteEditArgs(f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "DONE WriteLine -> Write") {
		t.Fatalf("a comment mentioning the new name must not block the rename:\n%s", msg)
	}
}
