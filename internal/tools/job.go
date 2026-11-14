package tools

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

type JobArgs struct {
	Host    string   `json:"host"`
	Action  string   `json:"action"` // start, status, list, tail, stop, wait
	Command string   `json:"command,omitempty"`
	Argv    []string `json:"argv,omitempty"` // alternativa sem shell ao command (um dos dois)
	Cwd     string   `json:"cwd,omitempty"`
	JobID   string   `json:"job_id,omitempty"`
	Lines   int      `json:"lines,omitempty"`
	Caller  string   `json:"_caller,omitempty"` // canal MCP (injetado pelo proxy)
	Await   bool     `json:"await,omitempty"`   // start: espera até startAwaitMax o fim do job e devolve a saída (comando finito)
}

// startAwaitMax: quanto o 'start' com await espera o job terminar (abaixo do teto de 45 s do wait).
var startAwaitMax = 40 * time.Second

// jobWaitMax mantém o `wait` (local e Linux) abaixo do teto de 60 s do Claude Desktop: passou disso, responde que o job
// segue ativo em vez de deixar o transporte JSON-RPC cair. Variável só para os testes encurtarem.
var jobWaitMax = 45 * time.Second // + 5 s de folga do transporte remoto = 50 s, abaixo dos 55 s do proxy e dos 60 s do cliente

// jobProbeLogLines: linhas do log que a sondagem do start traz (o trecho mostrado é cortado depois, em quickExitLines
// para a falha rápida; o start que terminou no orçamento mostra mais saída).
const jobProbeLogLines = 30

// waitTimeoutMarker é impresso pelo laço remoto quando o prazo do wait acaba com o job ainda ativo.
const waitTimeoutMarker = "__FASTEXEC_WAIT_TIMEOUT__"

// Marcadores da sondagem de largada do job Linux (job_quickexit.go): o mesmo dispatch diz se o unit sobreviveu, o rc e
// a vida dele, sem round-trip nem turno extra.
const (
	jobRunningMarker = "__FASTEXEC_JOB_RUNNING__"
	jobExitedMarker  = "__FASTEXEC_JOB_EXITED__"
	jobLogMarker     = "__FASTEXEC_JOB_LOG__"
)

// linuxJobProbe: fragmento de shell que espera (só no start), pergunta ao systemd se o unit ainda roda e, quando ele já
// parou, imprime rc, vida (timestamps monotônicos) e o começo do log. Nada é impresso quando o job continua.
func linuxJobProbe(unit string, wait, withLog bool) string {
	p := ""
	if wait {
		// espera até jobStartBudget ou até o unit terminar (o que vier primeiro)
		p = fmt.Sprintf("end=$(( $(date +%%s) + %d )); while systemctl --user is-active --quiet '%s.service'; do if [ $(date +%%s) -ge $end ]; then break; fi; sleep 0.5; done; ",
			int(jobStartBudget/time.Second), unit)
	}
	p += fmt.Sprintf("if systemctl --user is-active --quiet '%s.service'; then echo %s; else echo %s; systemctl --user show -p ExecMainStatus -p ActiveEnterTimestampMonotonic -p InactiveEnterTimestampMonotonic '%s.service';",
		unit, jobRunningMarker, jobExitedMarker, unit)
	if withLog {
		p += fmt.Sprintf(" echo %s; journalctl --user-unit='%s.service' -n %d --no-pager;", jobLogMarker, unit, jobProbeLogLines)
	}
	return p + " fi"
}

// splitLinuxProbe separa o texto útil (systemctl status, log) do bloco da sondagem e lê rc, vida e estado do unit.
func splitLinuxProbe(out string) (before, state, log string, rc *int, life time.Duration, hasLife bool) {
	i := strings.Index(out, jobExitedMarker)
	if j := strings.Index(out, jobRunningMarker); i < 0 || (j >= 0 && j < i) {
		i = j
	}
	if i < 0 {
		return out, "", "", nil, 0, false
	}
	before, rest := out[:i], out[i:]
	var start, stop int64
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == jobRunningMarker:
			state = jobStateRunning
		case line == jobExitedMarker:
			state = "EXITED"
		case strings.HasPrefix(line, "ExecMainStatus="):
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "ExecMainStatus=")); err == nil {
				rc = &v
			}
		case strings.HasPrefix(line, "ActiveEnterTimestampMonotonic="):
			start, _ = strconv.ParseInt(strings.TrimPrefix(line, "ActiveEnterTimestampMonotonic="), 10, 64)
		case strings.HasPrefix(line, "InactiveEnterTimestampMonotonic="):
			stop, _ = strconv.ParseInt(strings.TrimPrefix(line, "InactiveEnterTimestampMonotonic="), 10, 64)
		}
	}
	if stop > start {
		life, hasLife = time.Duration(stop-start)*time.Microsecond, true
	}
	if k := strings.Index(rest, jobLogMarker); k >= 0 {
		log = strings.TrimSpace(rest[k+len(jobLogMarker):])
	}
	return before, state, log, rc, life, hasLife
}

