//go:build windows

package graph

import "syscall"

// hiddenWindow evita a janela de console piscando quando o FastExec (binário GUI) chama o git.
func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
