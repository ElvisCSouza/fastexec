package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ls/dir de pasta grande vira listagem nativa com teto: as primeiras lsMaxEntries linhas e um resumo do resto.
func TestListRemediationIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < lsMaxEntries+50; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		_ = os.Mkdir(filepath.Join(dir, fmt.Sprintf("z%d", i)), 0o755)
	}
	out, handled, err := tryRemediateList(ExecArgs{Host: "local", Command: "ls " + dir})
	if err != nil || !handled {
		t.Fatalf("not handled: %v", err)
	}
	if n := strings.Count(out, "\nFILE ") + strings.Count(out, "\nDIR "); n != lsMaxEntries {
		t.Errorf("listed %d entries, want %d", n, lsMaxEntries)
	}
	if !strings.Contains(out, "[truncated: 53 more entries") {
		t.Errorf("missing summary:\n%s", out[len(out)-300:])
	}
}
