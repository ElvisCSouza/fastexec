package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// O modelo manda uma edição cujo texto novo já está no arquivo (o rename do compilador já a fez): não é erro, é
// "já aplicado", e nada é gravado.
func TestEditFileAlreadyApplied(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.cs")
	src := "class A {\n    void M() { SelfLog.Write(\"Maximum depth reached.\"); }\n}\n"
	_ = os.WriteFile(p, []byte(src), 0o644)

	msg, err := HandleEditFile(EditFileArgs{Host: "local", Path: p,
		Target:      "SelfLog.WriteLine(\"Maximum depth reached.\");",
		Replacement: "SelfLog.Write(\"Maximum depth reached.\");"})
	if err != nil || !strings.Contains(msg, "already applied, nothing changed") {
		t.Fatalf("want already applied, got %q err=%v", msg, err)
	}
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("file must not change: %q", got)
	}

	// texto novo ausente também: continua sendo erro
	_, err = HandleEditFile(EditFileArgs{Host: "local", Path: p,
		Target: "Nothing.Here(\"x\");", Replacement: "Something.Else(\"y\", 1);"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a real miss must stay an error, got %v", err)
	}

	// em lote: uma já aplicada e uma nova — a nova vale, a aplicada é pulada
	msg, err = HandleEditFile(EditFileArgs{Host: "local", Path: p, Edits: []EditOp{
		{Target: "SelfLog.WriteLine(\"Maximum depth reached.\");", Replacement: "SelfLog.Write(\"Maximum depth reached.\");"},
		{Target: "void M()", Replacement: "void Run()"},
	}})
	if err != nil || !strings.Contains(msg, "1 of the edits were already applied") {
		t.Fatalf("mixed batch: %q err=%v", msg, err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "void Run()") {
		t.Fatalf("the new edit must be applied: %q", got)
	}
}
