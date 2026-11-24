package tools

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Testes puros do contrato Linux: não chamam systemctl nem journalctl, então rodam em qualquer SO.

const testUptime = int64(10_000_000_000) // 10 000 s desde o boot, em µs

func lu(load, active, sub, result string, status int, activeMono, inactiveMono, uptime int64) linuxUnit {
	return linuxUnit{Load: load, Active: active, Sub: sub, Result: result, Status: status,
		ActiveMono: activeMono, InactiveMono: inactiveMono, UptimeUs: uptime}
}

func TestLinuxJobStateTabela(t *testing.T) {
	ttlUs := int64(linuxJobTTL / 1000) // linuxJobTTL em µs
	cases := []struct {
		name    string
		u       linuxUnit
		want    string
		wantRC  *int
		wantRCn bool
	}{
		{"unit inexistente vira OUTCOME_UNKNOWN", lu("not-found", "inactive", "dead", "success", 0, 0, 0, testUptime), linuxStateUnknown, nil, false},
		{"activating vira STARTING", lu("loaded", "activating", "start", "success", 0, 0, 0, testUptime), linuxStateStarting, nil, false},
		{"active vira RUNNING", lu("loaded", "active", "running", "success", 0, testUptime-1000, 0, testUptime), linuxStateRunning, nil, false},
		{"exit-code 3 vira EXITED(3)", lu("loaded", "failed", "failed", "exit-code", 3, 100, 200, testUptime), linuxStateExited, ptr(3), true},
		{"success rc 0 vira EXITED(0)", lu("loaded", "inactive", "dead", "success", 0, 100, 200, testUptime), linuxStateExited, ptr(0), true},
		{"success com SIGTERM vira STOPPED", lu("loaded", "inactive", "dead", "success", 15, 100, 200, testUptime), linuxStateStopped, ptr(15), true},
		{"signal 9 vira FAILED(9)", lu("loaded", "failed", "failed", "signal", 9, 100, 200, testUptime), linuxStateFailed, ptr(9), true},
		{"nunca ativou e falhou no start vira FAILED sem rc", lu("loaded", "failed", "failed", "start-limit-hit", 0, 0, 200, testUptime), linuxStateFailed, nil, false},
		{"terminou há mais que o TTL vira EXPIRED", lu("loaded", "inactive", "dead", "success", 0, 100, 200, 200+ttlUs+1), linuxStateExpired, ptr(0), true},
		{"terminou dentro do TTL continua EXITED", lu("loaded", "inactive", "dead", "success", 0, 100, 200, 200+ttlUs), linuxStateExited, ptr(0), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, rc := linuxJobState(c.u)
			if st != c.want {
				t.Fatalf("state = %s, want %s", st, c.want)
			}
			if (rc == nil) != (c.wantRC == nil) || (rc != nil && *rc != *c.wantRC) {
				t.Fatalf("rc = %v, want %v", fmtRC(rc), fmtRC(c.wantRC))
			}
		})
	}
}

func TestLinuxStateNuncaLidoComoNaoExecutado(t *testing.T) {
	// perda do registro (unit sumiu) não pode virar "não rodou": é OUTCOME_UNKNOWN, nunca EXITED/FAILED silencioso
	st, _ := linuxJobState(linuxUnit{})
	if st != linuxStateUnknown {
		t.Fatalf("unit sem LoadState deve ser OUTCOME_UNKNOWN, veio %s", st)
	}
}

func TestLinuxIntentEReplayConflito(t *testing.T) {
	if linuxIntent("echo a") == linuxIntent("echo b") {
		t.Fatal("comandos diferentes devem ter impressões digitais diferentes")
	}
	if linuxIntent("echo a") != linuxIntent("echo a") {
		t.Fatal("impressão digital deve ser estável")
	}
	desc := linuxDescription("job-x", linuxIntent("echo a"))
	if got := linuxIntentOf(desc); got != linuxIntent("echo a") {
		t.Fatalf("intent do Description = %q", got)
	}
	if linuxIntentOf("FastExec Job job-old") != "" {
		t.Fatal("unit antigo sem intent deve devolver vazio")
	}

	running := lu("loaded", "active", "running", "success", 0, 100, 0, testUptime)
	running.Description = desc

	rep, err := linuxStartExisting("x", "job-x", running, "echo a")
	if err != nil || !strings.Contains(rep, "replayed") || !strings.Contains(rep, "no new process started") {
		t.Fatalf("replay esperado, veio err=%v rep=%q", err, rep)
	}
	if _, err := linuxStartExisting("x", "job-x", running, "echo b"); err == nil || !strings.Contains(err.Error(), "already used by a different job") {
		t.Fatalf("conflito esperado, veio %v", err)
	}
	legacy := running
	legacy.Description = "FastExec Job job-x"
	if _, err := linuxStartExisting("x", "job-x", legacy, "echo a"); err == nil {
		t.Fatal("unit antigo sem impressão digital deve ser conflito, não replay")
	}
}

