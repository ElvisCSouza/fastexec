package tools

import (
	"fmt"
	"time"

	"homelab/fastexec/internal/transport"
)

type ExecArgs struct {
	Host      string `json:"host"`
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

func HandleExec(args ExecArgs) (string, error) {
	if args.Command == "" {
		return "", fmt.Errorf("parâmetro 'command' é obrigatório")
	}

	timeout := transport.DefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}

	res, err := transport.Dispatch(args.Host, args.Command, timeout)
	if err != nil {
		return "", err
	}

	if res.ExitCode != 0 {
		errMsg := res.Stderr
		if errMsg == "" {
			errMsg = res.Stdout
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("comando falhou com exit code %d (sem saída)", res.ExitCode)
		}
		return fmt.Sprintf("[exit %d] %s", res.ExitCode, errMsg), fmt.Errorf("comando falhou com código %d", res.ExitCode)
	}

	if res.Stdout == "" && res.Stderr != "" {
		return res.Stderr, nil
	}

	return res.Stdout, nil
}
