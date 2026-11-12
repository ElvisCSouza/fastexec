//go:build !windows

package lsp

import (
	"os/exec"
	"syscall"
	"time"
)

// sysProcAttr puts the server in its own process group so the whole tree can
// be signalled at once.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree terminates the server and its children: SIGTERM to the
// group first (servers flush state), then SIGKILL.
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_, _ = cmd.Process.Wait()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}
