package cli

// wizard_web.go: servidor local do assistente. Só escuta em 127.0.0.1 (porta livre), exige um token aleatório em
// toda chamada (inclusive na página, que vem na URL aberta pelo navegador) e confere o Host (contra DNS rebinding).
// A página é um único HTML embutido no binário.

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed wizard_ui.html
var wizardHTML string

const wizardUsage = `Usage: fastexec setup --ui [--no-browser]
  Opens the installer in your browser (a page served only on this computer, 127.0.0.1).
  --no-browser  print the address instead of opening it`

// wizardServer liga o motor a HTTP.
type wizardServer struct {
	w     *wizard
	token string
	addr  string
	done  chan struct{}
}

func newWizardServer(w *wizard) (*wizardServer, net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, nil, err
	}
	return &wizardServer{w: w, token: hex.EncodeToString(b), addr: ln.Addr().String(), done: make(chan struct{})}, ln, nil
}

func (s *wizardServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(rw, strings.Replace(wizardHTML, "__TOKEN__", s.token, 1))
	})
	mux.HandleFunc("/api/state", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, s.w.snapshot())
	})
	mux.HandleFunc("/api/events", s.events)
	mux.HandleFunc("/api/opts", func(rw http.ResponseWriter, r *http.Request) {
		var o wOptions
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<16)).Decode(&o) != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		s.w.setOpts(o)
		writeJSON(rw, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/run/", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(rw, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var run func()
		switch strings.TrimPrefix(r.URL.Path, "/api/run/") {
		case "inspect":
			run = s.w.Inspect
		case "install":
			run = func() { s.w.Install(); s.w.Verify() }
		case "verify":
			run = s.w.Verify
		case "undo":
			run = s.w.Undo
		default:
			http.NotFound(rw, r)
			return
		}
		if s.w.snapshot().Busy {
			http.Error(rw, "another step is running", http.StatusConflict)
			return
		}
		go run()
		writeJSON(rw, map[string]bool{"started": true})
	})
	mux.HandleFunc("/api/quit", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); close(s.done) }()
	})
	return s.guard(mux)
}

// guard: Host tem de ser o endereço local; a página e as chamadas /api/ levam o token (?t= ou X-Token).
func (s *wizardServer) guard(next http.Handler) http.Handler {
	port := s.addr[strings.LastIndex(s.addr, ":")+1:]
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr && r.Host != "localhost:"+port {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t") // a página e o EventSource não enviam cabeçalhos
		}
		if tok != s.token {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// events envia o estado inteiro a cada mudança (Server-Sent Events).
func (s *wizardServer) events(rw http.ResponseWriter, r *http.Request) {
	fl, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-store")
	ch, cancel := s.w.subscribe()
	defer cancel()
	send := func() {
		b, _ := json.Marshal(s.w.snapshot())
		fmt.Fprintf(rw, "data: %s\n\n", b)
		fl.Flush()
	}
	send()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-ch:
			time.Sleep(40 * time.Millisecond) // junta rajadas de eventos numa só mensagem
			send()
		case <-keep.C:
			fmt.Fprint(rw, ": keep-alive\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

// cmdSetupUI roda o assistente até o usuário fechar (botão Close) ou o processo ser interrompido.
func cmdSetupUI(args []string) {
	open := os.Getenv("FASTEXEC_NO_BROWSER") == ""
	for _, a := range args {
		switch a {
		case "--no-browser":
			open = false
		case "--ui":
		default:
			fmt.Println(wizardUsage)
			os.Exit(2)
		}
	}
	w := newWizard()
	w.suggestInstallDir()
	s, ln, err := newWizardServer(w)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start the installer page:", err)
		os.Exit(1)
	}
	url := "http://" + s.addr + "/?t=" + s.token
	fmt.Println("FastExec installer: " + url)
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go w.Inspect() // a página já abre com os checks de leitura rodando
	if open {
		openBrowser(url)
	}
	<-s.done
	_ = srv.Close()
}

// appBrowser procura um Edge/Chrome/Chromium para abrir a página em janela própria (--app: sem abas nem barra de
// endereço). Vazio quando não acha nenhum.
func appBrowser() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
		}
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
	default:
		for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

func openBrowser(url string) { openBrowserSize(url, 1000, 900) }

func openBrowserSize(url string, w, h int) {
	if b := appBrowser(); b != "" {
		if err := exec.Command(b, "--app="+url, fmt.Sprintf("--window-size=%d,%d", w, h)).Start(); err == nil {
			return
		}
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
