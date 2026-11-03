//go:build !windows

package transport

import "syscall"

func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return nil
}
