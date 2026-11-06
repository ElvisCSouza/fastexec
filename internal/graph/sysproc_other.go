//go:build !windows

package graph

import "syscall"

func hiddenWindow() *syscall.SysProcAttr { return nil }
