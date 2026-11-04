//go:build windows

package transport

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
}

func buildShellCommand(ctx context.Context, cmdStr string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       fmt.Sprintf(`cmd.exe /s /c "%s"`, cmdStr),
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
	return cmd
}
