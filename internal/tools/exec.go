package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/transport"
)

// avoidedRetry anota uma chamada que o FastExec respondeu ou corrigiu sozinho: o agente não precisou
// errar, ler o erro e reenviar. Os bytes são um proxy: o comando reenviado e o texto devolvido.
func avoidedRetry(label, out string) {
	telemetry.NoteAvoidedDetail(telemetry.CatRetry, 2*len(label)+len(out), 0, retryNote(label, out))
}

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
	Caller    string   `json:"_caller,omitempty"`       // canal MCP (injetado pelo proxy)
	Conv      string   `json:"_conversation,omitempty"` // id de conversa do cliente, quando houver
}

const sleepGuardMsg = "[FastExec Guard] 'sleep' and 'timeout' are blocked: exec is event-driven and returns the moment the process exits. For long work raise 'timeout_ms' (e.g. 300000); for asynchronous work use the 'job' tool with action 'wait'"

// HandleExec resolve o host (host_autolocal.go: caminho Windows sem host explícito com padrão remoto vai para
// 'local', com aviso na saída) e delega a handleExec.
func HandleExec(args ExecArgs) (string, error) {
	if isUndoCommand(args.Command, args.Argv) {
		return lastEditUndo(), nil // desfazer em memória da última gravação (lastedit.go): não roda shell nenhum
	}
	parts := append([]string{args.Command, args.Cwd}, args.Argv...)
	host, note := localHostForWindowsPath(args.Host, parts...)
	args.Host = host
	// Caminho estilo Git Bash em host Windows (unix_shell.go) é traduzido antes de rodar, com aviso — menos na linha
	// que vai para o bash, onde o caminho Unix do comando já está certo (só o cwd vira unidade Windows).
	argCmd, argCwd, bashNote := remediateLocalWindowsPaths(args.Host, args.Command, args.Cwd)
	args.Command, args.Cwd = argCmd, argCwd
	note = joinNotes(note, bashNote)
	out, err := handleExec(args)
	return withHostNote(note, out, err), err
}

