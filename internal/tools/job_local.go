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
	return fmt.Sprintf("%s | id=%s | processes=%d | up %s | last log %s ago | log %d B | %s",
		j.State, j.Meta.ID, j.Active, human(time.Since(j.Meta.CreatedAt)), human(time.Since(j.LastActivity)), j.LogSize, jobLabel(j.Meta))
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
		m, err := transport.StartLocalJob(args.Command, argv, args.Cwd, args.JobID)
		if err != nil {
			return "", err
		}
		note := ""
		if m.State != "RUNNING" {
			note = "\n(the process already exited; see the output with tail)"
		}
		return fmt.Sprintf("[job] iniciado (local)\nid:      %s\ncomando: %s\nlog:     %s%s\nUse job(host='local', action='status'|'tail'|'wait'|'stop', job_id='%s')",
			m.ID, jobLabel(*m), m.LogPath, note, m.ID), nil

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
		return fmt.Sprintf("[job] %s stopped (%s); process tree killed by the Job Object.", j.Meta.ID, j.State), nil

	case "wait":
		j, finished, err := transport.LocalJobWait(args.JobID, jobWaitMax)
		if err != nil {
			return "", err
		}
		tail, _ := transport.TailLocalJob(args.JobID, 20)
		tail = capOutput(cleanOutput(tail))
		if !finished {
			return fmt.Sprintf("[job %s still running after %s]\nANTI-POLLING: do not call 'wait' back to back. Use your client's scheduling (e.g. a timer) to check again in a few minutes, or end your turn telling the user.\n%s", j.Meta.ID, human(jobWaitMax), tail), nil
		}
		return fmt.Sprintf("[job %s finalizado: %s]\n%s", j.Meta.ID, j.State, tail), nil

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
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", j.Meta.ID, j.State, human(time.Since(j.Meta.CreatedAt)), jobLabel(j.Meta))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return "", fmt.Errorf("unknown job action '%s' (valid: start, wait, status, list, tail, stop)", action)
}
