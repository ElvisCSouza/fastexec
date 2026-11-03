package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
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

	effectiveCmd := cmd
	if input != "" {
		b64Input := base64.StdEncoding.EncodeToString([]byte(input))
		effectiveCmd = fmt.Sprintf("printf '%%s' '%s' | base64 -d | %s", b64Input, cmd)
	}
	if cwd != "" {
		effectiveCmd = "cd \"" + cwd + "\" && " + effectiveCmd
	}
	effectiveCmd = "export CI=1 DEBIAN_FRONTEND=noninteractive PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb NO_COLOR=1; " + effectiveCmd

	if target.IsSSH {
		return ExecSSH(target.SSHHost, effectiveCmd, timeout)
	}
	return ExecDaemon(target.TCPAddr, effectiveCmd, timeout)
}
