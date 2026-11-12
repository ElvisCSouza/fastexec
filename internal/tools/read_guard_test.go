package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// edit_file must never see a truncated file: the 32 KB read cap is only for
// the model-facing read_file.
func TestEditFileOnBigFileKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	body := strings.Repeat("0123456789 filler line\n", 5000) + "TARGET_END\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "TARGET_END", Replacement: "DONE_END"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(got), "DONE_END\n") || len(got) != len(body)+len("DONE_END")-len("TARGET_END") {
		t.Fatalf("file was truncated or damaged: %d bytes", len(got))
	}

	out, err := HandleReadFile(ReadFileArgs{Path: p})
	if err != nil || !strings.Contains(out, "[FastExec] Showing lines 1-") {
		t.Fatalf("direct whole read of a big file must be capped with a hint: %v", err)
	}
	out, _ = HandleReadFile(ReadFileArgs{Path: p, Pattern: "DONE_END"})
	if !strings.Contains(out, "DONE_END") {
		t.Fatalf("pattern read must reach the end of the file: %q", out)
	}
}

// TestReadFileFolderTellsWhatToUse: read_file de uma pasta respondia o erro cru do SO — no Windows, a string
// localizada "Função incorreta" (ERROR_INVALID_FUNCTION), que não diz nada ao modelo. Visto no M6 do ciclo2: o modelo
// pediu read_file de uma pasta com pattern e gastou um turno até cair no exec grep.
func TestReadFileFolderTellsWhatToUse(t *testing.T) {
	_, err := HandleReadFile(ReadFileArgs{Path: t.TempDir(), Pattern: "x"})
	if err == nil {
		t.Fatal("read_file de pasta deveria falhar")
	}
	if !strings.Contains(err.Error(), "is a folder, not a file") {
		t.Fatalf("erro sem orientação: %v", err)
	}
	for _, want := range []string{"file_ops", "code_outline", "exec"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("mensagem deveria citar %s: %v", want, err)
		}
	}
}
