//go:build windows

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

	"homelab/fastexec/internal/config"
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
	ExitCode      *int       `json:"exit_code,omitempty"` // nil = ainda não conhecido (job vivo ou saída não coletada)
}

// LocalJobInfo é o estado vivo de um job, já reconciliado com o kernel.
type LocalJobInfo struct {
	Meta         LocalJobMeta
	State        string
	Active       uint32 // processos ativos no job
	LastActivity time.Time
	LogSize      int64
}

// LocalJobsDir devolve a pasta de estado: FASTEXEC_JOBS_DIR (testes), senão <pasta de dados>/jobs.
func LocalJobsDir() (string, error) {
	dir := os.Getenv("FASTEXEC_JOBS_DIR")
	if dir == "" {
		dir = filepath.Join(config.Get().Data(), "jobs")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

var procOpenJobObject = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

// openJobObject abre um job existente pelo nome. Devolve (0, errJobGone) se ele não existe mais: um job só existe
// enquanto tiver processo vivo ou handle aberto.
var errJobGone = errors.New("job object no longer exists")

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
		return m, fmt.Errorf("invalid job_id: %q", id)
	}
	b, err := os.ReadFile(metaPath(dir, id))
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("job '%s' not found (use action='list')", id)
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
// O Job Object conta processos vivos, não códigos de saída: quem inicia o job deixa uma goroutine esperando o processo
// (recordExitWhenDone) e publica o rc aqui. Um leitor que precise do código espera nesse canal em vez de correr contra a
// gravação do metadado. Só existe entrada para jobs iniciados por este processo; a espera é curta e só acontece uma vez.
type exitSlot struct {
	done chan struct{}
	code int
}

var (
	exitSlotsMu sync.Mutex
	exitSlots   = map[string]*exitSlot{}
)

// exitSlotWait é o teto da espera pelo rc de um job que acabou de terminar (variável para os testes).
var exitSlotWait = time.Second

func putExitSlot(id string) *exitSlot {
	s := &exitSlot{done: make(chan struct{})}
	exitSlotsMu.Lock()
	exitSlots[id] = s
	exitSlotsMu.Unlock()
	return s
}

func dropExitSlot(id string) {
	exitSlotsMu.Lock()
	delete(exitSlots, id)
	exitSlotsMu.Unlock()
}

// exitCodePublished devolve o rc de um job iniciado por este processo, esperando (no máximo exitSlotWait) a goroutine
// que espera o processo. ok=false quando não há nada a esperar ou quando o prazo passou.
func exitCodePublished(id string) (int, bool) {
	exitSlotsMu.Lock()
	s := exitSlots[id]
	exitSlotsMu.Unlock()
	if s == nil {
		return 0, false
	}
	select {
	case <-s.done:
		return s.code, true
	case <-time.After(exitSlotWait):
		return 0, false
	}
}

// recordExitWhenDone espera o processo do job terminar e grava o rc (e o fim) no metadado. É a única fonte do código de
// saída de um job local: sem isso o rc fica só no log, que o 'job start'/'status' não lê.
func recordExitWhenDone(dir, id string, slot *exitSlot, cmd *exec.Cmd) {
	code := exitCodeOf(cmd, cmd.Wait())
	slot.code = code
	close(slot.done)
	m, err := loadMeta(dir, id)
	if err != nil {
		return
	}
	now := time.Now()
	m.ExitCode, m.EndedAt = &code, &now
	if m.State == jobStateRunning {
		m.State = jobStateExited
	}
	_ = saveMeta(dir, m)
}

// inspect devolve o estado vivo de um job, já reconciliado com o kernel.
func inspect(dir string, m LocalJobMeta) (LocalJobInfo, error) {
	info := LocalJobInfo{Meta: m, State: m.State}
	info.LastActivity, info.LogSize = logActivity(m)

	if m.ExitCode != nil {
		dropExitSlot(m.ID) // o rc já está no metadado: ninguém mais precisa esperar pela goroutine
	}

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
		if code, ok := exitCodePublished(m.ID); ok {
			m.ExitCode = &code
			dropExitSlot(m.ID)
		}
		info.Meta, info.State = m, jobStateExited
		_ = saveMeta(dir, m)
	} else if m.ExitCode == nil {
		// o metadado pode ter sido gravado (por quem detectou o fim) antes de a goroutine do processo publicar o rc
		if code, ok := exitCodePublished(m.ID); ok {
			m.ExitCode = &code
			dropExitSlot(m.ID)
			info.Meta = m
			_ = saveMeta(dir, m)
		}
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
			return nil, fmt.Errorf("cwd does not exist or is not a directory: %s", cwd)
		}
	}

	var running []string
	for _, j := range maintain(dir) {
		if j.State == jobStateRunning {
			running = append(running, j.Meta.ID)
		}
	}
	if len(running) >= LocalJobMaxActive {
		return nil, fmt.Errorf("limit of %d active local Windows jobs reached (%s); stop one with job(action='stop', job_id='...')",
			LocalJobMaxActive, strings.Join(running, ", "))
	}

	id := wantID
	if id == "" {
		id = newJobID()
	} else if !jobIDRegex.MatchString(id) {
		return nil, fmt.Errorf("invalid job_id: %q (letters, digits, '_' and '-', up to 40 characters)", id)
	}
	if _, err := os.Stat(metaPath(dir, id)); err == nil {
		return nil, fmt.Errorf("a job with id '%s' already exists", id)
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
		return nil, fmt.Errorf("could not create the job object: %v", err)
	}
	keep := false
	defer func() {
		if !keep {
			job.close()
		}
	}()

	flags := uint32(createNoWindow | createNewProcessGroup)
	var cmd *exec.Cmd
	if line, ok := cmdCPayload(argv); ok {
		cmd = exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`cmd.exe /s /c "%s"`, line), HideWindow: true, CreationFlags: flags}
	} else if argv != nil {
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
		return nil, fmt.Errorf("could not start: %v", err)
	}
	meta := LocalJobMeta{
		ID: id, Command: command, Argv: argv, Cwd: cwd, PID: cmd.Process.Pid,
		CreatedAt: time.Now(), JobObjectName: jobName, LogPath: logPath, State: jobStateRunning,
	}

	if aerr := job.assign(cmd.Process.Pid); aerr != nil {
		// quase sempre o processo já terminou (comando que falha na hora); se ainda vive, não dá para gerenciá-lo: mata
		done := make(chan struct{})
		code := 0
		go func() {
			code = exitCodeOf(cmd, cmd.Wait())
			close(done)
		}()
		select {
		case <-done: // saiu rápido: o job fica registrado como finalizado, com o log para consulta
			now := time.Now()
			meta.State, meta.EndedAt, meta.ExitCode = jobStateExited, &now, &code
			if err := saveMeta(dir, meta); err != nil {
				return nil, err
			}
			return &meta, nil
		case <-time.After(500 * time.Millisecond):
			_ = cmd.Process.Kill()
			os.Remove(logPath)
			return nil, fmt.Errorf("could not assign the process to the job object (%v); process killed", aerr)
		}
	}
	own(jobName, job.h) // a partir daqui o handle é guardado; o job vive enquanto este FastExec viver
	keep = true
	// Sem Release(): a goroutine espera o processo para guardar o rc. O handle é liberado no fim da espera.
	go recordExitWhenDone(dir, id, putExitSlot(id), cmd)

	if err := saveMeta(dir, meta); err != nil {
		_ = terminateByName(jobName)
		return nil, err
	}
	return &meta, nil
}

