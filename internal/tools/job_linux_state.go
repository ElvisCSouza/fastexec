package tools

// Contrato do job Linux (systemd --user). Tudo aqui é função pura ou gera/lê texto de shell: não chama systemctl nem
// journalctl, então roda e é testado em qualquer SO. O estado vive no próprio unit transitório (sem arquivo de
// metadado no host): o pedido (comando + cwd) vai no Description como impressão digital, e o estado sai do
// `systemctl show`. Os textos de estado são os mesmos do Windows (transport/localjob_windows.go).

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"homelab/fastexec/internal/transport"
)

// Estados persistidos e consultados. Espelham os do Windows (strings iguais, para o modelo ver o mesmo vocabulário).
const (
	linuxStateStarting = "STARTING"
	linuxStateRunning  = "RUNNING"
	linuxStateExited   = "EXITED"
	linuxStateStopped  = "STOPPED"
	linuxStateExpired  = "EXPIRED"
	linuxStateFailed   = "FAILED"
	linuxStateUnknown  = "OUTCOME_UNKNOWN" // unit sumiu / não existe: não se sabe se rodou; nunca lido como "não executado"
)

// linuxJobTTL: prazo declarado de retenção de um job finalizado. Passado disso, o estado vira EXPIRED. O FastExec não
// apaga unit nem journal; quem limpa é o systemd/journald. Ver spec-job-falha-rapida.md.
const linuxJobTTL = 7 * 24 * time.Hour

// linuxMaxCursorBlock: bytes lidos por chamada de cursor no host. Igual ao teto do contrato de bytes (LogChunkLimit).
const linuxMaxCursorBlock = transport.LogChunkLimit

// linuxJobIDRegex é o mesmo critério do Windows: id curto, sem espaço nem aspas (o id vai para a linha de shell).
var linuxJobIDRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// linuxUnitMarker abre o bloco que o host imprime com as propriedades do unit; linuxUptimeKey fecha o bloco.
const (
	linuxUnitMarker = "__FASTEXEC_UNIT__"
	linuxUptimeKey  = "UptimeUs="
	linuxStopRCKey  = "__FASTEXEC_STOP_RC="
	linuxCursorKey  = "CUR_" // prefixo das linhas do cursor
)

// linuxUnit: propriedades do systemd que decidem o estado. Monotônicos em microssegundos desde o boot.
type linuxUnit struct {
	Load, Active, Sub, Result, Description string
	Status                                 int   // ExecMainStatus: rc (Result=exit-code/success) ou sinal
	ActiveMono, InactiveMono               int64 // ActiveEnterTimestampMonotonic / InactiveEnterTimestampMonotonic
	UptimeUs                               int64 // /proc/uptime no instante da consulta
}

// linuxIntent: impressão digital do pedido. Só o hash entra no Description (sem o comando em claro).
func linuxIntent(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])[:16]
}

// linuxDescription é o Description gravado no unit no start: carrega o id e a impressão digital.
func linuxDescription(unit, intent string) string {
	return fmt.Sprintf("FastExec Job %s intent=%s", unit, intent)
}

// linuxIntentOf lê a impressão digital do Description. Unit sem ela (criado por versão anterior) devolve "".
func linuxIntentOf(description string) string {
	i := strings.LastIndex(description, " intent=")
	if i < 0 {
		return ""
	}
	return description[i+len(" intent="):]
}

// linuxUnitShow: fragmento de shell que imprime as propriedades do unit e o uptime. Sem --collect: o unit finalizado
// continua consultável até o systemd descarregá-lo, e é isso que sustenta o estado depois do término.
func linuxUnitShow(unit string) string {
	return fmt.Sprintf("echo %s; systemctl --user show -p LoadState -p ActiveState -p SubState -p Result -p ExecMainStatus "+
		"-p ActiveEnterTimestampMonotonic -p InactiveEnterTimestampMonotonic -p Description '%s.service'; "+
		"awk '{print \"%s\" int($1*1000000)}' /proc/uptime 2>/dev/null; ", linuxUnitMarker, unit, linuxUptimeKey)
}

