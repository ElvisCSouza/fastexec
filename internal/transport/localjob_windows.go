package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Backend Windows da tool `job` (host local): processos de longa duração (dev server, watcher, túnel) que sobrevivem ao
// fim da chamada MCP. O `exec` continua síncrono e mata tudo ao terminar; aqui cada job vive num Job Object NOMEADO e é
// parado pelo nome do job (nunca por PID, que o Windows recicla).
//
// Achado empírico: o nome de um job object só resolve enquanto ALGUÉM mantém um handle aberto; fechar o último handle
// remove o nome do namespace mesmo com processos vivos (o job fica inalcançável e o processo, órfão). Por isso o FastExec
// que inicia o job guarda o handle enquanto o job viver e cria o job com KILL_ON_JOB_CLOSE: o job vive exatamente o
// quanto este processo FastExec viver (sobrevive às chamadas, não a um restart do FastExec) e nunca vira órfão sem dono.
//
// Estado em disco, sem buffers em memória: <dir>/<id>.json (metadados) e <dir>/<id>.log (stdout+stderr escritos pelo
// próprio processo filho direto no arquivo).

var (
	// LocalJobMaxActive é o teto de jobs locais simultâneos (expandido para 12 para suportar múltiplas IAs e sessões concorrentes).
	LocalJobMaxActive = 12
	// localJobTTL é a vida máxima de um job local desde a criação; passou disso, o job é encerrado.
	localJobTTL = 12 * time.Hour
	// localJobKeepFinished é quanto tempo os arquivos de um job já finalizado ficam em disco.
	localJobKeepFinished = 7 * 24 * time.Hour
)

