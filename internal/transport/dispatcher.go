package transport

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"homelab/fastexec/internal/logger"
)

func ExecSSH(sshHost string, cmdStr string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", sshHost, cmdStr)
	cmd.SysProcAttr = hideWindowSysProcAttr()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	durationMs := time.Since(start).Milliseconds()
	logger.Debug("[ssh:%s] cmd: %.40s... -> exit %d (%dms)", sshHost, cmdStr, exitCode, durationMs)

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: durationMs,
	}, nil
}

func Dispatch(hostName string, cmd string, timeout time.Duration) (*ExecResult, error) {
	return DispatchWithInput(hostName, cmd, "", "", timeout)
}

func DispatchWithCwd(hostName string, cmd string, cwd string, timeout time.Duration) (*ExecResult, error) {
	return DispatchWithInput(hostName, cmd, cwd, "", timeout)
}

// DispatchArgv executa argv sem shell no host local. Em hosts remotos monta uma linha de comando com cada argumento
// entre aspas simples POSIX, que é o equivalente seguro de "sem interpretação" no destino.
func DispatchArgv(hostName string, argv []string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	target, err := ResolveHost(hostName)
	if err != nil {
		return nil, err
	}
	if cwd == "" && target.DefaultCwd != "" {
		cwd = target.DefaultCwd
	}
	if target.IsLocal {
		return ExecLocalArgv(argv, cwd, input, timeout)
	}
	// Daemon com SX: argv, cwd e stdin viajam como campos, sem shell e sem aspas.
	if res, ok, err := tryDaemonX(target, XRequest{Argv: argv, Cwd: cwd, Stdin: input}, timeout); ok || err != nil {
		return res, err
	}
	return DispatchWithInput(hostName, QuoteArgvPOSIX(argv), cwd, input, timeout)
}

// QuoteArgvPOSIX junta argv numa linha de comando POSIX com cada argumento entre aspas simples (sem interpretação).
func QuoteArgvPOSIX(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func DispatchWithInput(hostName string, cmd string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	target, err := ResolveHost(hostName)
	if err != nil {
		return nil, err
	}
	if cwd == "" && target.DefaultCwd != "" {
		cwd = target.DefaultCwd
	}
	if target.IsLocal {
		return ExecLocalWithInput(cmd, cwd, input, timeout)
	}

	// Daemon com SX: linha simples (só "programa args") vai direto, sem bash; o resto vai por bash -c, com cwd,
	// stdin e ambiente como campos do pedido (sem "cd &&", sem printf|base64 e sem o prefixo export).
	if !target.IsSSH {
		q := XRequest{Shell: cmd, Cwd: cwd, Stdin: input}
		if argv, simple := SimpleArgv(cmd); simple {
			q = XRequest{Argv: argv, Cwd: cwd, Stdin: input}
		}
		if res, ok, err := tryDaemonX(target, q, timeout); ok || err != nil {
			return res, err
		}
	}

	effectiveCmd := buildLegacyLine(cmd, cwd, input)

	if target.IsSSH {
		return ExecSSH(target.SSHHost, effectiveCmd, timeout)
	}
	return ExecDaemon(target.TCPAddr, effectiveCmd, timeout)
}
