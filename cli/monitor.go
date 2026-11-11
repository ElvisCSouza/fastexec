package cli

// monitor.go: janela de métricas ("quanto o FastExec economizou e por quê"). Mesmo desenho do assistente:
// servidor só em 127.0.0.1 (porta livre), token aleatório em toda chamada, conferência do Host, uma página HTML
// embutida; abre em janela própria do Edge/Chrome (--app). Os dados vêm do banco local de telemetria.

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"homelab/fastexec/internal/telemetry"
)

//go:embed monitor_ui.html
var monitorHTML string

const monitorUsage = `Usage: fastexec monitor [--no-browser]
  Opens the savings window: what FastExec saved you and why (a page served only on this computer, 127.0.0.1).
  --no-browser  print the address instead of opening it`

type monitorServer struct {
	token   string
	addr    string
	clients atomic.Int32 // páginas abertas (conexões SSE)
	seen    atomic.Bool  // alguma página já conectou
	srv     *http.Server

	// fontes dos dados (o banco local); os testes trocam por dados fixos para não tocar na telemetria real
	report func(period string) (telemetry.SavingsReport, error)
	lastID func() int64
}

func newMonitorServer() (*monitorServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	s := &monitorServer{token: hex.EncodeToString(b), addr: ln.Addr().String(),
		report: telemetry.BuildSavingsReport, lastID: telemetry.LastSavingID}
	s.srv = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *monitorServer) url() string { return "http://" + s.addr + "/?t=" + s.token }

func (s *monitorServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(rw, strings.Replace(monitorHTML, "__TOKEN__", s.token, 1))
	})
	mux.HandleFunc("/api/report", func(rw http.ResponseWriter, r *http.Request) {
		rep, err := s.report(r.URL.Query().Get("p"))
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Cache-Control", "no-store")
		writeJSON(rw, rep)
	})
	mux.HandleFunc("/api/events", s.events)
	return s.guard(mux)
}

func (s *monitorServer) guard(next http.Handler) http.Handler {
	port := s.addr[strings.LastIndex(s.addr, ":")+1:]
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr && r.Host != "localhost:"+port {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if tok != s.token {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// events manda o maior id de economia uma vez por segundo; a página recarrega o relatório quando ele muda.
func (s *monitorServer) events(rw http.ResponseWriter, r *http.Request) {
	fl, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-store")
	s.clients.Add(1)
	s.seen.Store(true)
	defer s.clients.Add(-1)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		fmt.Fprintf(rw, "data: %d\n\n", s.lastID())
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *monitorServer) close() { _ = s.srv.Close() }

// cmdMonitor: abre a janela e roda até ela ser fechada.
func cmdMonitor(args []string) {
	open := os.Getenv("FASTEXEC_NO_BROWSER") == ""
	for _, a := range args {
		switch a {
		case "--no-browser":
			open = false
		default:
			fmt.Println(monitorUsage)
			os.Exit(2)
		}
	}
	s, err := newMonitorServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start the savings window:", err)
		os.Exit(1)
	}
	defer s.close()
	fmt.Println("FastExec savings: " + s.url())
	if open {
		openBrowserSize(s.url(), 1080, 900)
	}
	// Sai quando a janela fecha (depois de ter conectado e ficado 5 s sem nenhuma página).
	idle := 0
	for {
		time.Sleep(time.Second)
		if s.seen.Load() && s.clients.Load() == 0 {
			if idle++; idle >= 5 {
				return
			}
		} else {
			idle = 0
		}
	}
}
