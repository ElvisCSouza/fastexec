//go:build windows

package lsp

import (
	"os/exec"
	"syscall"
)

// sysProcAttr starts the language server without a console window and in its
// own process group, so a kill can reach any helper processes it spawned
// (gopls forks `go list`, the C# server forks the compiler, ...).
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
	}
}

// killProcessTree terminates the server and its children.
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// /T takes the tree, /F cannot be refused. taskkill is present on every
	// supported Windows version; if it is missing we still kill the root.
	kill := exec.Command("taskkill", "/T", "/F", "/PID", itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := kill.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
	_, _ = cmd.Process.Wait()
}
