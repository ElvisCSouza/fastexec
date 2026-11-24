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
	jobStateStarting = "STARTING" // registro gravado antes do processo nascer
	jobStateRunning  = "RUNNING"
	jobStateExited   = "EXITED"          // sempre com ExitCode
	jobStateStopped  = "STOPPED"         // cancelado com a árvore confirmada morta
	jobStateExpired  = "EXPIRED"         // passou do TTL declarado (ExpiresAt) e foi encerrado
	jobStateFailed   = "FAILED"          // falha de infraestrutura ao iniciar (sem processo de usuário)
	jobStateUnknown  = "OUTCOME_UNKNOWN" // o processo sumiu sem rc registrado (perda do FastExec/resposta): não se sabe o fim

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
	ExpiresAt     time.Time  `json:"expires_at"`          // TTL declarado no start: CreatedAt + localJobTTL
	CancelAsked   bool       `json:"cancel_asked,omitempty"`
	Reason        string     `json:"reason,omitempty"` // texto curto para FAILED / OUTCOME_UNKNOWN
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
	// Arquivo temporário próprio por escrita: o exit watcher e o stop podem gravar o mesmo job ao mesmo tempo.
	f, err := os.CreateTemp(dir, m.ID+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	// Escrita atômica. No Windows o rename falha com "Acesso negado" enquanto um leitor (status/wait) tem o meta aberto: tenta de novo por um instante.
	var rerr error
	for i := 0; i < metaIOAttempts; i++ {
		if rerr = os.Rename(tmp, metaPath(dir, m.ID)); rerr == nil {
			return nil
		}
		time.Sleep(metaIOPause)
	}
	_ = os.Remove(tmp)
	return rerr
}