const (
	jobStateRunning = "RUNNING"
	jobStateExited  = "EXITED"
	jobStateStopped = "STOPPED"
	jobStateExpired = "EXPIRED"

	jobAccessQuery     = 0x0004
	jobAccessTerminate = 0x0008

	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

var jobIDRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// owned guarda o handle dos jobs iniciados por este processo, por nome do job object.
var owned = struct {
	sync.Mutex
	h map[string]windows.Handle
}{h: map[string]windows.Handle{}}

func ownedHandle(name string) (windows.Handle, bool) {
	owned.Lock()
	defer owned.Unlock()
	h, ok := owned.h[name]
	return h, ok
}

func own(name string, h windows.Handle) {
	owned.Lock()
	owned.h[name] = h
	owned.Unlock()
}

// disown fecha o handle guardado (com KILL_ON_JOB_CLOSE, se ainda houver processo vivo ele morre aqui).
func disown(name string) {
	owned.Lock()
	defer owned.Unlock()
	if h, ok := owned.h[name]; ok {
		windows.CloseHandle(h)
		delete(owned.h, name)
	}
}

// LocalJobMeta é o que fica gravado em <id>.json. O PID é só informativo: nada aqui é encerrado por PID.
type LocalJobMeta struct {
	ID            string     `json:"id"`
	Command       string     `json:"command,omitempty"`
	Argv          []string   `json:"argv,omitempty"`
	Cwd           string     `json:"cwd,omitempty"`
	PID           int        `json:"pid"`
	CreatedAt     time.Time  `json:"created_at"`
	JobObjectName string     `json:"job_object_name"`
	LogPath       string     `json:"log_path"`
	State         string     `json:"state"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
}

// LocalJobInfo é o estado vivo de um job, já reconciliado com o kernel.
type LocalJobInfo struct {
	Meta         LocalJobMeta
	State        string
	Active       uint32 // processos ativos no job
	LastActivity time.Time
	LogSize      int64
}

// LocalJobsDir devolve a pasta de estado: FASTEXEC_JOBS_DIR (testes), senão W:\Data\jobs se a unidade W: existir,
// senão %LOCALAPPDATA%\fastexec\jobs.
func LocalJobsDir() (string, error) {
	dir := os.Getenv("FASTEXEC_JOBS_DIR")
	if dir == "" {
		if st, err := os.Stat(`W:\`); err == nil && st.IsDir() {
			dir = `W:\Data\jobs`
		} else if base := os.Getenv("LOCALAPPDATA"); base != "" {
			dir = filepath.Join(base, "fastexec", "jobs")
		} else {
			return "", fmt.Errorf("sem pasta para jobs: W: ausente e LOCALAPPDATA indefinido")
		}
	}
	return dir, os.MkdirAll(dir, 0o755)
}

var procOpenJobObject = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

// openJobObject abre um job existente pelo nome. Devolve (0, errJobGone) se ele não existe mais: um job só existe
// enquanto tiver processo vivo ou handle aberto.
var errJobGone = errors.New("job object não existe mais")

func openJobObject(access uint32, name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procOpenJobObject.Call(uintptr(access), 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) || errors.Is(callErr, windows.ERROR_PATH_NOT_FOUND) {
			return 0, errJobGone
		}
		return 0, callErr
	}
	return windows.Handle(h), nil
}

type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// activeProcesses conta os processos vivos no job (contabilidade do próprio job: não envolve PID nenhum).
func activeProcesses(h windows.Handle) (uint32, error) {
	var a jobBasicAccounting
	if err := windows.QueryInformationJobObject(h, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil); err != nil {
		return 0, err
	}
	return a.ActiveProcesses, nil
}

func metaPath(dir, id string) string { return filepath.Join(dir, id+".json") }

func saveMeta(dir string, m LocalJobMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := metaPath(dir, m.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, metaPath(dir, m.ID)) // escrita atômica
}

func loadMeta(dir, id string) (LocalJobMeta, error) {
	var m LocalJobMeta
	if !jobIDRegex.MatchString(id) {
		return m, fmt.Errorf("job_id inválido: %q", id)
	}
	b, err := os.ReadFile(metaPath(dir, id))
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("job '%s' não encontrado (use action='list')", id)
		}
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func logActivity(m LocalJobMeta) (time.Time, int64) {
	st, err := os.Stat(m.LogPath)
	if err != nil {
		return m.CreatedAt, 0
	}
	if st.ModTime().After(m.CreatedAt) {
		return st.ModTime(), st.Size()
	}
	return m.CreatedAt, st.Size()
}

// inspect reconcilia o metadado com o kernel: abre o job pelo nome e conta processos ativos. Se o job acabou, grava
// EXITED. Não aplica TTL (quem chama decide).
func inspect(dir string, m LocalJobMeta) (LocalJobInfo, error) {
	info := LocalJobInfo{Meta: m, State: m.State}
	info.LastActivity, info.LogSize = logActivity(m)

	h, isOwned := ownedHandle(m.JobObjectName)
	var err error
	if !isOwned {
		h, err = openJobObject(jobAccessQuery, m.JobObjectName)
	}
	switch {
	case errors.Is(err, errJobGone):
		info.Active = 0
	case err != nil:
		return info, err
	default:
		n, qerr := activeProcesses(h)
		if !isOwned {
			windows.CloseHandle(h)
		}
		if qerr != nil {
			return info, qerr
		}
		info.Active = n
	}
	if isOwned && info.Active == 0 {
		disown(m.JobObjectName) // o job acabou: solta o handle
	}

	if info.Active > 0 {
		info.State = jobStateRunning
		return info, nil
	}
	if m.State == jobStateRunning { // terminou sozinho
		now := time.Now()
		m.State, m.EndedAt = jobStateExited, &now
		info.Meta, info.State = m, jobStateExited
		_ = saveMeta(dir, m)
	}
	return info, nil
}

func terminateByName(name string) error {
	if h, ok := ownedHandle(name); ok { // este processo é o dono: usa o handle guardado
		err := windows.TerminateJobObject(h, 1)
		disown(name)
		return err
	}
	h, err := openJobObject(jobAccessQuery|jobAccessTerminate, name)
	if errors.Is(err, errJobGone) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateJobObject(h, 1)
}

// maintain aplica a higiene: encerra jobs acima do TTL e apaga arquivos de jobs finalizados há muito tempo.
func maintain(dir string) []LocalJobInfo {
	entries, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []LocalJobInfo
	now := time.Now()
	for _, f := range entries {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		m, err := loadMeta(dir, id)
		if err != nil {
			continue
		}
		info, err := inspect(dir, m)
		if err != nil {
			out = append(out, info)
			continue
		}
		if info.State == jobStateRunning && now.Sub(m.CreatedAt) > localJobTTL {
			if terminateByName(m.JobObjectName) == nil {
				t := now
				m.State, m.EndedAt = jobStateExpired, &t
				_ = saveMeta(dir, m)
				info.Meta, info.State, info.Active = m, jobStateExpired, 0
			}
		}
		if info.State != jobStateRunning && now.Sub(info.LastActivity) > localJobKeepFinished {
			os.Remove(metaPath(dir, id))
			os.Remove(m.LogPath)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.CreatedAt.After(out[j].Meta.CreatedAt) })
	return out
}

func newJobID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102_150405") + "_" + hex.EncodeToString(b)
}

// StartLocalJob inicia command (cmd.exe) ou argv (sem shell) em background, num Job Object nomeado, e devolve na hora.
func StartLocalJob(command string, argv []string, cwd, wantID string) (*LocalJobMeta, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, err
	}
	if cwd != "" {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("cwd inexistente ou não é diretório: %s", cwd)
		}
	}

	var running []string
	for _, j := range maintain(dir) {
		if j.State == jobStateRunning {
			running = append(running, j.Meta.ID)
		}
	}
	if len(running) >= LocalJobMaxActive {
		return nil, fmt.Errorf("limite de %d jobs ativos no Windows local atingido (%s); pare um com job(action='stop', job_id='...')",
			LocalJobMaxActive, strings.Join(running, ", "))
	}

	id := wantID
	if id == "" {
		id = newJobID()
	} else if !jobIDRegex.MatchString(id) {
		return nil, fmt.Errorf("job_id inválido: %q (use letras, números, '_' e '-', até 40 caracteres)", id)
	}
	if _, err := os.Stat(metaPath(dir, id)); err == nil {
		return nil, fmt.Errorf("já existe um job com id '%s'", id)
	}

	logPath := filepath.Join(dir, id+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer lf.Close() // o filho já terá herdado a sua própria cópia do handle

	jobName := `Local\FastExec_Job_` + id
	// O job aninha dentro de qualquer job do host (Windows 8+), então não precisa de breakaway.
	job, err := newNamedJobObject(jobName)
	if err != nil {
		return nil, fmt.Errorf("não consegui criar o job object: %v", err)
	}
	keep := false
	defer func() {
		if !keep {
			job.close()
		}
	}()

	flags := uint32(createNoWindow | createNewProcessGroup)
	var cmd *exec.Cmd
	if argv != nil {
		effectiveArgv := ResolveShellArgv(argv)
		cmd = exec.Command(effectiveArgv[0], effectiveArgv[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
	} else {
		cmd = exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`cmd.exe /s /c "%s"`, command), HideWindow: true, CreationFlags: flags}
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = localEnv()
	cmd.Stdin = nil // NUL: EOF imediato
	cmd.Stdout, cmd.Stderr = lf, lf

	if err := cmd.Start(); err != nil {
		os.Remove(logPath)
		return nil, fmt.Errorf("falha ao iniciar: %v", err)
	}
	meta := LocalJobMeta{
		ID: id, Command: command, Argv: argv, Cwd: cwd, PID: cmd.Process.Pid,
		CreatedAt: time.Now(), JobObjectName: jobName, LogPath: logPath, State: jobStateRunning,
	}

	if aerr := job.assign(cmd.Process.Pid); aerr != nil {
		// quase sempre o processo já terminou (comando que falha na hora); se ainda vive, não dá para gerenciá-lo: mata
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done: // saiu rápido: o job fica registrado como finalizado, com o log para consulta
			now := time.Now()
			meta.State, meta.EndedAt = jobStateExited, &now
			if err := saveMeta(dir, meta); err != nil {
				return nil, err
			}
			return &meta, nil
		case <-time.After(500 * time.Millisecond):
			_ = cmd.Process.Kill()
			os.Remove(logPath)
			return nil, fmt.Errorf("não consegui associar o processo ao job object (%v); processo encerrado", aerr)
		}
	}
	_ = cmd.Process.Release()
	own(jobName, job.h) // a partir daqui o handle é guardado; o job vive enquanto este FastExec viver
	keep = true

	if err := saveMeta(dir, meta); err != nil {
		_ = terminateByName(jobName)
		return nil, err
	}
	return &meta, nil
}

// LocalJobStatus devolve o estado vivo do job (aplicando a higiene antes).
func LocalJobStatus(id string) (*LocalJobInfo, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, err
	}
	maintain(dir)
	m, err := loadMeta(dir, id)
	if err != nil {
		return nil, err
	}
	info, err := inspect(dir, m)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// LocalJobList devolve os jobs conhecidos, do mais novo para o mais velho.
func LocalJobList() ([]LocalJobInfo, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, err
	}
	return maintain(dir), nil
}

// LocalJobStop encerra a árvore inteira do job pelo NOME do Job Object (atômico, sem PID).
func LocalJobStop(id string) (*LocalJobInfo, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, err
	}
	m, err := loadMeta(dir, id)
	if err != nil {
		return nil, err
	}
	if err := terminateByName(m.JobObjectName); err != nil {
		return nil, fmt.Errorf("falha ao encerrar o job: %v", err)
	}
	if m.State == jobStateRunning {
		now := time.Now()
		m.State, m.EndedAt = jobStateStopped, &now
		if err := saveMeta(dir, m); err != nil {
			return nil, err
		}
	}
	info := LocalJobInfo{Meta: m, State: m.State}
	info.LastActivity, info.LogSize = logActivity(m)
	return &info, nil
}

// LocalJobWait espera, por evento de polling curto no job, até ele terminar ou até maxWait. finished=false: ainda roda.
func LocalJobWait(id string, maxWait time.Duration) (info *LocalJobInfo, finished bool, err error) {
	deadline := time.Now().Add(maxWait)
	for {
		info, err = LocalJobStatus(id)
		if err != nil {
			return nil, false, err
		}
		if info.State != jobStateRunning {
			return info, true, nil
		}
		if time.Now().After(deadline) {
			return info, false, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TailLocalJob devolve as últimas n linhas do log (lê no máximo os últimos 256 KiB), já decodificadas para UTF-8.
func TailLocalJob(id string, n int) (string, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return "", err
	}
	m, err := loadMeta(dir, id)
	if err != nil {
		return "", err
	}
	f, err := os.Open(m.LogPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const window = 256 * 1024
	off := int64(0)
	if st.Size() > window {
		off = st.Size() - window
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", err
	}
	if off > 0 { // descarta a linha cortada no começo da janela
		if i := strings.IndexByte(string(buf), '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	text := strings.TrimRight(decodeMixed(buf), "\r\n")
	lines := strings.Split(text, "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
