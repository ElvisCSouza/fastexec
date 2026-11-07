//go:build windows

package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
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

var execJobSeq atomic.Uint64

// newExecJob cria o job de um exec; named=true dá um nome único (Local\FastExec_Exec_<pid>_<n>) para o job poder
// ser assumido pela tool job no handoff e achado por outro processo FastExec.
func newExecJob(named bool) (*jobObject, string, error) {
	if !named {
		j, err := newJobObject()
		return j, "", err
	}
	name := fmt.Sprintf(`Local\FastExec_Exec_%d_%d`, os.Getpid(), execJobSeq.Add(1))
	j, err := newNamedJobObject(name)
	return j, name, err
}

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

// alive conta os processos ainda vivos no job, ignorando o conhost.exe (host de console, não é filho solto).
// Caso normal: uma consulta, sem espera. Se a primeira contagem não der zero, recontagem por até aliveSettle: logo
// depois do Wait o job ainda lista por milissegundos processos em fim de vida; filho solto de verdade continua lá.
func (j *jobObject) alive() uint32 {
	if !j.assigned {
		return 0
	}
	n := j.countAlive()
	for deadline := time.Now().Add(aliveSettle); n > 0 && time.Now().Before(deadline); {
		time.Sleep(aliveStep)
		n = j.countAlive()
	}
	return n
}

const (
	aliveSettle = 50 * time.Millisecond
	aliveStep   = 5 * time.Millisecond
)

func (j *jobObject) countAlive() uint32 {
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
