package tools

// Operações do job Linux que falam com o host (transport.Dispatch). Cada uma faz no máximo uma ida ao host para
// leitura e, quando é mutação (start, stop), uma única tentativa: sem retry automático.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// linuxShow lê o estado do unit no host. Erro se a saída não trouxe o bloco (ssh/systemd indisponível): aí não há
// como afirmar nada, e o chamador não pode tratar isso como "unit inexistente".
func linuxShow(targetHost, envPrefix, unit string) (linuxUnit, error) {
	res, err := transport.Dispatch(targetHost, envPrefix+linuxUnitShow(unit), transport.DefaultTimeout)
	if err != nil {
		return linuxUnit{}, err
	}
	_, u, ok := splitLinuxUnit(res.Stdout)
	if !ok {
		return linuxUnit{}, fmt.Errorf("could not read job state from host: %s", strings.TrimSpace(res.Stderr))
	}
	return u, nil
}

// linuxStateHeader: linha de estado que acompanha tail/wait/status. Unit inexistente é OUTCOME_UNKNOWN, não "não rodou".
func linuxStateHeader(u linuxUnit, running bool) string {
	if !linuxFound(u) {
		return "[job state=OUTCOME_UNKNOWN: no unit for this job_id on the host; it may have expired or never started]\n"
	}
	st, _ := linuxJobState(u)
	if running && st == linuxStateRunning {
		return "[job state=RUNNING: wait budget reached; call wait again with the same cursor]\n"
	}
	return fmt.Sprintf("[job state=%s]\n", st)
}

// linuxStartExisting decide o que fazer quando o job_id já tem unit: replay (mesmo pedido, nada novo começa) ou
// conflito (outro pedido com o mesmo id). Unit antigo sem impressão digital conta como conflito.
func linuxStartExisting(jobID, unit string, u linuxUnit, command string) (string, error) {
	if linuxIntentOf(u.Description) != linuxIntent(command) {
		return "", fmt.Errorf("job_id '%s' is already used by a different job; use a new job_id", jobID)
	}
	st, _ := linuxJobState(u)
	return fmt.Sprintf("[job %s replayed: same job_id and request; no new process started]\nUnit:    %s.service\nState:   %s\nStatus:  job(action='status', job_id='%s')",
		jobID, unit, st, unit), nil
}

// linuxCursorCall lê o log do unit a partir do cursor (tail, ou wait com orçamento). Usa o mesmo corte e cabeçalho do
// Windows (ChunkFromBlock / formatCursorReply) e acrescenta o estado do unit.
func linuxCursorCall(targetHost, envPrefix, unit, cursor string, wait bool) (string, error) {
	cur, perr := strconv.ParseInt(cursor, 10, 64)
	if perr != nil || cur < 0 {
		return "", fmt.Errorf("cursor must be the decimal byte offset returned by a previous tail/wait")
	}
	loop := ""
	timeout := transport.DefaultTimeout
	if wait {
		loop = fmt.Sprintf("end=$(( $(date +%%s) + %d )); while systemctl --user is-active --quiet '%s.service'; do if [ $(date +%%s) -ge $end ]; then echo %s; break; fi; sleep 0.5; done; ",
			int(jobWaitMax/time.Second), unit, waitTimeoutMarker)
		timeout = jobWaitMax + 5*time.Second
	}
	res, err := transport.Dispatch(targetHost, envPrefix+loop+linuxUnitShow(unit)+linuxCursorScript(unit, cur), timeout)
	if err != nil {
		return "", err
	}
	out := res.Stdout
	running := strings.Contains(out, waitTimeoutMarker)
	out = strings.ReplaceAll(out, waitTimeoutMarker, "")
	rest, u, ok := splitLinuxUnit(out)
	if !ok {
		return "", fmt.Errorf("could not read job log from host: %s", strings.TrimSpace(res.Stderr))
	}
	ch, err := parseLinuxCursor(rest)
	if err != nil {
		return "", err
	}
	return linuxStateHeader(u, running) + formatCursorReply(ch), nil
}

// linuxStopOnce faz o stop com a consulta antes e depois, num único comando do host, e devolve o veredito. Sem retry:
// stop é mutação, e um segundo `systemctl stop` não é repetição segura se o primeiro já tiver sido aceito.
func linuxStopOnce(targetHost, envPrefix, unit string) (string, error) {
	cmd := envPrefix + linuxUnitShow(unit) +
		fmt.Sprintf("systemctl --user stop '%s.service'; echo %s$?; ", unit, linuxStopRCKey) + linuxUnitShow(unit)
	res, err := transport.Dispatch(targetHost, cmd, transport.DefaultTimeout)
	if err != nil {
		return "", err
	}
	rest, pre, ok := splitLinuxUnit(res.Stdout)
	if !ok {
		return "", fmt.Errorf("could not read job state from host: %s", strings.TrimSpace(res.Stderr))
	}
	i := strings.Index(rest, linuxStopRCKey)
	if i < 0 {
		return "", fmt.Errorf("could not read stop result from host")
	}
	after := rest[i+len(linuxStopRCKey):]
	rcLine, tail, _ := strings.Cut(after, "\n")
	stopRC, _ := strconv.Atoi(strings.TrimSpace(rcLine))
	_, post, postOK := splitLinuxUnit(tail)
	verdict, reason := linuxStopVerdict(pre, stopRC, post, postOK)
	state := linuxStateUnknown
	if postOK && linuxFound(post) {
		state, _ = linuxJobState(post)
	}
	return fmt.Sprintf("[job %s.service cancel %s: %s | state %s]", unit, verdict, reason, state), nil
}
