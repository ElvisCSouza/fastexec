//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func spawnDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(daemonExe(self), "daemon-run")
	// CREATE_NO_WINDOW (0x08000000) | DETACHED_PROCESS (0x00000008)
	// Nota Win32: processos com DETACHED_PROCESS não podem herdar handles de stdio do pai.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | 0x00000008,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// daemonExe: o daemon sobe sempre do binário sem console (fastexec.exe), também quando quem chama é o gêmeo de
// console usado no terminal (fastexec-cli.exe). Senão o próximo proxy MCP veria "outro binário" e trocaria o
// daemon, encerrando os jobs locais que ele mantém.
func daemonExe(self string) string {
	dir, base := filepath.Split(self)
	if !strings.EqualFold(base, "fastexec-cli.exe") {
		return self
	}
	gui := filepath.Join(dir, "fastexec.exe")
	if st, err := os.Stat(gui); err == nil && !st.IsDir() {
		return gui
	}
	return self
}
