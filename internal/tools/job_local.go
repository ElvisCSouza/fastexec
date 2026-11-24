package tools

import (
	"fmt"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

func jobLabel(m transport.LocalJobMeta) string {
	s := m.Command
	if m.Argv != nil {
		s = strings.Join(m.Argv, " ")
	}
	return clipLine(s, 70)
}

func human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// jobAgeText: "up 4s" enquanto o job roda, "ran 50.3s" depois que ele terminou. O "up" vinha sempre do relógio da
// criação, então um job que morreu em 3 s aparecia como "up 12m" meia hora depois — e nenhuma linha do status/list
// dizia quanto ele tinha durado (o modelo ia procurar isso no log ou reexecutava o comando para medir).
func jobAgeText(j *transport.LocalJobInfo) string {
	if j.State == jobStateRunning {
		return "up " + human(time.Since(j.Meta.CreatedAt))
	}
	life, _ := jobLife(j)
	return ranText(life)
}

// ranSuffix: " | ran 50.3s" quando a vida do job é conhecida, para os cabeçalhos curtos que antes traziam só o rc.
func ranSuffix(j *transport.LocalJobInfo) string {
	if life, finished := jobLife(j); finished {
		return " | " + ranText(life)
	}
	return ""
}

func statusLine(j *transport.LocalJobInfo) string {
	return fmt.Sprintf("%s | id=%s | processes=%d | %s | last log %s ago | log %d B | %s",
		j.State, j.Meta.ID, j.Active, jobAgeText(j), human(time.Since(j.LastActivity)), j.LogSize, jobLabel(j.Meta))
}

// handleLocalJob atende a tool `job` no host local (Windows): Job Object nomeado + log em arquivo (transport/localjob_windows.go).
func handleLocalJob(action string, args JobArgs) (string, error) {
	switch action {
	case "start":
		hasCmd, hasArgv := args.Command != "", len(args.Argv) > 0
		if hasCmd == hasArgv {
			return "", fmt.Errorf("action 'start' needs 'command' (shell line) or 'argv' (no shell), only one")
		}
		var argv []string
		if hasArgv {
			argv = args.Argv
		}
		m, replayed, err := transport.StartLocalJob(args.Command, argv, args.Cwd, args.JobID)
		if err != nil {
			return "", err
		}
		if replayed {
			// mesmo job_id e mesmo pedido: devolve o job existente, sem processo novo (idempotência por call id)
			st, serr := transport.LocalJobStatus(m.ID)
			if serr != nil {
				return "", serr
			}
			return fmt.Sprintf("[job %s replayed: same job_id and request; no new process started]\n%s", m.ID, statusLine(st)), nil
		}
		if args.Await {
			// Comando finito (build, teste, script): o mesmo turno devolve a saída se terminar no prazo, poupando start+wait.
			wait := awaitMax(args.Caller)
			j, finished, werr := transport.LocalJobWait(m.ID, wait)
			if werr == nil {
				tail, _ := transport.TailLocalJob(m.ID, 30)
				tail = capOutput(cleanOutput(tail))
				if finished {
					return waitReport(j.Meta.ID, "local|"+j.Meta.ID, tail, true, fmt.Sprint(j.State)), nil
				}
				return fmt.Sprintf("[job %s still running after %s; job(host='local', action='wait', job_id='%s') continues]\n%s", m.ID, human(wait), m.ID, tail), nil
			}
		}
		return localStartReport(m), nil

	case "status", "tail", "stop", "kill", "wait":
		if args.JobID == "" {
			return "", fmt.Errorf("action '%s' needs 'job_id'", action)
		}
	}

	switch action {
	case "status":
		j, err := transport.LocalJobStatus(args.JobID)
		if err != nil {
			return "", err
		}
		if isQuickExit(j) {
			return localJobExitReport(j), nil // rc + log na mesma resposta: sem 'tail' nem read_file
		}
		return statusLine(j), nil

	case "tail":
		lines := args.Lines
		if lines <= 0 {
			lines = 30
		}
		lines = min(lines, 500)
		if args.Cursor != "" {
			return cursorChunk(args.JobID, args.Cursor)
		}
		out, err := transport.TailLocalJob(args.JobID, lines)
		if err != nil {
			return "", err
		}
		out = cleanOutput(out)
		if j, jerr := transport.LocalJobStatus(args.JobID); jerr == nil && isQuickExit(j) {
			head := fmt.Sprintf("%s rc=%s | id=%s | log %d B\n", j.State, rcText(j.Meta.ExitCode)+ranSuffix(j), j.Meta.ID, j.LogSize)
			if out == "" {
				out = "(empty log)"
			}
			if h := jobLogHint(jobLabel(j.Meta), j.Meta.ExitCode, out); h != "" {
				out += "\n" + h
			}
			return capOutput(head + out), nil
		}
		if out == "" {
			return "(log vazio)", nil
		}
		return capOutput(out), nil

	case "stop", "kill":
		c, err := transport.LocalJobStop(args.JobID)
		if err != nil {
			return "", err
		}
		// veredito: accepted | confirmed | denied, com o estado atual do job
		return fmt.Sprintf("[job %s cancel %s: %s | state %s]", args.JobID, c.Verdict, c.Reason, c.Info.State), nil

	case "wait":
		if args.Cursor != "" {
			return cursorChunk(args.JobID, args.Cursor) // incremental: sem esperar, pelo cursor de bytes
		}
		j, finished, err := transport.LocalJobWait(args.JobID, jobWaitMax)
		if err != nil {
			return "", err
		}
		tail, _ := transport.TailLocalJob(args.JobID, 20)
		tail = capOutput(cleanOutput(tail))
		rep := waitReport(j.Meta.ID, "local|"+j.Meta.ID, tail, finished, localStateText(j))
		if h := jobLogHint(jobLabel(j.Meta), j.Meta.ExitCode, tail); h != "" {
			rep += "\n" + h // morreu porque a ferramenta não existe: o wait já diz o que fazer
		}
		return rep, nil

	case "list", "ls":
		jobs, err := transport.LocalJobList()
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "No local job.", nil
		}
		const maxRows = 20
		var b strings.Builder
		for i, j := range jobs {
			if i == maxRows {
				fmt.Fprintf(&b, "… +%d mais antigos\n", len(jobs)-maxRows)
				break
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", j.Meta.ID, j.State, jobAgeText(&j), jobLabel(j.Meta))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "", fmt.Errorf("unknown job action '%s' (valid: start, wait, status, list, tail, stop)", action)
}
