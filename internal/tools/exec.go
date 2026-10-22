package tools

import (
	"fmt"
	"regexp"
	"time"

	"homelab/fastexec/internal/transport"
)

var sleepGuardRegex = regexp.MustCompile(`(?i)\b(sleep\s+\d+|timeout(\.exe)?\s+(\/t\s+)?\d+)\b`)

type ExecArgs struct {
	Host      string `json:"host"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd,omitempty"`
	Input     string `json:"input,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

func HandleExec(args ExecArgs) (string, error) {
	if args.Command == "" {
		return "", fmt.Errorf("parâmetro 'command' é obrigatório")
	}

	// Bloqueio determinístico de sleep / timeout artificial
	if sleepGuardRegex.MatchString(args.Command) {
		return "", fmt.Errorf("[FastExec Guard] Uso de 'sleep' ou 'timeout' é proibido. O FastExec é síncrono e orientado a eventos (retorna no milissegundo exato da conclusão do processo). Para tarefas longas, aumente o 'timeout_ms' (ex: timeout_ms: 300000). Para processos assíncronos, use 'fastexec.job' com action 'wait'")
	}

	timeout := transport.DefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}

	res, err := transport.DispatchWithInput(args.Host, args.Command, args.Cwd, args.Input, timeout)
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