func handleExec(args ExecArgs) (string, error) {
	hasCmd, hasArgv := args.Command != "", len(args.Argv) > 0
	switch {
	case hasCmd && hasArgv:
		return "", fmt.Errorf("give only one: 'command' (shell line) or 'argv' (no shell)")
	case !hasCmd && !hasArgv:
		return "", fmt.Errorf("parameter 'command' (or 'argv') is required")
	}

	label := args.Command
	if hasArgv {
		label = strings.Join(args.Argv, " ")
	}

	// Remove payload de blocos heredoc da análise (evita falsos positivos com documentação/scripts)
	cmdToCheck := stripHeredocs(label)

	// Auto-remediação de sleep puro <= 15s com aviso pedagógico
	if out, handled, err := tryRemediateSleep(cmdToCheck, args); handled {
		avoidedRetry(label, out)
		return out, err
	}

	// Bloqueio determinístico de sleep / timeout artificial
	var sleepNotice string
	if checkSleepGuard(cmdToCheck, args) {
		notice, reason, ok := sleepAllowance(cmdToCheck)
		if !ok {
			return "", fmt.Errorf("%s", reason)
		}
		sleepNotice = notice
	}

	// Auto-remediação de prefixo redundante 'cd <cwd padrão do host> &&' ou ';'
	var cdHint string
	if hasCmd {
		if out, handled, hint := tryRemediateCd(&args); handled {
			avoidedRetry(label, out)
			return out, nil
		} else {
			cdHint = hint
			label = args.Command
		}
	}

	// Busca de identificador num repositório: respondida pelo grafo (repetir o comando roda a busca crua). O pedido
	// de busca crua ('--raw' ou o prefixo 'raw:') sai do comando antes de rodar: a dica do rodapé tem de funcionar
	// como está, sem o agente ter de inventar outra forma.
	var graphNote string
	rawEscape := false
	if newLabel, stripped := stripRawEscape(&args); stripped {
		label, rawEscape = newLabel, true
		if parseListingKind(label) != listingNone {
			graphNote = GraphMarkRaw + ": raw listing as asked (grouping by directory was skipped)."
		} else {
			graphNote = GraphMarkRaw + ": raw text search as asked (the code graph was skipped)."
		}
	}
	if out, handled, note := tryGraphSearch(args, label, rawEscape); handled {
		if cdHint != "" {
			out = cdHint + "\n---\n" + out
		}
		avoidedRetry(label, out)
		return out, nil
	} else if graphNote == "" {
		graphNote = note
	}

	// Saída de build/teste (go/dotnet/npm/tsc/cargo): o '--raw' e FASTEXEC_NO_OUTPUT_FILTER desligam a compactação
	// (build_compact.go). O marcador só sai de comando de família reconhecida — em qualquer outro ele é do programa.
	buildNote := ""
	buildRaw := outputFilterOff()
	if newLabel, stripped := stripBuildRawEscape(&args); stripped {
		label, buildRaw = newLabel, true
		buildNote = "[FastExec] raw output as asked: build/test output not compacted."
	}

	// Auto-remediação de cat / type puro (conversão para read_file com notificação pedagógica)
	if out, handled, err := tryRemediateCat(args); handled {
		if cdHint != "" && out != "" {
			out = cdHint + "\n---\n" + out
		}
		avoidedRetry(label, out)
		return out, err
	}

	// Leitura embrulhada (pwsh -Command / bash -c / várias leituras com cd e separadores): cada arquivo via read_file
	if out, handled := tryRemediateReadScript(args); handled {
		if cdHint != "" {
			out = cdHint + "\n---\n" + out
		}
		avoidedRetry(label, out)
		return out, nil
	}

	// Auto-remediação de listagem pura dir / ls / get-childitem (conversão para file_ops list com notificação pedagógica)
	if out, handled, err := tryRemediateList(args); handled {
		if cdHint != "" && out != "" {
			out = cdHint + "\n---\n" + out
		}
		avoidedRetry(label, out)
		return out, err
	}

	timeout, ceiling := execTimeouts(args.Caller, transport.DefaultTimeout)
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	clampedFrom := time.Duration(0)
	if timeout > ceiling {
		clampedFrom, timeout = timeout, ceiling
	}

	// Guard de sistema: varredura recursiva de raiz ampla (C:\, C:\Users, /, /home) não executa.
	if root := broadScan(args); root != "" {
		return "", broadScanError(root)
	}

	// Guard de busca: grep recursivo na raiz ganha exclusões de diretórios de build/dependência (label segue o original).
	excluded := applyGrepExcludes(&args)

	// Escolha do shell (unix_shell.go): linha Unix em host Windows com bash disponível vai inteira para 'bash -lc',
	// com aviso na saída. Acontece depois de todas as guardas, para o comando que chega ao shell ser exatamente o que
	// as guardas avaliaram, e é uma execução só — nada de rodar no cmd e reenviar depois. 'label' continua sendo a
	// linha original (dicas, dedup e telemetria falam do comando da LLM, não do argv montado aqui).
	shellNote := ""
	unixLbl := unixLabel(label, hasArgv, args.Argv)
	if hasCmd {
		// PowerShell antes do bash: '$env:X=1; & "C:\...\dotnet.exe" build' tem aspas suficientes para parecer
		// Unix e morria nos dois shells ("syntax error near unexpected token '&'"). A precisão é a mesma das duas
		// detecções: cada plano só age no seu sinal forte, e o de PowerShell cede quando há ferramenta Unix.
		argv, note, ok := powershellPlanFor(args.Host, args.Command)
		if !ok {
			argv, note, ok = bashPlanFor(args.Host, args.Command)
		}
		if ok {
			args.Argv, args.Command, hasArgv, shellNote = argv, "", true, note
			// No shell do plano o PATH é outro (o do Git, o do PowerShell): a nota de "ferramenta não instalada"
			// seria falsa.
			unixLbl = ""
		}
	}

	// D9: o que o exec gravar em arquivo .go/.cs é validado depois (exec_validate.go).
	watch := execWatchBegin(args.Host, label, args.Cwd)

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
	telemetry.NoteCommandMs(res.Duration) // overhead do FastExec = tempo da chamada - tempo do comando
	if res.JobID != "" {
		return withHostNote(shellNote, handoffResult(*res, timeout), nil), nil
	}

	if !res.TimedOut && isTextSearch(label) {
		noteGrep(identity(args.Caller, args.Conv), label, res.Stdout) // antes do corte: a conta que o rename refaz é a inteira
	}
	searchHint := ""
	if !res.TimedOut && !searchGuardOff() && isTextSearch(label) {
		if cut, total, truncated := truncateSearchOutput(res.Stdout); truncated {
			res.Stdout, searchHint = cut, searchTruncatedHint(total)
		}
	}

	listingHint := ""
	if !rawEscape && !outputFilterOff() && !res.TimedOut && res.ExitCode == 0 {
		// Listagem recursiva grande vira uma linha por diretório (listing_group.go). Nenhum arquivo é omitido: o
		// texto é o mesmo, reagrupado. O '--raw' (rawEscape) desliga.
		if grouped, note, ok := regroupListing(label, res.Stdout); ok {
			res.Stdout, listingHint = strings.TrimRight(grouped, "\r\n")+"\n", note
		}
	}

	// Compactação de saída de build/teste, ANTES de formatar: o que sobrevive é erro/aviso com arquivo:linha, o
	// resumo final e o nome do teste que falhou; uma linha por pacote que passou, progresso e banner saem. A
	// compactação só é aceita se nenhum bloco de erro do original tiver ficado de fora (compactBuildOutput confere).
	compactNote := ""
	if !buildRaw && !res.TimedOut {
		if body, note, ok := compactBuildOutput(label, res.ExitCode, res.Stdout, res.Stderr); ok {
			res.Stdout, res.Stderr, compactNote = strings.TrimRight(body, "\r\n")+"\n", "", note
		}
	}

	out, ferr := formatExecResult(label, *res)
	if searchHint != "" {
		out = strings.TrimRight(out, "\r\n") + "\n" + searchHint
	} else if listingHint != "" {
		out = strings.TrimRight(out, "\r\n") + "\n" + listingHint
	} else if compactNote != "" {
		out = strings.TrimRight(out, "\r\n") + "\n" + compactNote
	} else if excluded && out == NoResultText {
		out += "\n" + searchExcludedNote
	}
	if !searchGuardOff() {
		out = execFailureHints(out, label, unixLbl, res)
		// Busca por "Tipo.Membro": o hint genérico de rename vira a chamada pronta, com o arquivo da declaração.
		if res.ExitCode == 0 && !res.TimedOut && strings.Contains(out, memberSearchHint) {
			if plan := memberRenamePlan(args.Cwd, label); plan != "" {
				out = strings.Replace(out, memberSearchHint, plan, 1)
			}
		}
		// sed/perl que trocou "Tipo.Velho(" por texto: o script rodou como escrito (nada é refeito nem desfeito), e a
		// resposta aponta o rename exato do compilador — o script só mexeu em texto, sem checar nada.
		if strings.Contains(out, editScriptHint) {
			if plan := memberRenamePlan(args.Cwd, label); plan != "" {
				out = strings.Replace(out, editScriptHint, plan+" The script above only changed text, nothing was checked by the compiler; the declaration is not covered by it.", 1)
			}
		}
	}
	if res.ExitCode != 0 && !res.TimedOut {
		if hint := checkCompileOrTestFailure(args.Cwd, label, out); hint != "" {
			out = strings.TrimRight(out, "\r\n") + "\n\n" + hint
		}
	}
	if res.TimedOut && clampedFrom > 0 {
		out += fmt.Sprintf("\nThe requested timeout_ms (%d) was capped at %d ms, the ceiling before the MCP client cuts the call.", clampedFrom.Milliseconds(), timeout.Milliseconds())
	}
	if ferr == nil && res.ExitCode == 0 && !res.TimedOut {
		if res.Duration > execDurationWarningMs && !longTimeoutClient(args.Caller) {
			warning := fmt.Sprintf("[FastExec Warning] This command took %.1fs synchronously. Commands over ~30 s risk the MCP client's hard 60 s timeout; use the 'job' tool (action 'start') instead.", float64(res.Duration)/1000)
			if out != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + warning
			} else {
				out = warning
			}
		}
		if hint := checkResidentService(args.Cwd, label, args.Host); hint != "" {
			if out != "" {
				out = strings.TrimRight(out, "\r\n") + "\n\n" + hint
			} else {
				out = hint
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
	if sleepNotice != "" {
		out = strings.TrimRight(out, "\r\n") + "\n\n" + sleepNotice
	}
	if graphNote != "" {
		out = strings.TrimRight(out, "\r\n") + "\n\n" + graphNote
	}
	if buildNote != "" {
		out = strings.TrimRight(out, "\r\n")
		if out != "" {
			out += "\n"
		}
		out += buildNote
	}
	if ferr == nil && res.ExitCode == 0 && !res.TimedOut {
		// apagou arquivo de código? o índice (de antes do apagamento) diz quem ainda usa os símbolos dele
		if h := execDeleteImpact(args); h != "" {
			out = strings.TrimRight(out, "\r\n")
			if out != "" {
				out += "\n"
			}
			out += h
		}
		if !outputFilterOff() { // saída íntegra pedida: nada de trocar o texto por um stub de repetição
			out = dedupExecOutput(args, label, out)
		}
	}
	if !res.TimedOut {
		if v := watch.Finish(); v != "" {
			out = strings.TrimRight(out, "\r\n")
			if out != "" {
				out += "\n"
			}
			out += v
		}
	}
	// Rodapé do tempo (elapsed.go) por ÚLTIMO: depois do '[exit N]', das notas de remediação e do dedup — se ele
	// entrasse antes, o texto do tempo mudaria o hash a cada execução e o dedup de saídas idênticas nunca casaria.
	if ft := elapsedFooter(time.Duration(res.Duration)*time.Millisecond, res.TimedOut); ft != "" {
		out = strings.TrimRight(out, "\r\n")
		if out != "" {
			out += "\n"
		}
		out += ft
	}
	return withHostNote(shellNote, out, ferr), ferr
}

// unixLabel: rótulo em que a detecção de ferramenta Unix (unix_shell.go) se apoia. Num 'command' a linha inteira é
// interpretada pelo shell, então os estágios ('a | b') contam; num 'argv' nada passa por shell e só o executável
// diz que ferramenta é (sem isso, argv ['echo','grep x | wc -l'] acusaria um 'wc' que nunca vai rodar).
func unixLabel(label string, hasArgv bool, argv []string) string {
	if hasArgv && len(argv) > 0 {
		return argv[0]
	}
	return label
}

// execFailureHints põe uma única nota no fim da saída — a mais específica que couber — e devolve o texto já com ela.
// 'label' é a linha (ou o argv juntado) para as notas que falam do comando; 'unixLbl' é o rótulo da detecção de
// ferramenta Unix. A nota do comando Unix fica por último de propósito: as notas acima (edit_file, read_file com
// ranges) dão o caminho nativo direto para o mesmo erro, e só quando nenhuma cabe é que a nota genérica aparece.
// O texto de 'out' (o erro original) nunca é escondido: a nota é acrescentada no fim.
func execFailureHints(out, label, unixLbl string, res *transport.ExecResult) string {
	unixNote := unixShellNoteFor(unixLbl, res)
	add := func(note string) string { return strings.TrimRight(out, "\r\n") + "\n" + note }
	switch {
	case isEditScript(label):
		return add(editScriptHint)
	case isDeclSearch(label):
		return add(declSearchHint)
	case isMemberSearch(label):
		return add(memberSearchHint)
	case isCallSearch(label):
		return add(callSearchHint)
	case isLineSliceRead(label):
		return add(lineSliceHint)
	case isBroadSearch(label) && nonEmptyLines(res.Stdout) >= broadSearchMinLines:
		return add(broadSearchHint)
	case isPythonMissing(res.Stdout + res.Stderr):
		return add(pythonMissingHint)
	case isFileAggregate(label):
		return add(aggregateHint)
	case unixNote != "":
		return add(unixNote)
	}
	return out
}

func formatExecResult(command string, res transport.ExecResult) (string, error) {
	out, err := formatExecOutput(command, res)
	if res.Leftover > 0 {
		out += fmt.Sprintf("\n[warning] %d child process(es) outlived the command and were killed with it; for background or long-running work use tool 'job' (action 'start').", res.Leftover)
	}
	return out, err
}

func formatExecOutput(command string, res transport.ExecResult) (string, error) {
	needle := searchNeedle(command) // termo buscado: linha gigante cortada mostra a ocorrência
	stdout, stderr := res.Stdout, res.Stderr
	if !outputFilterOff() { // FASTEXEC_NO_OUTPUT_FILTER=1: saída íntegra, nenhuma limpeza
		stdout = cleanOutputFor(res.Stdout, needle)
		stderr = cleanOutputFor(res.Stderr, needle)
		if rawN, cleanN := len(res.Stdout)+len(res.Stderr), len(stdout)+len(stderr); rawN > cleanN {
			telemetry.NoteSavingDetail(telemetry.CatClean, rawN, cleanN, cleanNote(res.Stdout+res.Stderr, stdout+stderr))
		}
	}

	if res.TimedOut {
		return formatTimeout(res.Duration, stdout, stderr)
	}

	if res.ExitCode != 0 {
		if res.ExitCode == 1 && stdout == "" && stderr == "" {
			return NoResultText, nil
		}
		// Busca sem resultado é silêncio normal (grep sem casamento sai 1). Mas se o comando escreveu algo na stderr,
		// o erro é real ('grep' não existe no cmd, arquivo não encontrado): engolir a mensagem era o "no result (exit 1)"
		// sem pista nenhuma do benchmark. Só o silêncio de verdade vira NoResultText.
		if (res.ExitCode == 1 || res.ExitCode == 255) && stdout == "" && stderr == "" && isSearchOrDiffCommand(command) {
			return NoResultText, nil
		}
		errMsg := stderr
		if errMsg == "" {
			errMsg = stdout
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("command failed with exit code %d (no output)", res.ExitCode)
		}
		errMsg = shapeOrCap(errMsg)
		if res.Local && isWindowsCmdletMismatch(command, errMsg) {
			errMsg += "\n[FastExec Hint] Host 'local' (Windows) runs commands through cmd.exe. For PowerShell cmdlets use argv ['pwsh','-NoProfile','-Command','-'] with the script in 'input'; for files use read_file / edit_file."
		}
		out := fmt.Sprintf("[exit %d] %s", res.ExitCode, errMsg)
		// Saída e erro contam os dois: numa linha composta ('busca && interpretador') o primeiro estágio pode ter
		// funcionado e impresso o que a LLM pediu, e só o segundo falhou. Antes o stdout era descartado sempre que
		// havia stderr — a LLM não via o resultado da busca e repetia a chamada inteira só para lê-lo (turno pago a
		// mais, medido no bench). O '[exit N]' continua abrindo o texto (é dele que o erro do MCP se origina) e o
		// stdout vem depois, marcado e anunciado, para não ser lido como mensagem de erro.
		if stderr != "" && stdout != "" {
			out += "\n[stdout before the failure]\n" + shapeOrCap(stdout) +
				"\n[FastExec] The command failed, but the output above was produced before the failure: it is the command's stdout, not part of the error."
		}
		if isBenignExitCode1(command, stdout, stderr) {
			return out, nil
		}
		return out, fmt.Errorf("command failed with exit code %d", res.ExitCode)
	}

	if stdout == "" && stderr != "" {
		return shapeOrCap(stderr), nil
	}

	// Exit 0 com erro no stderr (ex.: `pwsh -Command -` segue após um throw e sai 0): antes o stderr era
	// descartado e a LLM via só o stdout, sem saber que algo falhou. Só anexa quando o stderr tem cara de erro,
	// para não trazer progresso/avisos comuns.
	if errPart := stderrErrors(stderr); errPart != "" {
		return shapeOrCap(strings.TrimRight(stdout, "\r\n") + "\n[stderr]\n" + errPart +
			"\n[FastExec] Exit code was 0, but the command wrote errors to stderr (above): check that it really did what you expected."), nil
	}

	// avisos do daemon remoto (ex.: processo em segundo plano que ficou rodando) sempre chegam à LLM
	if notes := daemonNotes(stderr); notes != "" {
		return shapeOrCap(strings.TrimRight(stdout, "\r\n") + "\n" + notes), nil
	}

	return shapeOrCap(stdout), nil
}

var stderrErrorRegex = regexp.MustCompile(`(?i)(exception|\berror\b|\bfatal\b|traceback|not recognized|cannot find|could not|failed|permission denied|no such file)`)

// daemonNotes devolve as linhas "[server-exec] ..." do stderr (avisos do daemon remoto).
func daemonNotes(stderr string) string {
	var keep []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "[server-exec]") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	return strings.Join(keep, "\n")
}

// stderrErrors devolve o stderr (até ~2 KB finais) quando ele contém sinais de erro; "" caso contrário.
func stderrErrors(stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" || !stderrErrorRegex.MatchString(s) {
		return ""
	}
	const max = 2048
	if len(s) > max {
		cut := len(s) - max
		if i := strings.IndexByte(s[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		s = "[…] " + s[cut:]
	}
	return s
}

// formatTimeout devolve ao agente um desfecho inequívoco: estourou o prazo, a árvore foi encerrada e o que foi emitido
// até o corte. Assim ele nunca precisa adivinhar se o comando rodou.
func formatTimeout(elapsedMs int64, stdout, stderr string) (string, error) {
	telemetry.NoteAvoidedDetail(telemetry.CatTimeout, len(stdout)+len(stderr), 0,
		fmt.Sprintf("Timed out after %.1f s; returned the %d B emitted so far instead of letting the client cut the call", float64(elapsedMs)/1000, len(stdout)+len(stderr)))
	head := fmt.Sprintf("[timeout after %.1fs; process tree killed]", float64(elapsedMs)/1000)
	partial := stdout
	if stderr != "" {
		if partial != "" {
			partial += "\n"
		}
		partial += "[stderr]\n" + stderr
	}
	msg := head
	if partial != "" {
		msg += "\nPartial output before the cut:\n" + capOutput(partial)
	}
	msg += "\n[FastExec Guidance] For long work (> 30 s) use the 'job' tool (action 'start')."
	return msg, fmt.Errorf("timeout after %.1fs", float64(elapsedMs)/1000)
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
var coreutilsTimeoutRegex = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)(?:(?:nice|ionice|env|sudo|stdbuf|time)(?:\s+-{1,2}[A-Za-z0-9_=-]+(?:\s+\d+)?)*\s+)*timeout(?:\s+-[A-Za-z0-9_-]+|\s+--[a-z0-9_-]+(?:=\S+)?)*\s+\d+(?:\.\d+)?[smhd]?\s+([a-zA-Z0-9_\.\/-]+)`)

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
		return "", true, fmt.Errorf("[FastExec Guard] A %ds pause is blocked in the synchronous exec (over 15 s). For background work start a job, or schedule a later check instead of holding the session.", sec)
	}

	sleepFn(time.Duration(sec) * time.Second)
	out := fmt.Sprintf("[FastExec Auto-Remediation] %ds pause done with a native Go timer.\n(Tip: FastExec is event-driven; to synchronize, wait for the target's port or file, or use 'job'.)", sec)
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
	// Linha que vai para o bash conserva o caminho POSIX (unix_shell.go); aqui, que é leitura nativa, ele vira unidade.
	candidatePath = windowsPathForNativeRead(candidatePath)

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
		return "", true, fmt.Errorf("[FastExec Auto-Remediation] '%s' looked like a file read, but read_file failed on '%s': %w", cmdName, candidatePath, err)
	}

	cmdLabel := args.Command
	if cmdLabel == "" {
		cmdLabel = strings.Join(args.Argv, " ")
	}

	out := fmt.Sprintf("[FastExec Auto-Remediation] '%s' was run as the native 'read_file' tool (no process).\nNext time call 'read_file' directly to save time and tokens.\n---\n%s", cmdLabel, content)
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
	return fmt.Errorf("[FastExec Guard] To read files use the native 'read_file' tool (path='%s'): it pages safely (offset/limit) and spawns no process.", candidatePath)
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

	// Listagem recursiva ('ls -R', 'dir /s') não é o que o file_ops 'list' devolve: ele lê um diretório só. Deixar
	// passar por aqui responderia a pasta de cima como se fosse a árvore inteira — falso negativo caro. O comando
	// segue para o shell e a saída é reagrupada por listing_group.go.
	if exe == "ls" && lsHasRecursive(fields[1:]) {
		return "", ""
	}
	if exe == "dir" && dirHasFlag(fields[1:], "s") {
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
	// Linha que vai para o bash conserva o caminho POSIX (unix_shell.go); aqui, que é listagem nativa, ele vira unidade.
	pathArg = windowsPathForNativeRead(pathArg)

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
			formatted = "(empty directory)"
		} else {
			// listagem grande não vai inteira ao modelo: as primeiras lsMaxEntries e um resumo do resto
			shown := items
			if len(shown) > lsMaxEntries {
				shown = shown[:lsMaxEntries]
			}
			var sb strings.Builder
			for _, it := range shown {
				if it.IsDir {
					sb.WriteString(fmt.Sprintf("%-6s %10s  %s/\n", "DIR", "-", it.Name))
				} else {
					sb.WriteString(fmt.Sprintf("%-6s %10d  %s\n", "FILE", it.Size, it.Name))
				}
			}
			if rest := items[len(shown):]; len(rest) > 0 {
				dirs := 0
				for _, it := range rest {
					if it.IsDir {
						dirs++
					}
				}
				fmt.Fprintf(&sb, "[truncated: %d more entries (%d folders, %d files); list a subfolder or filter with a pattern]\n", len(rest), dirs, len(rest)-dirs)
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

	out := fmt.Sprintf("[FastExec Auto-Remediation] '%s' was listed natively (no process, compact output).\n---\n%s", cmdLabel, formatted)
	return out, true, nil
}

// handoffResult: o comando estourou o timeout e seguiu como job de fundo (nada foi morto). Saída parcial limpa e
// cortada como qualquer saída, e o que fazer a seguir.
func handoffResult(res transport.ExecResult, timeout time.Duration) string {
	var b strings.Builder
	if s := strings.TrimRight(cleanOutputFor(res.Stdout, ""), "\r\n"); s != "" {
		b.WriteString(s + "\n")
	}
	if s := strings.TrimRight(cleanOutputFor(res.Stderr, ""), "\r\n"); s != "" {
		b.WriteString("[stderr]\n" + s + "\n")
	}
	fmt.Fprintf(&b, "[FastExec] Still running after %s, so it was moved to background job '%s' (nothing was killed; output above is partial). "+
		"Next: job(host='local', action='wait', job_id='%s'); 'tail' shows the log, 'stop' cancels. "+
		"Next time start commands this long with job(action='start').",
		human(timeout), res.JobID, res.JobID)
	return b.String()
}

// lsMaxEntries: teto de linhas da listagem nativa que substitui ls/dir (um dir de pasta grande chegou a 165 KB).
const lsMaxEntries = 200

// cdRegexes: os três padrões de 'cd <cwd padrão do host>' redundante (puro, com subpasta, com resto), um conjunto
// por cwd, montado uma vez. /home/<usuário>/x também casa como ~/x.
type cdRegexes struct{ pure, subdir, prefix *regexp.Regexp }

var cdRegexCache sync.Map // cwd -> *cdRegexes

func cdRegexesFor(cwd string) *cdRegexes {
	if v, ok := cdRegexCache.Load(cwd); ok {
		return v.(*cdRegexes)
	}
	var dir string
	switch {
	case strings.HasPrefix(cwd, "/home/") && strings.Count(cwd, "/") >= 3:
		rest := cwd[len("/home/"):]
		dir = `(?:~|/home/[^/\s"']+)` + regexp.QuoteMeta(rest[strings.Index(rest, "/"):])
	case strings.HasPrefix(cwd, "/root/"):
		dir = `(?:~|/root)` + regexp.QuoteMeta(cwd[len("/root"):])
	default:
		dir = regexp.QuoteMeta(strings.TrimSuffix(cwd, "/"))
	}
	r := &cdRegexes{
		pure:   regexp.MustCompile(`(?i)^\s*cd\s+["']?` + dir + `/?["']?\s*$`),
		subdir: regexp.MustCompile(`(?i)^\s*cd\s+["']?` + dir + `/([^"';&]+)["']?\s*(?:&&|;)\s*`),
		prefix: regexp.MustCompile(`(?i)^\s*cd\s+["']?` + dir + `["']?\s*(?:&&|;)\s*`),
	}
	cdRegexCache.Store(cwd, r)
	return r
}

// tryRemediateCd remove o prefixo redundante 'cd <cwd padrão> &&' quando o host tem cwd padrão na config.
func tryRemediateCd(args *ExecArgs) (out string, handled bool, hint string) {
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return "", false, ""
	}

	target, err := transport.ResolveHost(args.Host)
	if err != nil || target.IsLocal || target.DefaultCwd == "" {
		return "", false, ""
	}
	re := cdRegexesFor(target.DefaultCwd)
	shown := config.DisplayCwd(target.DefaultCwd)

	// 1. Comando puro 'cd <cwd>'
	if re.pure.MatchString(cmd) {
		return fmt.Sprintf("[FastExec] The working directory is already %s (%s default cwd); nothing to run.", shown, target.Name), true, ""
	}

	// 2. cd <cwd>/<subpasta> && <resto>
	if matches := re.subdir.FindStringSubmatch(cmd); len(matches) >= 2 {
		subDir := strings.TrimSpace(matches[1])
		rest := strings.TrimSpace(cmd[len(matches[0]):])
		if rest != "" {
			if args.Cwd == "" {
				args.Cwd = subDir
				args.Command = rest
			} else {
				args.Command = fmt.Sprintf("cd %s && %s", subDir, rest)
			}
			hint = fmt.Sprintf("[FastExec Auto-Remediation] The 'cd %s/%s' prefix was simplified: on %s the default cwd is already %s; prefer the cwd parameter: '%s'.", shown, subDir, target.Name, shown, subDir)
			return "", false, hint
		}
	}

	// 3. cd <cwd> && <resto> ou cd <cwd>; <resto>
	if loc := re.prefix.FindStringIndex(cmd); loc != nil {
		rest := strings.TrimSpace(cmd[loc[1]:])
		if rest != "" {
			args.Command = rest
			hint = fmt.Sprintf("[FastExec Auto-Remediation] The 'cd %s' prefix was removed: on %s the default working directory is already %s. Pass commands directly or use the 'cwd' parameter.", shown, target.Name, shown)
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
		return "[FastExec Hint] For SQL (SQLite/DuckDB) use the 'sql' tool (module 'sql'): it returns structured JSON."
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
		return ""
	}
	return ""
}
