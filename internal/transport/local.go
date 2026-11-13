package transport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/env"
	"homelab/fastexec/internal/logger"
)

// waitDelay limita a espera por pipes que um filho deixou abertos depois de o processo principal terminar ou ser morto.
const waitDelay = 150 * time.Millisecond

func ExecLocal(cmdStr string, timeout time.Duration) (*ExecResult, error) {
	return ExecLocalWithInput(cmdStr, "", "", timeout)
}

func ExecLocalWithCwd(cmdStr string, cwd string, timeout time.Duration) (*ExecResult, error) {
	return ExecLocalWithInput(cmdStr, cwd, "", timeout)
}

// ExecLocalWithInput roda uma linha de comando via cmd.exe (pipelines, redirecionamento e demais recursos do shell).
func ExecLocalWithInput(cmdStr string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	return runLocal(localSpec{cmdStr: cmdStr, cwd: cwd, input: input}, timeout)
}

// ExecLocalArgv roda argv[0] com os argumentos exatamente como dados, sem passar por shell: nenhum metacaractere
// (> < | & %VAR% aspas) é interpretado antes de o executável receber o argumento.
func ExecLocalArgv(argv []string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("argv vazio")
	}
	return runLocal(localSpec{argv: argv, cwd: cwd, input: input}, timeout)
}

type localSpec struct {
	cmdStr string   // usado quando argv é nil
	argv   []string // se presente, execução direta sem shell
	cwd    string
	input  string
}

func (s localSpec) label() string {
	if s.argv != nil {
		return strings.Join(s.argv, " ")
	}
	return s.cmdStr
}

func runLocal(spec localSpec, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if spec.cwd != "" {
		if st, err := os.Stat(spec.cwd); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("cwd does not exist or is not a directory: %s", spec.cwd)
		}
	}

	start := time.Now()

	// pwsh -Command - (script no stdin) descarta o bloco multilinha final sem linha em branco depois dele.
	argv, stdin := spec.argv, pwshStdinInput(spec.argv, spec.input)

	// Job Object: toda a árvore de processos do comando morre junto no timeout e ao fim da chamada (nada fica órfão).
	// Com HandoffOnTimeout o job tem nome, para poder virar job de fundo no estouro.
	job, jobName, jerr := newExecJob(HandoffOnTimeout)
	if jerr != nil {
		logger.Debug("[local] job object unavailable, falling back to killing only the main process: %v", jerr)
	}

	var cmd *exec.Cmd
	if line, ok := cmdCPayload(argv); ok {
		cmd = buildShellCommand(context.Background(), line)
	} else if argv != nil {
		effectiveArgv := ResolveShellArgv(argv)
		cmd = exec.Command(effectiveArgv[0], effectiveArgv[1:]...)
		cmd.SysProcAttr = hideWindowSysProcAttr()
	} else {
		cmd = buildShellCommand(context.Background(), spec.cmdStr)
	}
	if spec.cwd != "" {
		cmd.Dir = spec.cwd
	}
	cmd.WaitDelay = waitDelay

	// stdin nunca fica pendurado: sem input, o filho recebe EOF imediato em vez de esperar um terminal que não existe.
	cmd.Stdin = strings.NewReader(stdin)

	cmd.Env = localEnv()

	stdoutBuf, stderrBuf := &switchWriter{}, &switchWriter{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	exitCode := 0
	startErr := cmd.Start()
	if startErr != nil {
		if job != nil {
			job.close()
		}
		return &ExecResult{ExitCode: -1, Stderr: startErr.Error(), Duration: time.Since(start).Milliseconds(), Local: true}, nil
	}
	if job != nil {
		if aerr := job.assign(cmd.Process.Pid); aerr != nil {
			logger.Debug("[local] could not assign the process to the job: %v", aerr)
		}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var werr error
	timedOut := false
	select {
	case werr = <-done:
	case <-timer.C:
		// estourou: com o processo vivendo além da chamada (daemon/servidor MCP), vira job de fundo em vez de morrer
		if job != nil && jobName != "" && HandoffOnTimeout {
			id, out, errOut, herr := handoffToJob(spec, start, cmd, job, jobName, stdoutBuf, stderrBuf, done)
			if herr == nil {
				return &ExecResult{ExitCode: 0, Stdout: decodeMixed(out), Stderr: decodeMixed(errOut),
					Duration: time.Since(start).Milliseconds(), Local: true, JobID: id}, nil
			}
			logger.Debug("[local] handoff to job failed, killing instead: %v", herr)
		}
		timedOut = true
		if job != nil {
			job.kill()
		}
		_ = cmd.Process.Kill() // melhor esforço; o job já cobre a árvore quando disponível
		werr = <-done
	}
	var leftover uint32
	if job != nil {
		if !timedOut {
			leftover = job.alive() // contar antes do close: o que sobrar aqui é morto pelo kernel
		}
		job.close() // KILL_ON_JOB_CLOSE: o que ainda estiver vivo na árvore morre aqui
	}

	if timedOut {
		exitCode = -1
	} else {
		exitCode = exitCodeOf(cmd, werr)
	}

	durationMs := time.Since(start).Milliseconds()
	logger.Debug("[local] cmd: %.40s... -> exit %d (%dms) timeout=%v", spec.label(), exitCode, durationMs, timedOut)

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   decodeMixed(stdoutBuf.Bytes()),
		Stderr:   decodeMixed(stderrBuf.Bytes()),
		Duration: durationMs,
		TimedOut: timedOut,
		Leftover: leftover,
		Local:    true,
	}, nil
}

