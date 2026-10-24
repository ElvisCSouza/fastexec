package tools

import (
	"fmt"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// localJobWaitMax mantém o `wait` abaixo do limite do cliente MCP (~4 min): passou disso, responde "ainda roda".
const localJobWaitMax = 200 * time.Second

func jobLabel(m transport.LocalJobMeta) string {
	s := m.Command
	if m.Argv != nil {
		s = strings.Join(m.Argv, " ")
	}
	if r := []rune(s); len(r) > 70 {
		s = string(r[:70]) + "…"
	}
	return s
}

func human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

func statusLine(j *transport.LocalJobInfo) string {
	return fmt.Sprintf("%s | id=%s | processos=%d | no ar há %s | último log há %s | log %d B | %s",
		j.State, j.Meta.ID, j.Active, human(time.Since(j.Meta.CreatedAt)), human(time.Since(j.LastActivity)), j.LogSize, jobLabel(j.Meta))
}

// handleLocalJob atende a tool `job` no host local (Windows): Job Object nomeado + log em arquivo (transport/localjob_windows.go).
func handleLocalJob(action string, args JobArgs) (string, error) {
	switch action {
	case "start":
		hasCmd, hasArgv := args.Command != "", len(args.Argv) > 0
		if hasCmd == hasArgv {
			return "", fmt.Errorf("ação 'start' requer 'command' (linha de shell) ou 'argv' (sem shell), só um dos dois")
		}
		var argv []string
		if hasArgv {
			argv = args.Argv
		}
		m, err := transport.StartLocalJob(args.Command, argv, args.Cwd, args.JobID)
		if err != nil {
			return "", err
		}
		note := ""
		if m.State != "RUNNING" {
			note = "\n(o processo já terminou; veja a saída com tail)"
		}
		return fmt.Sprintf("[job] iniciado (local)\nid:      %s\ncomando: %s\nlog:     %s%s\nUse job(host='local', action='status'|'tail'|'wait'|'stop', job_id='%s')",
			m.ID, jobLabel(*m), m.LogPath, note, m.ID), nil

	case "status", "tail", "stop", "kill", "wait":
		if args.JobID == "" {
			return "", fmt.Errorf("ação '%s' requer o parâmetro 'job_id'", action)
		}
	}

	switch action {
	case "status":
		j, err := transport.LocalJobStatus(args.JobID)
		if err != nil {
			return "", err
		}
		return statusLine(j), nil

	case "tail":
		lines := args.Lines
		if lines <= 0 {
			lines = 30
		}
		lines = min(lines, 500)
		out, err := transport.TailLocalJob(args.JobID, lines)
		if err != nil {
			return "", err
		}
		out = cleanOutput(out)
		if out == "" {
			return "(log vazio)", nil
		}
		return capOutput(out), nil

	case "stop", "kill":
		j, err := transport.LocalJobStop(args.JobID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[job] %s encerrado (%s); árvore de processos terminada pelo Job Object.", j.Meta.ID, j.State), nil

	case "wait":
		j, finished, err := transport.LocalJobWait(args.JobID, localJobWaitMax)
		if err != nil {
			return "", err
		}
		tail, _ := transport.TailLocalJob(args.JobID, 20)
		tail = capOutput(cleanOutput(tail))
		if !finished {
			return fmt.Sprintf("[job %s ainda em execução após %s; chame 'wait' de novo ou use 'tail']\n%s", j.Meta.ID, human(localJobWaitMax), tail), nil
		}
		return fmt.Sprintf("[job %s finalizado: %s]\n%s", j.Meta.ID, j.State, tail), nil

	case "list", "ls":
		jobs, err := transport.LocalJobList()
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "Nenhum job local.", nil
		}
		const maxRows = 20
		var b strings.Builder
		for i, j := range jobs {
			if i == maxRows {
				fmt.Fprintf(&b, "… +%d mais antigos\n", len(jobs)-maxRows)
				break
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", j.Meta.ID, j.State, human(time.Since(j.Meta.CreatedAt)), jobLabel(j.Meta))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "", fmt.Errorf("ação de job desconhecida: '%s' (ações válidas: start, wait, status, list, tail, stop)", action)
}