func loadMeta(dir, id string) (LocalJobMeta, error) {
	var m LocalJobMeta
	if !jobIDRegex.MatchString(id) {
		return m, fmt.Errorf("invalid job_id: %q", id)
	}
	var b []byte
	var err error
	// Leitura pode colidir com o rename de saveMeta no Windows: repete o erro transitório.
	for i := 0; i < metaIOAttempts; i++ {
		if b, err = os.ReadFile(metaPath(dir, id)); err == nil || os.IsNotExist(err) {
			break
		}
		time.Sleep(metaIOPause)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return m, fmt.Errorf("job '%s' not found (use action='list')", id)
		}
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// metaIOAttempts × metaIOPause é o teto da repetição de leitura/rename do meta (~1 s no pior caso).
const (
	metaIOAttempts = 100
	metaIOPause    = 10 * time.Millisecond
)

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

// startsHere: ids cujo start ainda está em curso neste processo (STARTING legítimo, não perda).
var (
	startsMu   sync.Mutex
	startsHere = map[string]bool{}
)

func markStarting(id string) {
	startsMu.Lock()
	startsHere[id] = true
	startsMu.Unlock()
}

func unmarkStarting(id string) {
	startsMu.Lock()
	delete(startsHere, id)
	startsMu.Unlock()
}

func startingHere(id string) bool {
	startsMu.Lock()
	defer startsMu.Unlock()
	return startsHere[id]
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
	if m.State == jobStateRunning || m.State == jobStateStarting {
		m.State = jobStateExited
		if m.CancelAsked {
			m.State = jobStateStopped
		}
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
	if startingHere(m.ID) { // start em andamento neste processo: ainda não é caso de perda
		info.State = jobStateStarting
		return info, nil
	}
	if m.State == jobStateRunning || m.State == jobStateStarting {
		// Sem tree viva. Só há estado final com prova: rc da goroutine deste processo (ou cancelamento confirmado).
		// Sem isso o fim é desconhecido (FastExec reiniciou ou perdeu o handle): OUTCOME_UNKNOWN, nunca "exited" sem rc.
		now := time.Now()
		m.EndedAt = &now
		code, ok := exitCodePublished(m.ID)
		if ok {
			m.ExitCode = &code
			dropExitSlot(m.ID)
		}
		switch {
		case m.CancelAsked:
			m.State = jobStateStopped
		case ok:
			m.State = jobStateExited
		default:
			m.State = jobStateUnknown
			m.Reason = "process tree gone without a recorded exit code (FastExec restarted or lost the job handle)"
		}
		info.Meta, info.State = m, m.State
		_ = saveMeta(dir, m)
		return info, nil
	}
	if m.ExitCode == nil && (m.State == jobStateExited || m.State == jobStateStopped) {
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
		if info.State == jobStateRunning && now.After(expiryOf(m)) {
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

// expiryOf: fim do TTL declarado no start (ExpiresAt; metadados antigos sem ele usam CreatedAt + localJobTTL).
func expiryOf(m LocalJobMeta) time.Time {
	if !m.ExpiresAt.IsZero() {
		return m.ExpiresAt
	}
	return m.CreatedAt.Add(localJobTTL)
}

// sameRequest diz se um job_id repetido traz o mesmo pedido. Só serve para o replay de um job_id já usado:
// jobs sem job_id NUNCA são comparados por argumentos (cada start sem id é um job novo).
func sameRequest(m LocalJobMeta, command string, argv []string, cwd string) bool {
	if m.Command != command || m.Cwd != cwd || len(m.Argv) != len(argv) {
		return false
	}
	for i := range argv {
		if m.Argv[i] != argv[i] {
			return false
		}
	}
	return true
}

// failStart grava o registro como FAILED (falha de infraestrutura ao iniciar) em vez de apagá-lo: o id continua
// consultável e o start repetido com o mesmo job_id devolve esse estado em vez de tentar de novo.
func failStart(dir string, meta *LocalJobMeta, why error) (*LocalJobMeta, bool, error) {
	now := time.Now()
	meta.State, meta.EndedAt, meta.Reason = jobStateFailed, &now, why.Error()
	unmarkStarting(meta.ID)
	_ = saveMeta(dir, *meta)
	return meta, false, why
}

// StartLocalJob inicia command (cmd.exe) ou argv (sem shell) em background, num Job Object nomeado, e devolve na hora.
// wantID é o job_id informado pelo cliente, usado como chave de idempotência: se já existe com o MESMO pedido,
// replayed=true e nenhum processo novo é criado; se existe com pedido diferente, é conflito. Vazio = job novo sempre.
// Antes de qualquer processo, o registro vai para o disco em STARTING (o handle existe antes da resposta).
func StartLocalJob(command string, argv []string, cwd, wantID string) (meta *LocalJobMeta, replayed bool, err error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, false, err
	}
	if cwd != "" {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			return nil, false, fmt.Errorf("cwd does not exist or is not a directory: %s", cwd)
		}
	}
	if wantID != "" && !jobIDRegex.MatchString(wantID) {
		return nil, false, fmt.Errorf("invalid job_id: %q (letters, digits, '_' and '-', up to 40 characters)", wantID)
	}
	if wantID != "" {
		if prev, lerr := loadMeta(dir, wantID); lerr == nil {
			if !sameRequest(prev, command, argv, cwd) {
				return nil, false, fmt.Errorf("job_id '%s' is already used by a different job; use a new job_id", wantID)
			}
			return &prev, true, nil // replay: mesmo pedido, mesmo handle, nada iniciado
		}
	}

	var running []string
	for _, j := range maintain(dir) {
		if j.State == jobStateRunning || j.State == jobStateStarting {
			running = append(running, j.Meta.ID)
		}
	}
	if len(running) >= LocalJobMaxActive {
		return nil, false, fmt.Errorf("limit of %d active local Windows jobs reached (%s); stop one with job(action='stop', job_id='...')",
			LocalJobMaxActive, strings.Join(running, ", "))
	}

	id := wantID
	if id == "" {
		id = newJobID()
	}
	if _, err := os.Stat(metaPath(dir, id)); err == nil {
		return nil, false, fmt.Errorf("a job with id '%s' already exists", id)
	}

	now := time.Now()
	logPath := filepath.Join(dir, id+".log")
	jobName := `Local\FastExec_Job_` + id
	rec := &LocalJobMeta{
		ID: id, Command: command, Argv: argv, Cwd: cwd, CreatedAt: now, ExpiresAt: now.Add(localJobTTL),
		JobObjectName: jobName, LogPath: logPath, State: jobStateStarting,
	}
	markStarting(id)
	if err := saveMeta(dir, *rec); err != nil {
		unmarkStarting(id)
		return nil, false, err
	}

	// Leitura/escrita normal, NÃO O_APPEND: no Windows o Go abre o append só com FILE_APPEND_DATA e o bash do Git
	// (msys) não consegue escrever nesse handle (log vazio, rc=1). O filho é o único escritor, então o offset
	// compartilhado do handle herdado basta.
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return failStart(dir, rec, err)
	}
	defer lf.Close() // o filho já terá herdado a sua própria cópia do handle

	// O job aninha dentro de qualquer job do host (Windows 8+), então não precisa de breakaway.
	job, err := newNamedJobObject(jobName)
	if err != nil {
		os.Remove(logPath)
		return failStart(dir, rec, fmt.Errorf("could not create the job object: %v", err))
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
		return failStart(dir, rec, fmt.Errorf("could not start: %v", err))
	}
	rec.PID = cmd.Process.Pid

	if aerr := job.assign(cmd.Process.Pid); aerr != nil {
		// quase sempre o processo já terminou (comando que falha na hora); se ainda vive, não dá para gerenciá-lo: mata
		done := make(chan struct{})
		code := 0
		go func() {
			code = exitCodeOf(cmd, cmd.Wait())
			close(done)
		}()
		select {
		case <-done: // saiu rápido: o job fica registrado como finalizado, com o rc real do processo
			end := time.Now()
			rec.State, rec.EndedAt, rec.ExitCode = jobStateExited, &end, &code
			unmarkStarting(id)
			if err := saveMeta(dir, *rec); err != nil {
				return nil, false, err
			}
			return rec, false, nil
		case <-time.After(500 * time.Millisecond):
			_ = cmd.Process.Kill()
			os.Remove(logPath)
			return failStart(dir, rec, fmt.Errorf("could not assign the process to the job object (%v); process killed", aerr))
		}
	}
	own(jobName, job.h) // a partir daqui o handle é guardado; o job vive enquanto este FastExec viver
	keep = true
	rec.State = jobStateRunning
	if err := saveMeta(dir, *rec); err != nil {
		_ = terminateByName(jobName)
		unmarkStarting(id)
		return nil, false, err
	}
	unmarkStarting(id)
	// Sem Release(): a goroutine espera o processo para guardar o rc. O handle é liberado no fim da espera.
	go recordExitWhenDone(dir, id, putExitSlot(id), cmd)
	return rec, false, nil
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
		CreatedAt: start, ExpiresAt: start.Add(localJobTTL), JobObjectName: jobName, LogPath: logPath, State: jobStateRunning}
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

// CancelReport é o veredito de um cancelamento, para quem chamou saber o que de fato aconteceu:
// "confirmed" = a árvore inteira morreu (nenhum processo vivo no Job Object); "accepted" = o pedido foi aceito pelo
// kernel mas a árvore ainda aparece viva no prazo curto de confirmação (consultar status depois);
// "denied" = nada foi cancelado (job já terminou, ainda está iniciando, ou o kernel recusou o terminate).
type CancelReport struct {
	Verdict string
	Reason  string
	Info    *LocalJobInfo
}

// cancelConfirmWait: quanto LocalJobStop espera a árvore sumir antes de responder "accepted" em vez de "confirmed".
var cancelConfirmWait = 1500 * time.Millisecond

// LocalJobStop encerra a árvore inteira do job pelo NOME do Job Object (atômico, sem PID) e devolve o veredito.
func LocalJobStop(id string) (*CancelReport, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return nil, err
	}
	m, err := loadMeta(dir, id)
	if err != nil {
		return nil, err
	}
	cur, err := inspect(dir, m)
	if err != nil {
		return nil, err
	}
	if cur.State == jobStateStarting {
		return &CancelReport{Verdict: "denied", Reason: "job is still starting; retry in a moment", Info: &cur}, nil
	}
	if cur.State != jobStateRunning {
		return &CancelReport{Verdict: "denied", Reason: "job already finished (state " + cur.State + ")", Info: &cur}, nil
	}
	if err := terminateByName(m.JobObjectName); err != nil {
		return &CancelReport{Verdict: "denied", Reason: "terminate refused by the kernel: " + err.Error(), Info: &cur}, nil
	}
	m.CancelAsked = true
	if err := saveMeta(dir, m); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(cancelConfirmWait)
	for {
		info, err := inspect(dir, m)
		if err != nil {
			return nil, err
		}
		if info.Active == 0 {
			return &CancelReport{Verdict: "confirmed", Reason: "process tree is gone", Info: &info}, nil
		}
		if time.Now().After(deadline) {
			return &CancelReport{Verdict: "accepted", Reason: "terminate accepted; tree still alive at confirmation time", Info: &info}, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
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

// LocalJobLogLines conta as linhas do log INTEIRO de um job (varredura streaming, sem buffer grande): o start que
// esperou o comando terminar precisa dizer quantas linhas mostrou de quantas existem, mesmo quando o log passa da
// janela de 256 KiB do tail.
func LocalJobLogLines(id string) (int, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return 0, err
	}
	m, err := loadMeta(dir, id)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(m.LogPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	total, tailNL, anyByte := 0, false, false
	for {
		n, rerr := f.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == '\n' {
				total++
			}
		}
		if n > 0 {
			anyByte, tailNL = true, buf[n-1] == '\n'
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, rerr
		}
	}
	if anyByte && !tailNL {
		total++ // última linha sem \n no fim do arquivo
	}
	return total, nil
}

// LocalJobLogChunk lê o log do job a partir do cursor (offset em bytes). Ver readLogChunk.
func LocalJobLogChunk(id string, cursor int64) (LogChunk, error) {
	dir, err := LocalJobsDir()
	if err != nil {
		return LogChunk{}, err
	}
	m, err := loadMeta(dir, id)
	if err != nil {
		return LogChunk{}, err
	}
	return readLogChunk(m.LogPath, cursor)
}