// exitCodeOf traduz o retorno do Wait em código de saída (-1 quando não há código).
func exitCodeOf(cmd *exec.Cmd, werr error) int {
	if werr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) {
		return exitErr.ExitCode()
	}
	if errors.Is(werr, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode() // o processo terminou; só um neto segurava o pipe
	}
	return -1
}

// HandoffOnTimeout: no estouro do timeout, o exec local (Windows) vira job de fundo em vez de matar a árvore.
// Ligado só onde o processo vive além da chamada (daemon local e servidor MCP); no CLI de um comando só, o job
// morreria junto com o processo, então lá o estouro continua matando.
var HandoffOnTimeout bool

// switchWriter captura a saída em memória e, depois do handoff, passa a escrever no log do job.
type switchWriter struct {
	mu   sync.Mutex
	buf  capBuffer
	file *os.File
}

func (w *switchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_, _ = w.file.Write(p) // erro de disco não pode matar o filho por pipe quebrado
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *switchWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Bytes()
}

// toFile grava no arquivo o que já foi capturado e manda o resto para lá; devolve a captura.
func (w *switchWriter) toFile(f *os.File) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	snap := w.buf.Bytes()
	_, _ = f.Write(snap)
	w.file = f
	return snap
}

// detach para de escrever no arquivo (antes de fechá-lo).
func (w *switchWriter) detach() {
	w.mu.Lock()
	w.file = nil
	w.mu.Unlock()
}

// headlessEnv define as variáveis canônicas para execução 100% autônoma ("interface zero / humano zero").
// Desativa animações, spinners, prompts [y/n], checagens de rede e paginadores na origem.
var headlessEnv = []string{
	"CI=1",
	"DEBIAN_FRONTEND=noninteractive",
	"PAGER=cat",
	"GIT_PAGER=cat",
	"GIT_TERMINAL_PROMPT=0",
	"TERM=dumb",
	"NO_COLOR=1",
	"POWERSHELL_UPDATECHECK=Off",
	"DOTNET_CLI_TELEMETRY_OPTOUT=1",
	"NPM_CONFIG_UPDATE_NOTIFIER=false",
	"PIP_NO_INPUT=1",
	"PIP_DISABLE_PIP_VERSION_CHECK=1",
}

// localEnv monta o ambiente dos processos filhos locais: registro do Windows (Machine + User) + Git\usr\bin no PATH para
// cp, mv, rm, ls, cat, grep funcionarem + variáveis headless canônicas.
func localEnv() []string {
	envList := env.MountForCurrentProcess()
	gitUsrBin := `C:\Program Files\Git\usr\bin`
	pathUpdated := false
	for i, e := range envList {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			envList[i] = e + ";" + gitUsrBin
			pathUpdated = true
			break
		}
	}
	if !pathUpdated {
		envList = append(envList, "PATH="+gitUsrBin)
	}
	return append(envList, headlessEnv...)
}
