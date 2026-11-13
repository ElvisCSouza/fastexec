//go:build windows

package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// O argv ["cmd","/c","findstr /C:\"...\" arquivo"] achava "zero ocorrências" mesmo com o texto no arquivo.
func TestRunLocalCmdCQuotedFindstrFindsText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.cs"), []byte("x\r\n    SelfLog.Write(\"hi\");\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runLocal(localSpec{
		argv: []string{"cmd", "/c", `findstr /S /N /C:"SelfLog.Write" *.cs`},
		cwd:  dir,
	}, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "SelfLog.Write") {
		t.Fatalf("findstr must see the text (exit %d):\nstdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
}
