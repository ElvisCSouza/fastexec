//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

func spawnDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "daemon-run")
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
