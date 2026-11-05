package tools

import (
	"encoding/json"
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

// execDurationWarningMs é o limiar para alertar o agente de que um comando síncrono está perigosamente longo (>30s).
var execDurationWarningMs int64 = 30000

var sleepGuardRegex = regexp.MustCompile(`(?i)\b(sleep\s+\d+|timeout(\.exe)?\s+(\/t\s+)?\d+|time\.sleep\(\s*\d+)\b`)

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

	// Remove payload de blocos heredoc da análise (evita falsos positivos com documentação/scripts)
	cmdToCheck := stripHeredocs(label)

	// Auto-remediação de sleep puro <= 15s com aviso pedagógico
	if out, handled, err := tryRemediateSleep(cmdToCheck, args); handled {
		return out, err
	}

	// Bloqueio determinístico de sleep / timeout artificial
	if checkSleepGuard(cmdToCheck, args) {
		return "", fmt.Errorf(sleepGuardMsg)
	}

	// Auto-remediação de prefixo redundante 'cd ~/workspace &&' ou 'cd ~/workspace;' no noteserver
	var cdHint string
	if hasCmd {
		if out, handled, hint := tryRemediateCd(&args); handled {
			return out, nil
		} else {
			cdHint = hint
			label = args.Command
		}
	}

	// Auto-remediação de cat / type puro (conversão para read_file com notificação pedagógica)
	if out, handled, err := tryRemediateCat(args); handled {
		if cdHint != "" && out != "" {
			out = cdHint + "\n---\n" + out
		}
		return out, err
	}

	// Auto-remediação de listagem pura dir / ls / get-childitem (conversão para file_ops list com notificação pedagógica)
	if out, handled, err := tryRemediateList(args); handled {
		if cdHint != "" && out != "" {
			out = cdHint + "\n---\n" + out
		}
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
		if res.Duration > execDurationWarningMs {
			warning := fmt.Sprintf("[FastExec Warning] Este comando levou %.1fs síncronos no exec. Comandos longos (>30s) correm risco de timeout rígido do cliente MCP (60s no Claude Desktop). Use a tool 'job' (action='start') para execução assíncrona desacoplada.", float64(res.Duration)/1000)
			if out != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + warning
			} else {
				out = warning
			}
		}
		if hint := detectNudge(label, args); hint != "" {
			if out != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + hint
			} else {
				out = hint
			}
		}
	}
	if cdHint != "" {
		if out != "" {
			out = cdHint + "\n---\n" + out
		} else {
			out = cdHint
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
		if (res.ExitCode == 1 || res.ExitCode == 255) && stdout == "" && isSearchOrDiffCommand(command) {
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
			errMsg += "\n[FastExec Hint] O host 'local' (Windows) executa via cmd.exe nativo por performance. Para cmdlets PowerShell invoque 'pwsh -NoProfile -Command ...'; para listar/manipular arquivos use a tool nativa 'file_ops'."
		}
		if isBenignExitCode1(command, stdout, stderr) {
			return fmt.Sprintf("[exit %d] %s", res.ExitCode, errMsg), nil
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

// stripHeredocs remove o payload de blocos heredoc (<<'EOF' ... EOF) antes de analisar comandos.
// Isso evita que texto de documentação, especificações ou scripts gravados via heredoc
// acionem indevidamente palavras-chave de guards do shell (falsos positivos).
func stripHeredocs(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}

	lines := strings.Split(cmd, "\n")
	var result []string
	var inHeredoc bool
	var heredocDelim string
	var allowTabs bool

	reHeredoc := regexp.MustCompile(`<<(-)?\s*['"]?([A-Za-z0-9_]+)['"]?`)

	for _, line := range lines {
		if inHeredoc {
			checkLine := line
			if allowTabs {
				checkLine = strings.TrimLeft(checkLine, "\t")
			}
			checkLine = strings.TrimRight(checkLine, "\r ")
			if checkLine == heredocDelim {
				inHeredoc = false
				heredocDelim = ""
				allowTabs = false
			}
			continue
		}

		result = append(result, line)
		if matches := reHeredoc.FindStringSubmatch(line); len(matches) >= 3 {
			inHeredoc = true
			allowTabs = matches[1] == "-"
			heredocDelim = matches[2]
		}
	}

	return strings.Join(result, "\n")
}

// coreutilsTimeoutRegex identifica o utilitário GNU coreutils `timeout [options] <duration> <command> [args]`.
// Ex: `timeout 40 docker exec ...` ou `timeout 10s curl http://...`.
// O grupo 1 captura o comando filho executado com deadline.
var coreutilsTimeoutRegex = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)timeout(?:\s+-[A-Za-z0-9_-]+|\s+--[a-z0-9_-]+(?:=\S+)?)*\s+\d+(?:\.\d+)?[smhd]?\s+([a-zA-Z0-9_\.\/-]+)`)

func isCoreutilsTimeout(cmd string) bool {
	matches := coreutilsTimeoutRegex.FindStringSubmatch(cmd)
	if len(matches) < 2 {
		return false
	}
	subCmd := strings.ToLower(filepath.Base(matches[1]))
	return subCmd != "" && !strings.HasPrefix(subCmd, "-")
}

// checkSleepGuard verifica se a linha de comando tenta uma pausa passiva artificial (sleep ou timeout passivo).
// Ele ignora wrappers de processo legítimos como o `timeout` do GNU coreutils.
func checkSleepGuard(cmd string, args ExecArgs) bool {
	hasArgv := len(args.Argv) > 0
	if hasArgv {
		exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		if exe == "sleep" {
			return true
		}
		if exe == "timeout" {
			return !isCoreutilsTimeout(cmd)
		}
		if (shellExes[exe] || exe == "python" || exe == "python3" || exe == "py") && sleepGuardRegex.MatchString(cmd) {
			if isCoreutilsTimeout(cmd) {
				cleaned := coreutilsTimeoutRegex.ReplaceAllString(cmd, " ")
				return sleepGuardRegex.MatchString(cleaned)
			}
			return true
		}
		return false
	}

	if !sleepGuardRegex.MatchString(cmd) {
		return false
	}

	if isCoreutilsTimeout(cmd) {
		cleaned := coreutilsTimeoutRegex.ReplaceAllString(cmd, " ")
		return sleepGuardRegex.MatchString(cleaned)
	}

	return true
}

var pureSleepRegex = regexp.MustCompile(`(?i)^\s*(?:sleep|timeout(?:\.exe)?(?:\s+\/t)?|start-sleep(?:\s+(?:-s|-seconds))?)\s+(\d+)\s*$`)
var pythonSleepRegex = regexp.MustCompile(`(?i)^\s*(?:python|python3|py)(?:\.exe)?\s+-c\s+["'].*time\.sleep\((\d+(?:\.\d+)?)\).*["']\s*$`)

var sleepFn = time.Sleep

// tryRemediateSleep intercepta comandos exclusivos de sleep / timeout (incluindo python time.sleep).
// Se a pausa pedida for <= 15s, executa a espera controlada em Go e retorna aviso pedagógico.
// Se > 15s, bloqueia com instrução explícita de usar job assíncrono.
func tryRemediateSleep(label string, args ExecArgs) (string, bool, error) {
	var targetStr string
	if len(args.Argv) > 0 {
		exe := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		if exe == "sleep" || exe == "timeout" || exe == "python" || exe == "python3" || exe == "py" {
			targetStr = strings.Join(args.Argv, " ")
		}
	} else {
		targetStr = strings.TrimSpace(label)
	}

	if targetStr == "" {
		return "", false, nil
	}

	var sec int
	matches := pureSleepRegex.FindStringSubmatch(targetStr)
	if len(matches) >= 2 {
		_, _ = fmt.Sscanf(matches[1], "%d", &sec)
	} else {
		pyMatches := pythonSleepRegex.FindStringSubmatch(targetStr)
		if len(pyMatches) >= 2 {
			var f float64
			if _, err := fmt.Sscanf(pyMatches[1], "%f", &f); err == nil {
				sec = int(f + 0.5)
			}
		} else {
			return "", false, nil
		}
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

var winDirFlagRegex = regexp.MustCompile(`(?i)^/[a-z0-9:-]{1,4}$`)

func joinHostPath(host, dir, sub string) string {
	if host != "" && host != "local" {
		dir = strings.TrimRight(dir, "/")
		sub = strings.TrimLeft(sub, "/")
		return dir + "/" + sub
	}
	return filepath.Join(dir, sub)
}

// detectListCommand identifica comandos puros de listagem de diretório (ls, dir, Get-ChildItem, gci)
// sem pipes, redirecionamentos ou múltiplos argumentos complexos.
func detectListCommand(args ExecArgs) (cmdName string, candidatePath string) {
	var fields []string

	if args.Command != "" {
		cmd := strings.TrimSpace(args.Command)
		if strings.ContainsAny(cmd, "|><;&`$\n") {
			return "", ""
		}
		fields = strings.Fields(cmd)
	} else if len(args.Argv) > 0 {
		fields = args.Argv
	}

	if len(fields) == 0 {
		return "", ""
	}

	exe := strings.ToLower(filepath.Base(fields[0]))
	exe = strings.TrimSuffix(exe, ".exe")

	switch exe {
	case "ls", "dir":
		cmdName = exe
	default:
		return "", ""
	}

	// Analisa os argumentos restantes para extrair o caminho (ou "." se omitido)
	var pathArg string
	for i := 1; i < len(fields); i++ {
		arg := fields[i]
		lowerArg := strings.ToLower(arg)

		// Parâmetros com nome do PowerShell: -Path <valor> ou -LiteralPath <valor>
		if (lowerArg == "-path" || lowerArg == "-literalpath") && i+1 < len(fields) {
			pathArg = fields[i+1]
			i++
			continue
		}

		// Flags tipo Unix/PowerShell (-l, -la, -r, --color, etc.)
		if strings.HasPrefix(arg, "-") {
			continue
		}

		// Flags clássicas do cmd.exe (/b, /s, /a, /w, /p, /q, /o, etc.) apenas quando o comando for 'dir'
		if cmdName == "dir" && winDirFlagRegex.MatchString(arg) {
			continue
		}

		// Se já capturamos um caminho e encontramos outro argumento não-flag,
		// pode ser listagem de múltiplos diretórios (ex: ls dir1 dir2) - delegamos para o shell.
		if pathArg != "" {
			return "", ""
		}

		pathArg = arg
	}

	// Limpa aspas envolventes se houver
	pathArg = strings.Trim(pathArg, `"'`)

	// Se contém curingas (* ou ?), delega para o shell expandir o glob
	if strings.ContainsAny(pathArg, "*?") {
		return "", ""
	}

	if pathArg == "" {
		pathArg = "."
	}

	return cmdName, pathArg
}

// tryRemediateList intercepta listagens de diretório simples e executa via file_ops (action='list'),
// economizando criação de processos e normalizando o retorno.
func tryRemediateList(args ExecArgs) (string, bool, error) {
	_, candidatePath := detectListCommand(args)
	if candidatePath == "" {
		return "", false, nil
	}

	filePath := candidatePath
	if filePath == "" || filePath == "." {
		if args.Cwd != "" {
			filePath = args.Cwd
		} else {
			filePath = "."
		}
	} else if args.Cwd != "" && !filepath.IsAbs(filePath) && !strings.HasPrefix(filePath, "/") && !strings.HasPrefix(filePath, "\\") && (len(filePath) < 2 || filePath[1] != ':') {
		filePath = joinHostPath(args.Host, args.Cwd, filePath)
	}

	rawJSON, err := HandleFileOps(FileOpsArgs{
		Host:   args.Host,
		Action: "list",
		Path:   filePath,
	})
	if err != nil {
		// Se não for um diretório ou falhar na listagem nativa, delega suavemente para o shell
		return "", false, nil
	}

	type remediatedListItem struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"is_dir"`
	}

	var items []remediatedListItem
	var formatted string
	if err := json.Unmarshal([]byte(rawJSON), &items); err == nil {
		if len(items) == 0 {
			formatted = "(diretório vazio)"
		} else {
			var sb strings.Builder
			for _, it := range items {
				if it.IsDir {
					sb.WriteString(fmt.Sprintf("%-6s %10s  %s/\n", "DIR", "-", it.Name))
				} else {
					sb.WriteString(fmt.Sprintf("%-6s %10d  %s\n", "FILE", it.Size, it.Name))
				}
			}
			formatted = strings.TrimRight(sb.String(), "\n")
		}
	} else {
		formatted = rawJSON
	}

	cmdLabel := args.Command
	if cmdLabel == "" {
		cmdLabel = strings.Join(args.Argv, " ")
	}

	out := fmt.Sprintf("[FastExec Auto-Remediation] O comando '%s' foi convertido automaticamente para a tool nativa 'file_ops' (action='list', zero-process).\nNa próxima, invoque 'file_ops' diretamente com action='list' para economizar tempo e tokens.\n---\n%s", cmdLabel, formatted)
	return out, true, nil
}

var (
	redundantCdWorkspaceRegex = regexp.MustCompile(`(?i)^\s*cd\s+["']?(?:~|\/home\/[^\/\s"']+)\/workspace["']?\s*(?:&&|;)\s*`)
	redundantCdSubdirRegex    = regexp.MustCompile(`(?i)^\s*cd\s+["']?(?:~|\/home\/[^\/\s"']+)\/workspace\/([^"';&]+)["']?\s*(?:&&|;)\s*`)
	pureCdWorkspaceRegex      = regexp.MustCompile(`(?i)^\s*cd\s+["']?(?:~|\/home\/[^\/\s"']+)\/workspace\/?["']?\s*$`)
)

// tryRemediateCd detecta e remove o prefixo redundante 'cd ~/workspace &&' quando o comando roda no noteserver,
// onde o diretório de trabalho padrão já é /home/elvis/workspace.
func tryRemediateCd(args *ExecArgs) (out string, handled bool, hint string) {
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return "", false, ""
	}

	host := strings.ToLower(strings.TrimSpace(args.Host))
	if host != "" && host != "noteserver" && host != "servidor" && host != "192.168.31.142" && host != "192.168.31.142:9142" {
		return "", false, ""
	}

	// 1. Comando puro 'cd ~/workspace'
	if pureCdWorkspaceRegex.MatchString(cmd) {
		return "[FastExec] O diretório de trabalho já é ~/workspace (cwd padrão no noteserver). Nenhum comando necessário.", true, ""
	}

	// 2. cd ~/workspace/<subpasta> && <resto>
	if matches := redundantCdSubdirRegex.FindStringSubmatch(cmd); len(matches) >= 2 {
		subDir := strings.TrimSpace(matches[1])
		rest := strings.TrimSpace(cmd[len(matches[0]):])
		if rest != "" {
			if args.Cwd == "" {
				args.Cwd = subDir
				args.Command = rest
			} else {
				args.Command = fmt.Sprintf("cd %s && %s", subDir, rest)
			}
			hint = fmt.Sprintf("[FastExec Auto-Remediation] O prefixo 'cd ~/workspace/%s' foi simplificado. No noteserver o cwd padrão já é ~/workspace; prefira usar o parâmetro cwd: '%s'.", subDir, subDir)
			return "", false, hint
		}
	}

	// 3. cd ~/workspace && <resto> ou cd ~/workspace; <resto>
	if loc := redundantCdWorkspaceRegex.FindStringIndex(cmd); loc != nil {
		rest := strings.TrimSpace(cmd[loc[1]:])
		if rest != "" {
			args.Command = rest
			hint = "[FastExec Auto-Remediation] O prefixo 'cd ~/workspace' foi removido automaticamente: no noteserver o diretório de trabalho padrão já é ~/workspace. Passe seus comandos diretamente ou use o parâmetro 'cwd'."
			return "", false, hint
		}
	}

	return "", false, ""
}

func isSearchOrDiffCommand(cmd string) bool {
	lowerCmd := strings.ToLower(cmd)
	searchTokens := []string{
		"grep", "findstr", "select-string", "sls",
		"diff", "cmp", "comm", "rg", "ripgrep",
	}
	for _, tok := range searchTokens {
		if strings.Contains(lowerCmd, tok) {
			return true
		}
	}
	return false
}

func isBenignExitCode1(command, stdout, stderr string) bool {
	lowerCmd := strings.ToLower(command)
	// Diff / cmp: exit code 1 significa apenas que diferenças foram encontradas
	if (strings.Contains(lowerCmd, "diff") || strings.Contains(lowerCmd, "cmp")) && stdout != "" {
		return true
	}
	combined := strings.ToLower(stdout + " " + stderr)
	// Erro semântico retornado por query SQL (psql, sqlite3, duckdb)
	if strings.Contains(combined, "error:") || strings.Contains(combined, "syntax error") {
		if strings.Contains(lowerCmd, "psql") || strings.Contains(lowerCmd, "sqlite") || strings.Contains(lowerCmd, "duckdb") || strings.Contains(lowerCmd, "select") {
			return true
		}
	}
	// Busca encadeada no shell que produziu saída em stdout apesar de exit code 1 no último comando
	if (strings.Contains(lowerCmd, "findstr") || strings.Contains(lowerCmd, "grep") || strings.Contains(lowerCmd, "select-string") || strings.Contains(lowerCmd, "sls") || strings.Contains(lowerCmd, "rg") || strings.Contains(lowerCmd, "ripgrep")) && stdout != "" {
		return true
	}
	return false
}

// detectNudge sugere ferramentas ou módulos especializados do FastExec no rodapé da resposta quando
// o comando executado no shell tem uma alternativa superior nativa.
func detectNudge(label string, args ExecArgs) string {
	cmd := strings.TrimSpace(label)

	// SQL Nudge: mesmo com aspas, pipes ou filtros de linha, sugere a tool 'sql' nativa
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "sqlite3 ") || strings.Contains(lower, "duckdb ") || strings.Contains(lower, "psql ") ||
		strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "insert ") || strings.HasPrefix(lower, "update ") {
		return "[FastExec Hint] Para consultas SQL (SQLite/DuckDB), use a tool nativa 'sql' que devolve JSON estruturado e não infla o shell."
	}

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
	}
	return ""
}