func TestLinuxJobIDValidation(t *testing.T) {
	ok := []string{"a", "job-1", "A_b-9", strings.Repeat("a", 40)}
	bad := []string{"", "-x", "a b", "a'b", "a;b", strings.Repeat("a", 41), "é"}
	for _, id := range ok {
		if !linuxJobIDRegex.MatchString(id) {
			t.Errorf("id %q deveria ser aceito", id)
		}
	}
	for _, id := range bad {
		if linuxJobIDRegex.MatchString(id) {
			t.Errorf("id %q deveria ser recusado", id)
		}
	}
}

func TestSplitLinuxUnitLeBlocoEDevolveResto(t *testing.T) {
	out := "antes do bloco\n__FASTEXEC_UNIT__\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nResult=exit-code\n" +
		"ExecMainStatus=3\nActiveEnterTimestampMonotonic=100\nInactiveEnterTimestampMonotonic=200\n" +
		"Description=FastExec Job job-x intent=abcd\nUptimeUs=300\ndepois do bloco\n"
	rest, u, complete := splitLinuxUnit(out)
	if !complete {
		t.Fatal("bloco completo deveria ser reconhecido")
	}
	if rest != "antes do bloco\ndepois do bloco\n" {
		t.Fatalf("resto = %q", rest)
	}
	if u.Status != 3 || u.Result != "exit-code" || u.UptimeUs != 300 || linuxIntentOf(u.Description) != "abcd" {
		t.Fatalf("unit parseado errado: %+v", u)
	}
	if _, _, complete := splitLinuxUnit("sem marcador"); complete {
		t.Fatal("sem marcador não é bloco completo")
	}
	if _, _, complete := splitLinuxUnit("__FASTEXEC_UNIT__\nLoadState=loaded\n"); complete {
		t.Fatal("bloco cortado (sem uptime) não é completo")
	}
}

func TestLinuxStopVerdict(t *testing.T) {
	active := lu("loaded", "active", "running", "success", 0, 100, 0, testUptime)
	stoppedNow := lu("loaded", "inactive", "dead", "success", 15, 100, 200, testUptime)
	stopping := lu("loaded", "deactivating", "stop-sigterm", "success", 0, 100, 0, testUptime)
	gone := lu("not-found", "inactive", "dead", "success", 0, 0, 0, testUptime)
	finished := lu("loaded", "inactive", "dead", "success", 0, 100, 200, testUptime)

	cases := []struct {
		name   string
		pre    linuxUnit
		rc     int
		post   linuxUnit
		seen   bool
		want   string
		reason string
	}{
		{"parou: confirmed", active, 0, stoppedNow, true, "confirmed", "STOPPED"},
		{"ainda parando: accepted, sem retry", active, 0, stopping, true, "accepted", "still"},
		{"systemd recusou: denied", active, 1, active, true, "denied", "refused"},
		{"já terminou: denied", finished, 0, finished, true, "denied", "already finished"},
		{"unit inexistente: denied com OUTCOME_UNKNOWN", gone, 0, gone, true, "denied", "OUTCOME_UNKNOWN"},
		{"unit sumiu depois do stop: denied", active, 0, linuxUnit{}, false, "denied", "vanished"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, r := linuxStopVerdict(c.pre, c.rc, c.post, c.seen)
			if v != c.want || !strings.Contains(r, c.reason) {
				t.Fatalf("verdict=%s reason=%q; want %s com %q", v, r, c.want, c.reason)
			}
		})
	}
}

