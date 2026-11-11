package cli

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"homelab/fastexec/internal/telemetry"
)

func TestMonitorServer(t *testing.T) {
	sandbox(t)
	s, err := newMonitorServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	base := "http://" + s.addr
	fixed := telemetry.SavingsReport{Period: "today", Calls: 1, SavedTokens: 1000, LastID: 7,
		Recent: []telemetry.SavingEvent{{ID: 7, Tool: "exec", Cats: []string{"clean"}}}}
	s.report = func(p string) (telemetry.SavingsReport, error) { return fixed, nil }
	s.lastID = func() int64 { return 7 }

	get := func(path, tok, host string) (*http.Response, int) {
		req, _ := http.NewRequest("GET", base+path, nil)
		if tok != "" {
			req.Header.Set("X-Token", tok)
		}
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp, resp.StatusCode
	}
	for name, c := range map[string]struct{ tok, host string }{
		"no token":      {"", ""},
		"wrong token":   {"wrong", ""},
		"foreign host":  {s.token, "evil.example:80"},
		"rebinding dns": {s.token, "localhost.evil.example:" + s.addr[strings.LastIndex(s.addr, ":")+1:]},
	} {
		resp, code := get("/api/report", c.tok, c.host)
		resp.Body.Close()
		if code != 403 {
			t.Fatalf("%s: %d, esperado 403", name, code)
		}
	}

	resp, code := get("/api/report?p=today", s.token, "")
	if code != 200 {
		t.Fatalf("report: %d", code)
	}
	var rep telemetry.SavingsReport
	_ = json.NewDecoder(resp.Body).Decode(&rep)
	resp.Body.Close()
	if rep.SavedTokens != 1000 || rep.Calls != 1 || len(rep.Recent) != 1 || rep.Recent[0].Cats[0] != "clean" {
		t.Fatalf("relatório inesperado: %+v", rep)
	}

	// a página leva o token e o fluxo de eventos traz o último id
	resp, code = get("/?t="+s.token, "", "")
	if code != 200 {
		t.Fatalf("page: %d", code)
	}
	resp.Body.Close()
	resp, code = get("/api/events", s.token, "")
	if code != 200 {
		t.Fatalf("events: %d", code)
	}
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	resp.Body.Close()
	if strings.TrimSpace(line) != "data: 7" {
		t.Fatalf("evento inesperado: %q", line)
	}
	if !s.seen.Load() {
		t.Error("a conexão de eventos deveria marcar que a janela abriu")
	}
}
