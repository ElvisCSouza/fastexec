//go:build windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// O gêmeo de console (fastexec-cli.exe) sobe o daemon pelo fastexec.exe ao lado, para o proxy MCP não trocá-lo.
func TestDaemonExe(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "fastexec-cli.exe")
	gui := filepath.Join(dir, "fastexec.exe")
	if got := daemonExe(cli); got != cli {
		t.Errorf("sem fastexec.exe ao lado usa o próprio: %s", got)
	}
	if err := os.WriteFile(gui, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := daemonExe(cli); got != gui {
		t.Errorf("esperava %s, veio %s", gui, got)
	}
	if got := daemonExe(gui); got != gui {
		t.Errorf("fastexec.exe usa o próprio: %s", got)
	}
}
