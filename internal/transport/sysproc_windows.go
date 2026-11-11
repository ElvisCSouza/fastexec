//go:build windows

package transport

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
)

// rePowerShellLine reconhece uma linha que começa chamando o PowerShell (powershell, pwsh, com ou sem .exe).
var rePowerShellLine = regexp.MustCompile(`(?i)^\s*(powershell|pwsh)(\.exe)?\s`)

// psInlineEscapes diz se a linha é uma chamada de PowerShell escrita com o escape de aspas do PowerShell (\").
// O cmd.exe não conhece esse escape: conta as aspas de outro jeito e passa a tratar | & < > dentro da string como
// operadores dele, e a chamada falha em silêncio ("no result (exit 1)"). Essas linhas vão direto ao PowerShell, que
// as interpreta como o autor quis. Sem \" a linha segue pelo cmd como sempre (pipes, &&, redirecionamentos, %VAR%).
func psInlineEscapes(cmdStr string) bool {
	return rePowerShellLine.MatchString(cmdStr) && strings.Contains(cmdStr, `\"`)
}

func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
}

func buildShellCommand(ctx context.Context, cmdStr string) *exec.Cmd {
	if psInlineEscapes(cmdStr) {
		exe := "powershell.exe"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(cmdStr)), "pwsh") {
			exe = "pwsh.exe"
		}
		if path, err := exec.LookPath(exe); err == nil {
			cmd := exec.CommandContext(ctx, path)
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CmdLine:       strings.TrimSpace(cmdStr),
				CreationFlags: 0x08000000,
				HideWindow:    true,
			}
			return cmd
		}
	}
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       fmt.Sprintf(`cmd.exe /s /c "%s"`, cmdStr),
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
	return cmd
}
