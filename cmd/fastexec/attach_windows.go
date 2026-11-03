//go:build windows

package main

import (
	"os"
	"syscall"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = modkernel32.NewProc("AttachConsole")
	procGetFileType   = modkernel32.NewProc("GetFileType")
)

const fileTypePipe = 3

func init() {
	// Se stdout já for válido (redirecionado para arquivo, pipe MCP JSON-RPC, etc.),
	// não anexa console para não sobrescrever o destino de saída.
	h, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	ft, _, _ := procGetFileType.Call(uintptr(h))
	if ft != 0 {
		return
	}

	// Se executado diretamente pelo usuário em um console existente (cmd/powershell),
	// anexa ao console pai para exibir saídas CLI normalmente.
	// Se iniciado por aplicativo GUI sem console, falha silenciosamente sem abrir janela.
	r, _, _ := procAttachConsole.Call(uintptr(^uint32(0)))
	if r != 0 {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0666); err == nil {
			os.Stdout = f
			os.Stderr = f
		}
	}
}
