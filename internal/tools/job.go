package tools

import (
	"encoding/base64"
	"fmt"
	"math/rand"
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
}

func sanitizeUnit(id string) string {
	s := strings.TrimSpace(id)
	if !strings.HasPrefix(s, "job-") {
		s = "job-" + s
	}
	s = strings.TrimSuffix(s, ".service")
	return s
}

func HandleJob(args JobArgs) (string, error) {
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		action = "list"
	}

	targetHost := args.Host
	if targetHost == "" {
		targetHost = "noteserver"
	}

	if target, err := transport.ResolveHost(targetHost); err == nil && target.IsLocal {
		return handleLocalJob(action, args)
	}

	// hosts remotos (systemd-run): argv vira linha de comando com aspas simples POSIX
	if len(args.Argv) > 0 && args.Command == "" {
		args.Command = transport.QuoteArgvPOSIX(args.Argv)
	}
	if args.Cwd != "" && args.Command != "" {
		args.Command = "cd \"" + args.Cwd + "\" && " + args.Command
	}

	envPrefix := "export XDG_RUNTIME_DIR=/run/user/1000; "

	switch action {
	case "start":
		if args.Command == "" {
			return "", fmt.Errorf("ação 'start' requer o parâmetro 'command'")
		}
		jobID := args.JobID
		if jobID == "" {
			jobID = fmt.Sprintf("%s_%d", time.Now().Format("20060102_150405"), rand.Intn(9000)+1000)
		}
		unit := sanitizeUnit(jobID)

		b64Cmd := base64.StdEncoding.EncodeToString([]byte(args.Command))
		cmd := fmt.Sprintf("%s systemd-run --user --unit='%s' --description='FastExec Job %s' /bin/bash -c \"$(echo '%s' | base64 -d)\"",
			envPrefix, unit, unit, b64Cmd)

		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return fmt.Sprintf("[Job Start Error: exit %d] %s", res.ExitCode, res.Stderr), fmt.Errorf("falha ao iniciar job: %s", res.Stderr)
		}

		return fmt.Sprintf("[job] Iniciado com sucesso\nUnit:    %s.service\nComando: %s\nStatus:  Use job(action='status', job_id='%s')\nWait:    Use job(action='wait', job_id='%s')\nTail:    Use job(action='tail', job_id='%s')",
			unit, args.Command, unit, unit, unit), nil

	case "wait":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("ação 'wait' requer o parâmetro 'job_id'")
		}
		// Aguarda o processo terminar no SO e devolve o log final instantaneamente no milissegundo do término
		cmd := fmt.Sprintf("%s while systemctl --user is-active --quiet '%s.service'; do sleep 0.5; done; journalctl --user-unit='%s.service' -n 50 --no-pager",
			envPrefix, unit, unit)
		res, err := transport.Dispatch(targetHost, cmd, 600*time.Second)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		return fmt.Sprintf("[job %s finalizado]\n%s", unit, out), nil

	case "status":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("ação 'status' requer o parâmetro 'job_id'")
		}
		cmd := fmt.Sprintf("%s systemctl --user status '%s.service' --no-pager", envPrefix, unit)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		return out, nil

	case "list", "ls":
		cmd := fmt.Sprintf("%s systemctl --user list-units 'job-*.service' --all --no-pager", envPrefix)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := strings.TrimSpace(res.Stdout)
		if out == "" || strings.Contains(out, "0 loaded units listed") {
			return "Nenhum job encontrado em execução ou finalizado recentemente.", nil
		}
		return out, nil

	case "tail":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("ação 'tail' requer o parâmetro 'job_id'")
		}
		lines := args.Lines
		if lines <= 0 {
			lines = 30
		}
		cmd := fmt.Sprintf("%s journalctl --user-unit='%s.service' -n %d --no-pager", envPrefix, unit, lines)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		return out, nil

	case "stop", "kill":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("ação 'stop' requer o parâmetro 'job_id'")
		}
		cmd := fmt.Sprintf("%s systemctl --user stop '%s.service'", envPrefix, unit)
		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return fmt.Sprintf("Erro ao parar job: %s", res.Stderr), fmt.Errorf("falha ao parar job: %s", res.Stderr)
		}
		return fmt.Sprintf("[job] %s.service parado com sucesso.", unit), nil

	default:
		return "", fmt.Errorf("ação de job desconhecida: '%s' (ações válidas: start, wait, status, list, tail, stop)", action)
	}
}
