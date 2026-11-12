//go:build windows

package graph

import (
	"runtime"
	"syscall"
)

// bgnice_windows.go: a passada de fundo roda com a thread abaixo do normal (THREAD_PRIORITY_BELOW_NORMAL): ela
// cede a CPU para a chamada que a disparou e para o resto da máquina, sem mudar a prioridade do processo inteiro
// (o daemon continua atendendo na prioridade de sempre). A thread é fixada na goroutine (LockOSThread) — a
// prioridade é da thread, e sem a fixação o Go poderia devolver a goroutine a outra thread.

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadPriority = kernel32.NewProc("SetThreadPriority")
)

const (
	threadPriorityNormal      = 0
	threadPriorityBelowNormal = -1
	// GetCurrentThread devolve o pseudo-handle da thread atual: (HANDLE)-1.
	currentThread = ^uintptr(0)
)

func lowerThreadPriority() (restore func()) {
	runtime.LockOSThread()
	below := int32(threadPriorityBelowNormal) // o argumento é DWORD: a conversão tira o -1 do domínio das constantes
	r, _, _ := procSetThreadPriority.Call(currentThread, uintptr(below))
	if r == 0 {
		runtime.UnlockOSThread() // não deu para baixar: segue na prioridade normal, sem fixar a thread
		return func() {}
	}
	return func() {
		_, _, _ = procSetThreadPriority.Call(currentThread, uintptr(threadPriorityNormal))
		runtime.UnlockOSThread()
	}
}
