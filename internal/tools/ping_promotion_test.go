package tools

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/transport"
)

// A silent loopback ping longer than 15 probes is a wait: it becomes a background job, the same way a long pause does.
// Covers Windows (-n, >nul, | Out-Null), Linux (-c, >/dev/null), and a ping that starts a chain.
func TestSilentLoopbackPingWaitIsPromoted(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(time.Duration) { t.Fatal("a ping wait must not hold the call") }
	defer func() { sleepFn = origSleep }()

	cases := []string{
		"ping -n 30 127.0.0.1 >nul",
		"ping -n 30 localhost > nul",
		"ping -n 30 ::1 >nul",
		"ping -n 30 127.0.0.1 >nul 2>&1",
		"ping -n 30 127.0.0.1 | Out-Null",
		"ping.exe -n 16 localhost >nul",
		"ping -c 20 127.0.0.1 >/dev/null",
		"ping -c 20 localhost >/dev/null 2>&1",
		"ping -n 30 127.0.0.1 >nul && echo ready",
		"ping -n 30 127.0.0.1 >nul & echo ready",
		"ping -c 20 localhost >/dev/null; echo ok",
		"ping -n 30 127.0.0.1 >nul || echo failed",
	}
	for _, cmd := range cases {
		var got []JobArgs
		fakePromote(t, &got, nil)
		out, err := HandleExec(ExecArgs{Host: "local", Command: cmd})
		if err != nil {
			t.Errorf("%q: esperava promoção, erro: %v", cmd, err)
			continue
		}
		if !strings.Contains(out, "ping wait") || !strings.Contains(out, "job(action='wait'") {
			t.Errorf("%q: saída sem aviso de promoção: %s", cmd, out)
		}
		if len(got) != 1 || got[0].Action != "start" || got[0].Command != cmd {
			t.Errorf("%q: pedido de job inesperado: %+v", cmd, got)
		}
	}
}

// A shell argv carries its line as the last argument: the same rule applies to that line.
func TestShellArgvPingWaitIsPromoted(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"cmd /c", []string{"cmd", "/c", "ping -n 30 127.0.0.1 >nul"}},
		{"bash -c", []string{"bash", "-c", "ping -c 20 localhost >/dev/null"}},
		{"powershell -Command", []string{"powershell", "-Command", "ping -n 30 127.0.0.1 | Out-Null"}},
	}
	for _, c := range cases {
		var got []JobArgs
		fakePromote(t, &got, nil)
		_, handled, err := tryPromotePingWait(ExecArgs{Host: "local", Argv: c.argv})
		if err != nil || !handled {
			t.Errorf("%s: esperava promoção (handled=%v err=%v)", c.name, handled, err)
		}
		if len(got) != 1 || strings.Join(got[0].Argv, " ") != strings.Join(c.argv, " ") {
			t.Errorf("%s: argv do job deveria ser o mesmo: %+v", c.name, got)
		}
	}
}

// Everything that is not a silent loopback wait stays inline: visible output, short counts, remote targets, other
// flags, the ping not at the start of the chain, direct argv (no shell to suppress output), and unknown shapes.
func TestNonWaitPingsAreNotPromoted(t *testing.T) {
	commands := []string{
		"ping -n 30 127.0.0.1",                    // saída visível (diagnóstico)
		"ping -n 30 localhost",                    // saída visível
		"ping -n 15 127.0.0.1 >nul",               // 15 é o limite, não passa de 15
		"ping -n 3 127.0.0.1 >nul",                // contagem curta
		"ping -c 1 localhost >/dev/null",          // contagem curta (Linux)
		"ping -n 30 192.0.2.10 >nul",              // destino remoto
		"ping -n 30 router >nul",                  // nome da config, não loopback
		"ping -n 30 127.0.0.11 >nul",              // não é loopback (token inteiro)
		"ping -t 127.0.0.1 >nul",                  // -t: sem contagem
		"ping -n 30 -w 1000 127.0.0.1 >nul",       // flags extras: fora do padrão
		"ping -n 30 127.0.0.1 > out.txt",          // saída para arquivo: não é suprimida
		"ping -n 30 127.0.0.1 >nul | findstr x",   // filtro depois do supressor
		"echo start && ping -n 30 127.0.0.1 >nul", // ping não inicia a cadeia
		"tracert -n 127.0.0.1 >nul",               // outro comando
	}
	for _, cmd := range commands {
		var got []JobArgs
		fakePromote(t, &got, nil)
		_, handled, _ := tryPromotePingWait(ExecArgs{Host: "local", Command: cmd})
		if handled || len(got) != 0 {
			t.Errorf("%q: não deveria ser promovido (handled=%v, jobs=%d)", cmd, handled, len(got))
		}
	}

	direct := []ExecArgs{
		{Host: "local", Argv: []string{"ping", "-n", "30", "127.0.0.1"}},
		{Host: "local", Argv: []string{"ping", "-n", "30", "127.0.0.1", ">nul"}},
		{Host: "local", Argv: []string{"bash", "-lc", "ping -c 20 localhost >/dev/null"}},
	}
	for _, a := range direct {
		var got []JobArgs
		fakePromote(t, &got, nil)
		_, handled, _ := tryPromotePingWait(a)
		if handled || len(got) != 0 {
			t.Errorf("argv %v: não deveria ser promovido", a.Argv)
		}
	}
}

// On a Linux host the promoted command reaches systemd unchanged: the fake dispatch captures the payload, and the
// base64 body decodes to the same ping line (with only the cwd prefix the start path always adds).
func TestPingWaitLinuxPayloadIsUnchanged(t *testing.T) {
	ping := "ping -c 20 127.0.0.1 >/dev/null"
	var sent []string
	origDispatch := dispatch
	t.Cleanup(func() { dispatch = origDispatch })
	dispatch = func(host, cmd string, timeout time.Duration) (*transport.ExecResult, error) {
		sent = append(sent, cmd)
		return &transport.ExecResult{ExitCode: 0, Stdout: ""}, nil
	}

	out, err := HandleExec(ExecArgs{Host: "server", Command: ping, Cwd: "/srv/app"})
	if err != nil {
		t.Fatalf("promoção Linux falhou: %v (saída: %s)", err, out)
	}
	if !strings.Contains(out, "ping wait") {
		t.Fatalf("saída sem aviso de promoção: %s", out)
	}
	if len(sent) != 1 {
		t.Fatalf("esperava um envio ao host, houve %d", len(sent))
	}
	m := regexp.MustCompile(`echo '([A-Za-z0-9+/=]+)' \| base64 -d`).FindStringSubmatch(sent[0])
	if m == nil {
		t.Fatalf("payload sem base64 do comando: %s", sent[0])
	}
	body, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("base64 inválido: %v", err)
	}
	if want := `cd "/srv/app" && ` + ping; string(body) != want {
		t.Errorf("comando alterado no systemd:\n got: %q\nwant: %q", body, want)
	}
	if !strings.Contains(sent[0], "systemd-run --user") {
		t.Errorf("start sem systemd-run: %s", sent[0])
	}
}
