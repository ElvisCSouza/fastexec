//go:build !windows

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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd.Start()
}
