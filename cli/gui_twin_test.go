package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `fastexec-cli.exe setup` deve registrar o exe sem console quando ele existe ao lado; sem o gemeo, mantem o caminho.
func TestGuiTwin(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "fastexec-cli.exe")
	gui := filepath.Join(dir, "fastexec.exe")
	if got := guiTwin(cli); got != cli {
		t.Fatalf("sem o gemeo ao lado deve manter %s, veio %s", cli, got)
	}
	if err := os.WriteFile(gui, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := guiTwin(cli); got != gui {
		t.Fatalf("com o gemeo ao lado deve usar %s, veio %s", gui, got)
	}
	if got := guiTwin(gui); got != gui {
		t.Fatalf("o proprio exe sem console nao muda: %s", got)
	}
	other := filepath.Join(dir, "fastexec-setup-windows-amd64.exe")
	if got := guiTwin(other); got != other {
		t.Fatalf("outros nomes nao mudam: %s", got)
	}
}
