//go:build windows

package transport

import "syscall"

func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
		HideWindow:    true,
	}
}
