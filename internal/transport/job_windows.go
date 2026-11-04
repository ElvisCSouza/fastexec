//go:build windows

package transport

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject agrupa um processo e toda a sua descendência. Fechar o handle (ou chamar kill) encerra a árvore inteira de
// forma atômica pelo kernel, sem depender de os filhos cooperarem nem de os pipes fecharem.
type jobObject struct {
	h        windows.Handle
	assigned bool
}

func newJobObject() (*jobObject, error) { return newNamedJobObject("") }

// newNamedJobObject cria o job (com KILL_ON_JOB_CLOSE); name vazio = job anônimo. Nome no namespace Local\ da sessão.
func newNamedJobObject(name string) (*jobObject, error) {
	var namePtr *uint16
	if name != "" {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		namePtr = p
	}
	h, err := windows.CreateJobObject(nil, namePtr)
	if h == 0 {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &jobObject{h: h}, nil
}

// assign põe o processo (já iniciado) no job. Filhos criados depois herdam o job automaticamente.
// Janela residual conhecida: um filho criado entre CreateProcess e esta chamada escapa; o cmd.exe leva mais que isso
// para inicializar, então na prática o cmd e seus descendentes entram no job.
func (j *jobObject) assign(pid int) error {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(j.h, ph); err != nil {
		return err
	}
	j.assigned = true
	return nil
}

// kill encerra todos os processos do job agora.
func (j *jobObject) kill() {
	if j.assigned {
		windows.TerminateJobObject(j.h, 1)
	}
}

// close libera o handle; com KILL_ON_JOB_CLOSE, o que ainda estiver vivo no job morre junto.
func (j *jobObject) close() {
	windows.CloseHandle(j.h)
}