// emulateLinuxCursorOut reproduz a saída que o script do host imprimiria para um log de tamanho len(log). Serve para
// testar a reconstrução em blocos sem shell: a mesma aritmética do script (C, S, T, bloco [C, C+32KiB)).
func emulateLinuxCursorOut(log []byte, cursor int64) string {
	S := int64(len(log))
	C := cursor
	T := 0
	if C > S {
		T = 1
		C = 0
	}
	end := min(C+linuxMaxCursorBlock, S)
	block := log[C:end]
	return fmt.Sprintf("CUR_SIZE=%d\nCUR_TRUNC=%d\nCUR_START=%d\nCUR_B64=%s\n", S, T, C, base64.StdEncoding.EncodeToString(block))
}

func TestCursorReconstroiLogGrandeSemPerdaNemRepeticao(t *testing.T) {
	// > 64 KiB, com ANSI, linha longa sem '\n' (força corte por tamanho), runas multibyte que podem cair na borda do bloco
	var b strings.Builder
	for i := 0; b.Len() < 150<<10; i++ {
		fmt.Fprintf(&b, "\x1b[32mlinha %d\x1b[0m çã€😀\n", i)
	}
	b.WriteString(strings.Repeat("L", 70_000)) // sem '\n'
	b.WriteString("€€€€\nfim\n")
	log := []byte(b.String())

	var got strings.Builder
	var cur int64
	chunks := 0
	for {
		ch, err := parseLinuxCursor(emulateLinuxCursorOut(log, cur))
		if err != nil {
			t.Fatalf("parse no cursor %d: %v", cur, err)
		}
		if !utf8.ValidString(ch.Text) {
			t.Fatalf("pedaço com UTF-8 quebrado no cursor %d", cur)
		}
		if ch.Next < cur || ch.Next > ch.Size {
			t.Fatalf("next %d fora do intervalo [%d, %d]", ch.Next, cur, ch.Size)
		}
		if ch.Next == cur && cur < ch.Size {
			t.Fatalf("sem progresso no cursor %d", cur)
		}
		if int64(len(ch.Text)) != ch.Next-cur {
			t.Fatalf("texto (%d bytes) não bate com next-cursor (%d)", len(ch.Text), ch.Next-cur)
		}
		got.WriteString(ch.Text)
		chunks++
		if ch.Next == ch.Size {
			break
		}
		cur = ch.Next
		if chunks > 1000 {
			t.Fatal("laço sem fim")
		}
	}
	if got.String() != string(log) {
		t.Fatalf("reconstrução difere: got %d bytes, want %d", got.Len(), len(log))
	}
	if chunks < 3 {
		t.Fatalf("esperava pelo menos 3 blocos para > 64 KiB, veio %d", chunks)
	}
	if !strings.Contains(got.String(), "\x1b[32m") {
		t.Fatal("ANSI deveria passar intacto")
	}
	// replay: o mesmo cursor devolve exatamente o mesmo pedaço
	a, _ := parseLinuxCursor(emulateLinuxCursorOut(log, 0))
	bch, _ := parseLinuxCursor(emulateLinuxCursorOut(log, 0))
	if a != bch {
		t.Fatal("replay do mesmo cursor deve ser idêntico")
	}
}

func TestCursorTruncadoEExpirado(t *testing.T) {
	log := []byte("abc")
	ch, err := parseLinuxCursor(emulateLinuxCursorOut(log, 10))
	if err != nil || !ch.Truncated || ch.Text != "abc" || ch.Next != 3 {
		t.Fatalf("cursor além do fim: %+v err=%v (espera truncated, recomeça em 0)", ch, err)
	}
	ch, err = parseLinuxCursor("CUR_MISSING=1\n")
	if err != nil || !ch.Expired {
		t.Fatalf("log ausente deve ser expired: %+v err=%v", ch, err)
	}
	ch, err = parseLinuxCursor(emulateLinuxCursorOut(log, 3))
	if err != nil || ch.Text != "" || ch.Next != 3 || ch.Truncated {
		t.Fatalf("cursor no fim: %+v err=%v", ch, err)
	}
	if _, err := parseLinuxCursor("lixo sem contrato"); err == nil {
		t.Fatal("saída sem SIZE/START deve dar erro")
	}
	if _, err := parseLinuxCursor("CUR_JERR=1\n"); err == nil {
		t.Fatal("falha do journalctl deve dar erro, nunca ler arquivo antigo como se fosse atual")
	}
	if _, err := parseLinuxCursor("CUR_SIZE=1\nCUR_START=0\nCUR_B64=!!!\n"); err == nil {
		t.Fatal("base64 inválido deve dar erro")
	}
}

func ptr(v int) *int { return &v }

func fmtRC(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}
