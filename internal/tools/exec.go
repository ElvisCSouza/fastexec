package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// MaxClientTimeoutMs é o teto do timeout_ms do exec: o cliente MCP (Claude Desktop) desiste de uma chamada pendente em
// ~240 s; com 210 s o FastExec responde (com a saída parcial) 30 s antes de o transporte ser cortado.
const MaxClientTimeoutMs = 210000

// maxClientTimeout é variável só para os testes poderem encurtar o teto.
var maxClientTimeout = time.Duration(MaxClientTimeoutMs) * time.Millisecond

var sleepGuardRegex = regexp.MustCompile(`(?i)\b(sleep\s+\d+|timeout(\.exe)?\s+(\/t\s+)?\d+)\b`)

// shellExes são os executáveis cujo argumento de texto é interpretado como comando de shell. O guard de sleep só faz
// sentido neles: num argv para node/python o texto é código ou dado, não uma linha de shell.
var shellExes = map[string]bool{"cmd": true, "bash": true, "sh": true, "zsh": true, "powershell": true, "pwsh": true}

type ExecArgs struct {
	Host string `json:"host"`
	// Command é uma linha de shell (pipelines, redirecionamento). Argv é a alternativa sem shell: o executável recebe
	// cada argumento exatamente como enviado. Informe um dos dois.
	Command   string   `json:"command,omitempty"`
	Argv      []string `json:"argv,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Input     string   `json:"input,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
}

const sleepGuardMsg = "[FastExec Guard] Uso de 'sleep' ou 'timeout' é proibido. O FastExec é síncrono e orientado a eventos (retorna no milissegundo exato da conclusão do processo). Para tarefas longas, aumente o 'timeout_ms' (ex: timeout_ms: 300000). Para processos assíncronos, use 'fastexec.job' com action 'wait'"

func HandleExec(args ExecArgs) (string, error) {
	hasCmd, hasArgv := args.Command != "", len(args.Argv) > 0
	switch {
	case hasCmd && hasArgv:
		return "", fmt.Errorf("informe só um: 'command' (linha de shell) ou 'argv' (sem shell)")
	case !hasCmd && !hasArgv:
		return "", fmt.Errorf("parâmetro 'command' (ou 'argv') é obrigatório")
	}

	// Bloqueio determinístico de sleep / timeout artificial
	label := args.Command
	if hasArgv {
		label = strings.Join(args.Argv, " ")
		exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		if exe == "sleep" || exe == "timeout" || (shellExes[exe] && sleepGuardRegex.MatchString(label)) {
			return "", fmt.Errorf(sleepGuardMsg)
		}
	} else if sleepGuardRegex.MatchString(label) {
		return "", fmt.Errorf(sleepGuardMsg)
	}

	timeout := transport.DefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	clampedFrom := time.Duration(0)
	if timeout > maxClientTimeout {
		clampedFrom, timeout = timeout, maxClientTimeout
	}

	var (
		res *transport.ExecResult
		err error
	)
	if hasArgv {
		res, err = transport.DispatchArgv(args.Host, args.Argv, args.Cwd, args.Input, timeout)
	} else {
		res, err = transport.DispatchWithInput(args.Host, args.Command, args.Cwd, args.Input, timeout)
	}
	if err != nil {
		return "", err
	}

	out, ferr := formatExecResult(label, *res)
	if res.TimedOut && clampedFrom > 0 {
		out += fmt.Sprintf("\nO timeout_ms pedido (%d) foi limitado a %d ms, o teto antes de o cliente MCP cortar a chamada.", clampedFrom.Milliseconds(), timeout.Milliseconds())
	}
	return out, ferr
}

func formatExecResult(command string, res transport.ExecResult) (string, error) {
	stdout := cleanOutput(res.Stdout)
	stderr := cleanOutput(res.Stderr)

	if res.TimedOut {
		return formatTimeout(res.Duration, stdout, stderr)
	}

	if res.ExitCode != 0 {
		if res.ExitCode == 1 && stdout == "" && stderr == "" {
			return NoResultText, nil
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

// formatTimeout devolve ao agente um desfecho inequívoco: estourou o prazo, a árvore foi encerrada e o que foi emitido
// até o corte. Assim ele nunca precisa adivinhar se o comando rodou.
func formatTimeout(elapsedMs int64, stdout, stderr string) (string, error) {
	head := fmt.Sprintf("[timeout após %.1fs; árvore de processos encerrada]", float64(elapsedMs)/1000)
	partial := stdout
	if stderr != "" {
		if partial != "" {
			partial += "\n"
		}
		partial += "[stderr]\n" + stderr
	}
	msg := head
	if partial != "" {
		msg += "\nSaída parcial capturada até o corte:\n" + capOutput(partial)
	}
	msg += "\nPara comandos que exigem mais de 3 minutos, use a tool 'job' (action='start')."
	return msg, fmt.Errorf("timeout após %.1fs", float64(elapsedMs)/1000)
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
