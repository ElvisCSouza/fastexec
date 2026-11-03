package transport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
			return nil, fmt.Errorf("cwd inexistente ou não é diretório: %s", spec.cwd)
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Job Object: toda a árvore de processos do comando morre junto no timeout e ao fim da chamada (nada fica órfão).
	job, jerr := newJobObject()
	if jerr != nil {
		logger.Debug("[local] job object indisponível, seguindo só com kill do processo principal: %v", jerr)
	}

	var cmd *exec.Cmd
	if spec.argv != nil {
		cmd = exec.CommandContext(ctx, spec.argv[0], spec.argv[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, "cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`cmd.exe /s /c "%s"`, spec.cmdStr)}
	}
	if spec.cwd != "" {
		cmd.Dir = spec.cwd
	}
	cmd.Cancel = func() error {
		if job != nil {
			job.kill()
		}
		cmd.Process.Kill() // melhor esforço; o job já cobre a árvore quando disponível
		return nil
	}
	cmd.WaitDelay = waitDelay

	// stdin nunca fica pendurado: sem input, o filho recebe EOF imediato em vez de esperar um terminal que não existe.
	cmd.Stdin = strings.NewReader(spec.input)

	cmd.Env = localEnv()

	stdoutBuf, stderrBuf := &capBuffer{}, &capBuffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	exitCode := 0
	startErr := cmd.Start()
	if startErr != nil {
		if job != nil {
			job.close()
		}
		return &ExecResult{ExitCode: -1, Stderr: startErr.Error(), Duration: time.Since(start).Milliseconds()}, nil
	}
	if job != nil {
		if aerr := job.assign(cmd.Process.Pid); aerr != nil {
			logger.Debug("[local] não consegui pôr o processo no job: %v", aerr)
		}
	}

	werr := cmd.Wait()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if job != nil {
		job.close() // KILL_ON_JOB_CLOSE: o que ainda estiver vivo na árvore morre aqui
	}

	switch {
	case timedOut:
		exitCode = -1
	case werr == nil:
		exitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(werr, exec.ErrWaitDelay) && cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode() // o processo terminou; só um neto segurava o pipe
		} else {
			exitCode = -1
		}
	}

	durationMs := time.Since(start).Milliseconds()
	logger.Debug("[local] cmd: %.40s... -> exit %d (%dms) timeout=%v", spec.label(), exitCode, durationMs, timedOut)

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   decodeMixed(stdoutBuf.Bytes()),
		Stderr:   decodeMixed(stderrBuf.Bytes()),
		Duration: durationMs,
		TimedOut: timedOut,
	}, nil
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
