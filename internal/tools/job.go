package tools

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"runtime"
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
		cmd := fmt.Sprintf("%s systemd-run --user --unit='%s' --description='FastExec Job %s' /bin/bash -c \"$(echo '%s' | base64 -d)\"",
			envPrefix, unit, unit, b64Cmd)

		res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return fmt.Sprintf("[Job Start Error: exit %d] %s", res.ExitCode, res.Stderr), fmt.Errorf("could not start job: %s", res.Stderr)
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
		return waitReport(unit, args.Host+"|"+unit, out, !running, ""), nil

	case "status":
		unit := sanitizeUnit(args.JobID)
		if args.JobID == "" {
			return "", fmt.Errorf("action 'status' needs 'job_id'")
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