// splitLinuxUnit tira o primeiro bloco de propriedades do texto. rest é o texto sem o bloco (o resto da saída fica).
// complete = o bloco inteiro (até o uptime) chegou. Unit inexistente também é bloco completo: quem decide se existe é
// linuxFound(u).
func splitLinuxUnit(out string) (rest string, u linuxUnit, complete bool) {
	i := strings.Index(out, linuxUnitMarker)
	if i < 0 {
		return out, linuxUnit{}, false
	}
	head := out[:i]
	block := out[i+len(linuxUnitMarker):]
	consumed := 0
	for _, line := range strings.SplitAfter(block, "\n") {
		consumed += len(line)
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, linuxUptimeKey) {
			u.apply(t)
			return head + block[consumed:], u, true
		}
		u.apply(t)
	}
	// bloco incompleto (saída cortada): consome tudo para não vazar propriedades no texto
	return head, u, false
}

// apply lê uma linha "Chave=valor" do bloco do systemd.
func (u *linuxUnit) apply(line string) {
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return
	}
	switch k {
	case "LoadState":
		u.Load = v
	case "ActiveState":
		u.Active = v
	case "SubState":
		u.Sub = v
	case "Result":
		u.Result = v
	case "ExecMainStatus":
		u.Status, _ = strconv.Atoi(v)
	case "ActiveEnterTimestampMonotonic":
		u.ActiveMono, _ = strconv.ParseInt(v, 10, 64)
	case "InactiveEnterTimestampMonotonic":
		u.InactiveMono, _ = strconv.ParseInt(v, 10, 64)
	case "Description":
		u.Description = v
	case strings.TrimSuffix(linuxUptimeKey, "="):
		u.UptimeUs, _ = strconv.ParseInt(v, 10, 64)
	}
}

// linuxFound: o unit existe no host (systemd carrega LoadState diferente de not-found).
func linuxFound(u linuxUnit) bool {
	return u.Load != "" && u.Load != "not-found"
}

// linuxRC: rc do processo quando o systemd o registrou (saída normal ou sinal); nil para unit que não terminou.
func linuxRC(u linuxUnit) *int {
	if u.Result == "" || u.Active == "active" || u.Active == "activating" {
		return nil
	}
	rc := u.Status
	return &rc
}

// linuxJobState decide o estado a partir do unit. Função pura: a mesma entrada dá a mesma resposta.
//   - unit inexistente: OUTCOME_UNKNOWN (perda de registro não é "não executado");
//   - activating: STARTING; active/reloading/deactivating: RUNNING;
//   - inativo há mais que linuxJobTTL: EXPIRED;
//   - nunca ativou e falhou (recurso, limite de start): FAILED;
//   - Result=exit-code, ou success com rc 0: EXITED(rc);
//   - success com sinal de encerramento limpo que o próprio systemd manda (SIGINT/SIGPIPE/SIGTERM): STOPPED;
//   - demais Results (signal, core-dump, timeout, watchdog): FAILED(rc).
//
// Heurística: o systemd conta SIGTERM/SIGINT/SIGPIPE como saída limpa quando é ele que para o unit, por isso o
// Result=success com esses sinais vira STOPPED. Validar em host real antes de confiar 100%.
func linuxJobState(u linuxUnit) (string, *int) {
	if !linuxFound(u) {
		return linuxStateUnknown, nil
	}
	switch u.Active {
	case "activating":
		return linuxStateStarting, nil
	case "active", "reloading", "deactivating":
		return linuxStateRunning, nil
	}
	rc := linuxRC(u)
	if u.InactiveMono > 0 && u.UptimeUs-u.InactiveMono > int64(linuxJobTTL/time.Microsecond) {
		return linuxStateExpired, rc
	}
	if u.ActiveMono == 0 && u.Result != "success" && u.Result != "exit-code" {
		return linuxStateFailed, nil
	}
	switch u.Result {
	case "exit-code":
		return linuxStateExited, rc
	case "success":
		if u.Status == 0 {
			return linuxStateExited, rc
		}
		switch u.Status {
		case 2, 13, 15: // SIGINT, SIGPIPE, SIGTERM
			return linuxStateStopped, rc
		}
		return linuxStateExited, rc
	}
	return linuxStateFailed, rc
}

// linuxFinished: estado terminal (o job não roda mais).
func linuxFinished(state string) bool {
	switch state {
	case linuxStateExited, linuxStateStopped, linuxStateExpired, linuxStateFailed:
		return true
	}
	return false
}

