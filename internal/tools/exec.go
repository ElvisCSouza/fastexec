package tools

import (
	"fmt"
	"regexp"
	"strings"
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

	return formatExecResult(args.Command, *res)
}

func formatExecResult(command string, res transport.ExecResult) (string, error) {
	stdout := cleanOutput(res.Stdout)
	stderr := cleanOutput(res.Stderr)

	if res.ExitCode != 0 {
		if res.ExitCode == 1 && stdout == "" && stderr == "" {
			return NoResultText, fmt.Errorf("comando falhou com código 1")
		}
		errMsg := stderr
		if errMsg == "" {
			errMsg = stdout
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("comando falhou com exit code %d (sem saída)", res.ExitCode)
		}
		errMsg = capOutput(errMsg)
		if isWindowsCmdletMismatch(command, errMsg) {
			errMsg += "\n[FastExec Hint] O host 'local' (Windows) executa via cmd.exe nativo por performance. Para cmdlets PowerShell invoque 'powershell -NoProfile -Command ...'; para listar/manipular arquivos use a tool nativa 'file_ops'."
		}
		return fmt.Sprintf("[exit %d] %s", res.ExitCode, errMsg), fmt.Errorf("comando falhou com código %d", res.ExitCode)
	}

	if stdout == "" && stderr != "" {
		return capOutput(stderr), nil
	}

	return capOutput(stdout), nil
}

func isWindowsCmdletMismatch(cmd string, errMsg string) bool {
	lowerErr := strings.ToLower(errMsg)
	if !strings.Contains(lowerErr, "não é reconhecido") && !strings.Contains(lowerErr, "not recognized") {
		return false
	}
	lowerCmd := strings.ToLower(cmd)
	markers := []string{"select-object", "get-childitem", "get-content", "where-object", "$env:", "get-process", "set-executionpolicy"}
	for _, m := range markers {
		if strings.Contains(lowerCmd, m) {
			return true
		}
	}
	return false
}