// linuxExitHead: cabeçalho "[job] EXITED rc=N after X s" (a vida pode não ser conhecida: unit que nunca ficou ativo).
func linuxExitHead(rc *int, life time.Duration, hasLife bool) string {
	head := fmt.Sprintf("[job] EXITED rc=%s", rcText(rc))
	if hasLife {
		head += " after " + human(life)
	}
	return head
}

// linuxExitRan: cabeçalho de um job Linux que terminou DEPOIS da largada. Fora da janela de falha rápida a resposta era
// só o texto cru do systemctl/journalctl: o rc aparecia no meio do texto e a vida, em lugar nenhum — quem quisesse
// saber quanto o job durou tinha de ler o log ou repetir o comando.
func linuxExitRan(rc *int, life time.Duration, hasLife bool) string {
	head := fmt.Sprintf("[job] EXITED rc=%s", rcText(rc))
	if hasLife {
		head += " | " + ranText(life)
	}
	return head + "\n"
}

// linuxExitTail: rc, vida e log de um job Linux que morreu na largada.
func linuxExitTail(rc *int, life time.Duration, hasLife bool, label, log string) string {
	var b strings.Builder
	if hasLife {
		fmt.Fprintf(&b, "rc=%s | ran %s | log %d B\n", rcText(rc), human(life), len(log))
	} else {
		fmt.Fprintf(&b, "rc=%s | log %d B\n", rcText(rc), len(log))
	}
	if ex := logExcerpt(log, quickExitLines, quickExitChars); ex != "" {
		b.WriteString(ex + "\n")
	}
	if h := jobMissingToolHint(label, rc, log); h != "" {
		b.WriteString(h + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func sanitizeUnit(id string) string {
	s := strings.TrimSpace(id)
	if !strings.HasPrefix(s, "job-") {
		s = "job-" + s
	}
	s = strings.TrimSuffix(s, ".service")
	return s
}

// HandleJob resolve o host (host_autolocal.go: caminho Windows sem host explícito com padrão remoto vai para
// 'local', com aviso na saída) e delega a handleJob.
func HandleJob(args JobArgs) (string, error) {
	parts := append([]string{args.Command, args.Cwd}, args.Argv...)
	host, note := localHostForWindowsPath(args.Host, parts...)
	args.Host = host
	// Caminho estilo Git Bash em host Windows (unix_shell.go) é traduzido antes de rodar, com aviso.
	jobCmd, jobCwd, bashNote := remediateGitBashPaths(args.Host, args.Command, args.Cwd)
	args.Command, args.Cwd = jobCmd, jobCwd
	note = joinNotes(note, bashNote)
	out, err := handleJob(args)
	return withHostNote(note, out, err), err
}

func handleJob(args JobArgs) (string, error) {
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		action = "list"
	}

	targetHost := args.Host // vazio = host padrão da config

	target, err := transport.ResolveHost(targetHost)
	if err == nil && target.IsLocal && runtime.GOOS == "windows" {
		return handleLocalJob(action, args) // Job Object nomeado
	}

	// Linux, remoto ou local (systemd-run --user): argv vira linha de comando com aspas simples POSIX
	if len(args.Argv) > 0 && args.Command == "" {
		args.Command = transport.QuoteArgvPOSIX(args.Argv)
	}
	if args.Cwd == "" && err == nil && target.DefaultCwd != "" {
		args.Cwd = target.DefaultCwd
	}
	if args.Cwd != "" && args.Command != "" {
		args.Command = "cd \"" + args.Cwd + "\" && " + args.Command
	}

	// systemctl --user precisa do XDG_RUNTIME_DIR, que um daemon iniciado pelo systemd do sistema não tem
	envPrefix := "export XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}; "

	switch action {
	case "start":
		if args.Command == "" {
			return "", fmt.Errorf("action 'start' needs 'command'")
		}
		jobID := args.JobID
		if jobID == "" {
			jobID = fmt.Sprintf("%s_%d", time.Now().Format("20060102_150405"), rand.Intn(9000)+1000)
		}
		unit := sanitizeUnit(jobID)

		b64Cmd := base64.StdEncoding.EncodeToString([]byte(args.Command))
		cmd := fmt.Sprintf("%s systemd-run --user --unit='%s' --description='FastExec Job %s' /bin/bash -c \"$(echo '%s' | base64 -d)\" && %s",
			envPrefix, unit, unit, b64Cmd, linuxJobProbe(unit, true, true))

		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return fmt.Sprintf("[Job Start Error: exit %d] %s", res.ExitCode, res.Stderr), fmt.Errorf("could not start job: %s", res.Stderr)
		}

		if _, state, log, rc, life, hasLife := splitLinuxProbe(res.Stdout); state == "EXITED" {
			// terminou dentro do orçamento do start: rc e saída no mesmo turno, sem "started" + wait/tail
			if !hasLife || life <= quickExitLife {
				return fmt.Sprintf("%s\n%s\nUnit:    %s.service\nCommand: %s\n%s\nNext: job(action='tail', job_id='%s') for the whole log.",
					startAutoNote(), linuxExitHead(rc, life, hasLife), unit, args.Command, linuxExitTail(rc, life, hasLife, args.Command, log), unit), nil
			}
			return fmt.Sprintf("%s\n%sUnit:    %s.service\nCommand: %s\n%s", startAutoNote(), linuxExitRan(rc, life, hasLife),
				unit, args.Command, capOutput(cleanOutput(log))), nil
		}

		return fmt.Sprintf("[job] started\nUnit:    %s.service\nCommand: %s\nStatus:  job(action='status', job_id='%s')\nWait:    job(action='wait', job_id='%s')\nTail:    job(action='tail', job_id='%s')",
			unit, args.Command, unit, unit, unit), nil

	case "wait":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("action 'wait' needs 'job_id'")
		}
		// Aguarda o processo terminar no SO e devolve o log final no instante do término. O laço é limitado no próprio host
		// remoto (jobWaitMax): se o job ainda roda no prazo, imprime o marcador e segue para o log, em vez de o transporte
		// estourar o limite do cliente MCP.
		cmd := fmt.Sprintf("%s end=$(( $(date +%%s) + %d )); while systemctl --user is-active --quiet '%s.service'; do if [ $(date +%%s) -ge $end ]; then echo %s; break; fi; sleep 0.5; done; journalctl --user-unit='%s.service' -n 50 --no-pager",
			envPrefix, int(jobWaitMax/time.Second), unit, waitTimeoutMarker, unit)
		res, err := transport.Dispatch(targetHost, cmd, jobWaitMax+5*time.Second)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		running := strings.Contains(out, waitTimeoutMarker)
		out = strings.TrimSpace(strings.ReplaceAll(out, waitTimeoutMarker, ""))
		before, state, _, rc, life, hasLife := splitLinuxProbe(out)
		stateText := ""
		if !running && state == "EXITED" {
			stateText = linuxExitHead(rc, life, hasLife)
			stateText = strings.TrimPrefix(stateText, "[job] ") // o waitReport já monta o cabeçalho
		}
		rep := waitReport(unit, args.Host+"|"+unit, before, !running, stateText)
		if h := jobLogHint(args.Command, rc, before); h != "" {
			rep += "\n" + h
		}
		return rep, nil

	case "status":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("action 'status' needs 'job_id'")
		}
		cmd := fmt.Sprintf("%s systemctl --user status '%s.service' --no-pager; %s", envPrefix, unit, linuxJobProbe(unit, false, true))
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		before, state, log, rc, life, hasLife := splitLinuxProbe(out)
		if state == "EXITED" && (!hasLife || life <= quickExitLife) {
			// terminou na largada: rc e log junto do status, sem precisar de 'tail'
			return strings.TrimRight(before, "\n") + "\n" + linuxExitTail(rc, life, hasLife, args.Command, log), nil
		}
		if state == "EXITED" {
			// terminou depois da largada: o rc e a vida vêm na frente, do texto cru do systemctl
			return linuxExitRan(rc, life, hasLife) + before, nil
		}
		return before, nil

	case "list", "ls":
		cmd := fmt.Sprintf("%s systemctl --user list-units 'job-*.service' --all --no-pager", envPrefix)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := strings.TrimSpace(res.Stdout)
		if out == "" || strings.Contains(out, "0 loaded units listed") {
			return "No running or recently finished job.", nil
		}
		return out, nil

	case "tail":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("action 'tail' needs 'job_id'")
		}
		lines := args.Lines
		if lines <= 0 {
			lines = 30
		}
		cmd := fmt.Sprintf("%s journalctl --user-unit='%s.service' -n %d --no-pager; %s", envPrefix, unit, lines, linuxJobProbe(unit, false, false))
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		before, state, _, rc, life, hasLife := splitLinuxProbe(out)
		if state == "EXITED" && (!hasLife || life <= quickExitLife) {
			// morreu na largada: o tail já diz o rc e, se for ferramenta ausente, o que fazer
			head := fmt.Sprintf("%s | unit=%s.service\n", linuxExitHead(rc, life, hasLife), unit)
			rep := head + before
			if h := jobLogHint(args.Command, rc, before); h != "" {
				rep += "\n" + h
			}
			return rep, nil
		}
		if state == "EXITED" {
			// terminou depois da largada: mesmo cabeçalho do status, com a vida que o log não diz
			return linuxExitRan(rc, life, hasLife) + before, nil
		}
		return before, nil

	case "stop", "kill":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("action 'stop' needs 'job_id'")
		}
		cmd := fmt.Sprintf("%s systemctl --user stop '%s.service'", envPrefix, unit)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return fmt.Sprintf("could not stop job: %s", res.Stderr), fmt.Errorf("could not stop job: %s", res.Stderr)
		}
		return fmt.Sprintf("[job] %s.service stopped.", unit), nil

	default:
		return "", fmt.Errorf("unknown job action '%s' (valid: start, wait, status, list, tail, stop)", action)
	}
}
