package transport

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"homelab/commander/internal/logger"
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
	target, err := ResolveHost(hostName)
	if err != nil {
		return nil, err
	}
	if target.IsLocal {
		return ExecLocal(cmd, timeout)
	}
	if target.IsSSH {
		return ExecSSH(target.SSHHost, cmd, timeout)
	}
	return ExecDaemon(target.TCPAddr, cmd, timeout)
}
