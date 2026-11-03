package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// MaxClientTimeoutMs é o teto do timeout_ms do exec: o Claude Desktop corta a chamada do dispositivo aos
// 60 s; com 50 s o FastExec responde (com a saída parcial) 10 s antes de o transporte ser cortado.
const MaxClientTimeoutMs = 50000

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

	label := args.Command
	if hasArgv {
		label = strings.Join(args.Argv, " ")
	}

	// Auto-remediação de sleep puro <= 15s com aviso pedagógico
	if out, handled, err := tryRemediateSleep(label, args); handled {
		return out, err
	}

	// Bloqueio determinístico de sleep / timeout artificial
	if hasArgv {
		exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		if exe == "sleep" || exe == "timeout" || (shellExes[exe] && sleepGuardRegex.MatchString(label)) {
			return "", fmt.Errorf(sleepGuardMsg)
		}
	} else if sleepGuardRegex.MatchString(label) {
		return "", fmt.Errorf(sleepGuardMsg)
	}

	// Auto-remediação de cat / type puro (conversão para read_file com notificação pedagógica)
	if out, handled, err := tryRemediateCat(args); handled {
		return out, err
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
	if ferr == nil && res.ExitCode == 0 && !res.TimedOut {
		if hint := detectNudge(label, args); hint != "" {
			if out != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + hint
			} else {
				out = hint
			}
		}
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
	msg += "\n[FastExec Guidance] Para tarefas longas (> 30s), use a tool 'job' (action='start')."
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

var pureSleepRegex = regexp.MustCompile(`(?i)^\s*(?:sleep|timeout(?:\.exe)?(?:\s+\/t)?|start-sleep(?:\s+(?:-s|-seconds))?)\s+(\d+)\s*$`)

var sleepFn = time.Sleep

// tryRemediateSleep intercepta comandos exclusivos de sleep / timeout.
// Se a pausa pedida for <= 15s, executa a espera controlada em Go e retorna aviso pedagógico.
// Se > 15s, bloqueia com instrução explícita de usar job assíncrono.
func tryRemediateSleep(label string, args ExecArgs) (string, bool, error) {
	var targetStr string
	if len(args.Argv) > 0 {
		exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		if exe == "sleep" || exe == "timeout" {
			targetStr = strings.Join(args.Argv, " ")
		}
	} else {
		targetStr = strings.TrimSpace(label)
	}

	if targetStr == "" {
		return "", false, nil
	}

	matches := pureSleepRegex.FindStringSubmatch(targetStr)
	if len(matches) < 2 {
		return "", false, nil
	}

	var sec int
	_, err := fmt.Sscanf(matches[1], "%d", &sec)
	if err != nil {
		return "", false, nil
	}

	if sec > 15 {
		return "", true, fmt.Errorf("[FastExec Guard] Pausa de %ds bloqueada no exec síncrono (>15s excede o limite aceitável de espera). Para processos em background, inicie um job ou utilize agendamentos para não prender a sessão do LLM.", sec)
	}

	sleepFn(time.Duration(sec) * time.Second)
	out := fmt.Sprintf("[FastExec Auto-Remediation] Pausa de %ds concluída via timer nativo em Go.\n(Dica: o FastExec é orientado a eventos. Para sincronização de processos, prefira aguardar a porta/arquivo do alvo ou usar 'job').", sec)
	return out, true, nil
}

// detectCatCommand extrai o nome do comando e o caminho do arquivo caso seja uma leitura simples.
func detectCatCommand(args ExecArgs) (string, string) {
	var cmdName string
	var candidatePath string

	if args.Command != "" {
		cmd := strings.TrimSpace(args.Command)
		if strings.ContainsAny(cmd, "|><;&`$\n") {
			return "", ""
		}
		fields := strings.Fields(cmd)
		if len(fields) < 2 {
			return "", ""
		}
		cmdName = strings.ToLower(filepath.Base(fields[0]))
		cmdName = strings.TrimSuffix(cmdName, ".exe")

		switch cmdName {
		case "cat", "type", "get-content", "gc":
			candidatePath = fields[len(fields)-1]
		default:
			return "", ""
		}
	} else if len(args.Argv) >= 2 {
		cmdName = strings.ToLower(filepath.Base(args.Argv[0]))
		cmdName = strings.TrimSuffix(cmdName, ".exe")
		switch cmdName {
		case "cat", "type", "get-content", "gc":
			candidatePath = args.Argv[len(args.Argv)-1]
		default:
			return "", ""
		}
	} else {
		return "", ""
	}

	candidatePath = strings.Trim(candidatePath, `"'`)

	if candidatePath != "" && !strings.HasPrefix(candidatePath, "-") && !strings.HasPrefix(candidatePath, "/") {
		return cmdName, candidatePath
	} else if candidatePath != "" {
		if cmdName == "cat" || cmdName == "get-content" || cmdName == "gc" || strings.Contains(candidatePath, "/") || strings.Contains(candidatePath, "\\") {
			return cmdName, candidatePath
		}
	}

	return "", ""
}

// tryRemediateCat tenta ler o arquivo diretamente via HandleReadFile (zero-process), retornando
// o conteúdo com um cabeçalho explicativo pedagógico.
func tryRemediateCat(args ExecArgs) (string, bool, error) {
	cmdName, candidatePath := detectCatCommand(args)
	if candidatePath == "" {
		return "", false, nil
	}

	filePath := candidatePath
	if args.Cwd != "" && !filepath.IsAbs(filePath) && !strings.HasPrefix(filePath, "/") && !strings.HasPrefix(filePath, "\\") {
		filePath = filepath.Join(args.Cwd, filePath)
	}

	content, err := HandleReadFile(ReadFileArgs{
		Host: args.Host,
		Path: filePath,
	})
	if err != nil {
		return "", true, fmt.Errorf("[FastExec Auto-Remediation] O comando '%s' foi identificado como leitura de arquivo, mas falhou ao ler '%s' via read_file: %w", cmdName, candidatePath, err)
	}

	cmdLabel := args.Command
	if cmdLabel == "" {
		cmdLabel = strings.Join(args.Argv, " ")
	}

	out := fmt.Sprintf("[FastExec Auto-Remediation] O comando '%s' foi convertido automaticamente para a tool nativa 'read_file' (zero-process).\nNa próxima, invoque 'read_file' diretamente para economizar tempo e tokens.\n---\n%s", cmdLabel, content)
	return out, true, nil
}

// checkCatGuard bloqueia cirurgicamente invocações puras de 'cat' ou 'type' para leitura de arquivos,
// orientando o agente a usar a tool nativa 'read_file'. Pipelines (|), redirecionamentos (>) ou encadeamentos
// são transformações legítimas de shell e permanecem permitidos.
func checkCatGuard(args ExecArgs) error {
	_, candidatePath := detectCatCommand(args)
	if candidatePath == "" {
		return nil
	}
	return fmt.Errorf("[FastExec Guard] Para leitura de arquivos use a tool nativa 'read_file' (path='%s'). Ela suporta paginação segura (offset/limit), evita criação de subprocessos e preserva integridade sem risco de estouro de buffer.", candidatePath)
}

// detectNudge sugere ferramentas ou módulos especializados do FastExec no rodapé da resposta quando
// o comando executado no shell tem uma alternativa superior nativa.
func detectNudge(label string, args ExecArgs) string {
	cmd := strings.TrimSpace(label)
	if strings.ContainsAny(cmd, "|><;&`$\n") {
		return ""
	}
	var fields []string
	if args.Command != "" {
		fields = strings.Fields(cmd)
	} else if len(args.Argv) > 0 {
		fields = args.Argv
	}
	if len(fields) == 0 {
		return ""
	}

	exe := strings.ToLower(filepath.Base(fields[0]))
	exe = strings.TrimSuffix(exe, ".exe")

	switch exe {
	case "ls", "dir":
		return "[FastExec Hint] Para listar arquivos e diretórios, prefira a tool nativa 'file_ops' (action='list')."
	case "docker":
		return "[FastExec Hint] Módulo 'docker' disponível sob demanda via tool_manage (ex: tool_manage(action='call', tool='docker_ps'))."
	case "sqlite3", "duckdb":
		return "[FastExec Hint] Para consultas SQL diretas com saída JSON estruturada, prefira o módulo 'sql'."
	}
	return ""
}
