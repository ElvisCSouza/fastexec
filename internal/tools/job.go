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

// jobWaitMax mantém o `wait` (local e Linux) abaixo do teto de 60 s do Claude Desktop: passou disso, responde que o job
// segue ativo em vez de deixar o transporte JSON-RPC cair. Variável só para os testes encurtarem.
var jobWaitMax = 35 * time.Second

// waitTimeoutMarker é impresso pelo laço remoto quando o prazo do wait acaba com o job ainda ativo.
const waitTimeoutMarker = "__FASTEXEC_WAIT_TIMEOUT__"

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

	target, err := transport.ResolveHost(targetHost)
	if err == nil && target.IsLocal {
		return handleLocalJob(action, args)
	}

	// hosts remotos (systemd-run): argv vira linha de comando com aspas simples POSIX
	if len(args.Argv) > 0 && args.Command == "" {
		args.Command = transport.QuoteArgvPOSIX(args.Argv)
	}
	if args.Cwd == "" && err == nil && target.DefaultCwd != "" {
		args.Cwd = target.DefaultCwd
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
		if strings.Contains(out, waitTimeoutMarker) {
			out = strings.TrimSpace(strings.ReplaceAll(out, waitTimeoutMarker, ""))
			return fmt.Sprintf("[job %s ainda em execução após %s; chame 'wait' de novo ou use 'tail']\n%s", unit, human(jobWaitMax), out), nil
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