// handoffToJob transforma um exec que estourou o timeout em job de fundo, sem matar nada: o job object do exec
// (nomeado) passa a ser do registro de jobs, a saída capturada vai para o log do job e o resto da saída também.
// done entrega o fim do processo; aí o log ganha "[exit N]" e fecha. Devolve o id e a saída até agora.
func handoffToJob(spec localSpec, start time.Time, cmd *exec.Cmd, job *jobObject, jobName string,
	out, errOut *switchWriter, done <-chan error) (string, []byte, []byte, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return "", nil, nil, err
	}
	running := 0
	for _, j := range maintain(dir) {
		if j.State == jobStateRunning {
			running++
		}
	}
	if running >= LocalJobMaxActive {
		return "", nil, nil, fmt.Errorf("limit of %d active local jobs reached", LocalJobMaxActive)
	}
	id := newJobID()
	logPath := filepath.Join(dir, id+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", nil, nil, err
	}
	meta := LocalJobMeta{ID: id, Command: spec.cmdStr, Argv: spec.argv, Cwd: spec.cwd, PID: cmd.Process.Pid,
		CreatedAt: start, JobObjectName: jobName, LogPath: logPath, State: jobStateRunning}
	if err := saveMeta(dir, meta); err != nil {
		f.Close()
		os.Remove(logPath)
		return "", nil, nil, err
	}
	own(jobName, job.h) // o handle passa a ser do registro: o job vive enquanto este FastExec viver

	stdout := out.toFile(f)
	stderr := errOut.toFile(f)
	go func() {
		werr := <-done
		out.detach()
		errOut.detach()
		code := exitCodeOf(cmd, werr)
		fmt.Fprintf(f, "\n[exit %d]\n", code)
		f.Close()
		// o rc também vai para o metadado: 'status' responde na hora, sem ler o log
		if m, lerr := loadMeta(dir, id); lerr == nil {
			now := time.Now()
			m.ExitCode, m.EndedAt = &code, &now
			if m.State == jobStateRunning {
				m.State = jobStateExited
			}
			_ = saveMeta(dir, m)
		}
	}()
	return id, stdout, stderr, nil
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
		return nil, fmt.Errorf("could not stop the job: %v", err)
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
