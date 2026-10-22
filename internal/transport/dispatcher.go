package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
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

func DispatchWithInput(hostName string, cmd string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	target, err := ResolveHost(hostName)
	if err != nil {
		return nil, err
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

	if target.IsSSH {
		return ExecSSH(target.SSHHost, effectiveCmd, timeout)
	}
	return ExecDaemon(target.TCPAddr, effectiveCmd, timeout)
}
