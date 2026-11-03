//go:build !windows

package analytics

import "syscall"

func hideWindowSysProcAttr() *syscall.SysProcAttr {
	return nil
}