// linuxStopVerdict decide o veredito de um stop com a consulta antes e depois do único `systemctl stop`.
//   - confirmed: o unit saiu de ativo (o estado final vai na linha);
//   - accepted: o systemd aceitou (rc 0), mas o unit ainda está parando/ativo: consultar de novo, sem retry;
//   - denied: nada a parar (já terminou, não existe) ou o systemd recusou.
//
// Quando não existe unit, o veredito diz OUTCOME_UNKNOWN: não é "não rodou".
func linuxStopVerdict(pre linuxUnit, stopRC int, post linuxUnit, postSeen bool) (verdict, reason string) {
	if !linuxFound(pre) {
		return "denied", "no unit for this job_id on the host; state OUTCOME_UNKNOWN, nothing was confirmed"
	}
	if st, _ := linuxJobState(pre); linuxFinished(st) {
		return "denied", "job already finished (state " + st + ")"
	}
	if stopRC != 0 {
		return "denied", "terminate refused by systemd"
	}
	if !postSeen || !linuxFound(post) {
		return "denied", "unit vanished after stop; state OUTCOME_UNKNOWN"
	}
	switch post.Active {
	case "active", "activating", "reloading", "deactivating":
		return "accepted", "stop sent; unit still " + post.Active + ", check status"
	}
	st, _ := linuxJobState(post)
	return "confirmed", "unit inactive, state " + st
}

// linuxCursorScript: materializa o journal do unit em arquivo do host (escrita atômica) e lê o bloco [cursor, cursor+32 KiB)
// em base64. Com isso o caminho Linux usa o mesmo corte (ChunkFromBlock) e o mesmo cabeçalho (formatCursorReply) do Windows.
// O arquivo fica em XDG_RUNTIME_DIR (tmpfs do usuário): some no logout, e aí a leitura devolve expired.
// Custo conhecido: cada chamada de cursor regrava o journal inteiro do unit (dívida, na spec).
func linuxCursorScript(unit string, cursor int64) string {
	f := fmt.Sprintf("${XDG_RUNTIME_DIR:-/tmp}/fastexec-%s.log", unit)
	return fmt.Sprintf("F=\"%[1]s\"; if journalctl --user-unit='%[2]s.service' -o cat --no-pager > \"$F.tmp\" 2>/dev/null && mv -f \"$F.tmp\" \"$F\"; then "+
		"if [ ! -f \"$F\" ]; then echo %[3]sMISSING=1; else S=$(wc -c < \"$F\"); C=%[4]d; T=0; "+
		"if [ \"$C\" -gt \"$S\" ]; then T=1; C=0; fi; echo %[3]sSIZE=$S; echo %[3]sTRUNC=$T; echo %[3]sSTART=$C; "+
		"echo %[3]sB64=$(tail -c +$((C+1)) \"$F\" | head -c %[5]d | base64 -w0); fi; "+
		"else R=$?; rm -f \"$F.tmp\"; echo %[3]sJERR=$R; fi",
		f, unit, linuxCursorKey, cursor, linuxMaxCursorBlock)
}

// parseLinuxCursor lê a saída de linuxCursorScript e monta o pedaço pelo contrato compartilhado (ChunkFromBlock).
func parseLinuxCursor(out string) (transport.LogChunk, error) {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, linuxCursorKey) {
			continue
		}
		k, v, _ := strings.Cut(strings.TrimPrefix(line, linuxCursorKey), "=")
		kv[k] = v
	}
	if kv["JERR"] != "" {
		return transport.LogChunk{}, fmt.Errorf("journalctl failed on host (exit %s); log state is unknown", kv["JERR"])
	}
	if kv["MISSING"] == "1" {
		return transport.LogChunk{Expired: true}, nil
	}
	size, err1 := strconv.ParseInt(kv["SIZE"], 10, 64)
	start, err2 := strconv.ParseInt(kv["START"], 10, 64)
	if err1 != nil || err2 != nil {
		return transport.LogChunk{}, fmt.Errorf("cursor read from host returned no size/start")
	}
	block, err := base64.StdEncoding.DecodeString(kv["B64"])
	if err != nil {
		return transport.LogChunk{}, fmt.Errorf("cursor read from host returned invalid block: %v", err)
	}
	ch := transport.ChunkFromBlock(block, start, size)
	ch.Truncated = kv["TRUNC"] == "1"
	return ch, nil
}
