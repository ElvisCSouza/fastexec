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
	if err != nil || !strings.Contains(out, "[FastExec] File has") {
		t.Fatalf("direct whole read of a big file must be capped with a hint: %v", err)
	}
	out, _ = HandleReadFile(ReadFileArgs{Path: p, Pattern: "DONE_END"})
	if !strings.Contains(out, "DONE_END") {
		t.Fatalf("pattern read must reach the end of the file: %q", out)
	}
}
