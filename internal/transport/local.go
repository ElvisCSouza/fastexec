package transport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"homelab/fastexec/internal/logger"
)

func ExecLocal(cmdStr string, timeout time.Duration) (*ExecResult, error) {
	return ExecLocalWithInput(cmdStr, "", "", timeout)
}

func ExecLocalWithCwd(cmdStr string, cwd string, timeout time.Duration) (*ExecResult, error) {
	return ExecLocalWithInput(cmdStr, cwd, "", timeout)
}

func ExecLocalWithInput(cmdStr string, cwd string, input string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: fmt.Sprintf(`cmd.exe /s /c "%s"`, cmdStr),
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	// Injeta Git\usr\bin no PATH para comandos Unix comuns (cp, mv, rm, ls, cat, grep) funcionarem nativamente no Windows
	gitUsrBin := `C:\Program Files\Git\usr\bin`
	env := os.Environ()
	pathUpdated := false
	for i, e := range env {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			env[i] = e + ";" + gitUsrBin
			pathUpdated = true
			break
		}
	}
	if !pathUpdated {
		env = append(env, "PATH="+gitUsrBin)
	}
	cmd.Env = env

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
	logger.Debug("[local] cmd: %.40s... -> exit %d (%dms)", cmdStr, exitCode, durationMs)

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: durationMs,
	}, nil
}
