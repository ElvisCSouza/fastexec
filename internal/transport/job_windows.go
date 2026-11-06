//go:build windows

package transport

import (
	"path/filepath"
	"strings"
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

// jobPIDList espelha JOBOBJECT_BASIC_PROCESS_ID_LIST com capacidade fixa (árvore de um exec raramente passa de 64).
type jobPIDList struct {
	Assigned uint32
	InList   uint32
	PIDs     [64]uintptr
}

// alive conta, numa consulta só e sem espera, os processos ainda vivos no job, ignorando o conhost.exe que o console
// do cmd deixa saindo nos mesmos milissegundos (não é filho solto). 0 se o job não recebeu processo ou a consulta falhar.
func (j *jobObject) alive() uint32 {
	if !j.assigned {
		return 0
	}
	var list jobPIDList
	if err := windows.QueryInformationJobObject(j.h, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil); err != nil && err != windows.ERROR_MORE_DATA {
		return 0
	}
	var n uint32
	for i := uint32(0); i < list.InList && i < uint32(len(list.PIDs)); i++ {
		if !isConhost(uint32(list.PIDs[i])) {
			n++
		}
	}
	return n + (list.Assigned - min(list.Assigned, list.InList)) // além da capacidade: conta sem filtrar
}

// isConhost diz se o PID é o conhost.exe (host de console). Falha na consulta = não é (conta como vivo).
func isConhost(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:size])), "conhost.exe")
}

// close libera o handle; com KILL_ON_JOB_CLOSE, o que ainda estiver vivo no job morre junto.
func (j *jobObject) close() {
	windows.CloseHandle(j.h)
}
