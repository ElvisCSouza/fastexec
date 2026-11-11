package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	builtExe  string
	buildErr  error
)

// realExe compila o fastexec uma vez para o smoke test MCP (o binário do teste não é o servidor).
func realExe(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fx-wizard-bin")
		if err != nil {
			buildErr = err
			return
		}
		name := "fastexec"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		builtExe = filepath.Join(dir, name)
		out, err := exec.Command("go", "build", "-o", builtExe, "homelab/fastexec/cmd/fastexec").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("cannot build fastexec: %v", buildErr)
	}
	return builtExe
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// sandbox isola HOME, dados e daemon: nada do usuário real é tocado. Devolve a pasta "casa" falsa.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("FASTEXEC_HOME", filepath.Join(home, ".fastexec"))
	t.Setenv("FASTEXEC_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("FASTEXEC_CONFIG", filepath.Join(home, "none.json"))
	t.Setenv("FASTEXEC_DAEMON_ADDR", freeAddr(t))
	// sem `claude` no PATH: o teste nunca roda a CLI real
	empty := t.TempDir()
	path := empty
	if runtime.GOOS == "windows" {
		path += string(os.PathListSeparator) + filepath.Join(os.Getenv("SystemRoot"), "System32")
	} else {
		path += string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	}
	t.Setenv("PATH", path)
	return home
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cursorFile(home string) string { return filepath.Join(home, ".cursor", "mcp.json") }

func readStr(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func item(w *wizard, id string) *wItem {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range w.items {
		if it.ID == id {
			c := *it
			return &c
		}
	}
	return nil
}

func TestInspectDetectsClientsAndOverlap(t *testing.T) {
	home := sandbox(t)
	writeFile(t, cursorFile(home), `{"mcpServers":{"desktop-commander":{"command":"npx","args":["-y","@wonderwhy-er/desktop-commander"]}}}`)
	w := newWizard()
	w.exe = filepath.Join(home, "bin", "fastexec.exe")
	w.Inspect()
	if it := item(w, "client:"+cursorFile(home)); it == nil || it.Status != wOK {
		t.Fatalf("cursor not detected: %+v", it)
	}
	if it := item(w, "compete:"+cursorFile(home)+":desktop-commander"); it == nil || it.Status != wWarn {
		t.Fatalf("overlap not flagged: %+v", it)
	}
}

func TestInspectChangesNothing(t *testing.T) {
	home := sandbox(t)
	body := `{"mcpServers":{}}`
	writeFile(t, cursorFile(home), body)
	w := newWizard()
	w.Inspect()
	if readStr(t, cursorFile(home)) != body {
		t.Fatal("Inspect modified a client file")
	}
	if _, err := os.Stat(filepath.Join(home, ".fastexec")); err == nil {
		t.Fatal("Inspect created the data folder")
	}
}

func TestInstallKeepsOtherEntriesBacksUpAndIsIdempotent(t *testing.T) {
	home := sandbox(t)
	orig := `{"theme":"dark","mcpServers":{"other":{"command":"x","args":[]}}}`
	writeFile(t, cursorFile(home), orig)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec.exe")
	w.opts.Exclusive = false
	w.Install()
	got := readStr(t, cursorFile(home))
	if !strings.Contains(got, `"theme"`) || !strings.Contains(got, `"other"`) || !strings.Contains(got, `"fastexec"`) {
		t.Fatalf("entries lost or missing: %s", got)
	}
	if readStr(t, cursorFile(home)+".fastexec-backup") != orig {
		t.Fatal("backup differs from the original")
	}
	before := got
	w2 := newWizard()
	w2.exe = w.exe
	w2.opts.Exclusive = false
	w2.Install()
	if readStr(t, cursorFile(home)) != before {
		t.Fatal("second install changed the file")
	}
	if it := item(w2, "reg:"+cursorFile(home)); it == nil || it.Detail != "already registered" {
		t.Fatalf("second install should report already registered: %+v", it)
	}
}

func TestInvalidJSONIsLeftAlone(t *testing.T) {
	home := sandbox(t)
	bad := `{"mcpServers": {broken`
	writeFile(t, cursorFile(home), bad)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec.exe")
	w.Inspect()
	if it := item(w, "client:"+cursorFile(home)); it == nil || it.Status != wFail {
		t.Fatalf("invalid JSON should fail the check: %+v", it)
	}
	w.Install()
	if readStr(t, cursorFile(home)) != bad {
		t.Fatal("invalid file was modified")
	}
}

func TestSkippedClientIsNotTouched(t *testing.T) {
	home := sandbox(t)
	body := `{"mcpServers":{}}`
	writeFile(t, cursorFile(home), body)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec.exe")
	w.opts = wOptions{Skip: []string{cursorFile(home)}}
	w.Install()
	if readStr(t, cursorFile(home)) != body {
		t.Fatal("skipped client was modified")
	}
}

func TestInstallCopiesExecutable(t *testing.T) {
	home := sandbox(t)
	src := filepath.Join(home, "dl", "fastexec.exe")
	writeFile(t, src, "binary-v1")
	writeFile(t, cursorFile(home), `{"mcpServers":{}}`)
	w := newWizard()
	w.exe = src
	dest := filepath.Join(home, "Programs", "FastExec")
	w.opts = wOptions{InstallDir: dest}
	w.Install()
	if w.exe != filepath.Join(dest, "fastexec.exe") || readStr(t, w.exe) != "binary-v1" {
		t.Fatalf("not copied: exe=%s", w.exe)
	}
	if !strings.Contains(readStr(t, cursorFile(home)), strings.ReplaceAll(w.exe, `\`, `\\`)) {
		t.Fatal("client does not point to the copied executable")
	}
	// segunda cópia com o destino existente (como numa atualização)
	writeFile(t, src, "binary-v2")
	w2 := newWizard()
	w2.exe = src
	w2.opts = wOptions{InstallDir: dest}
	w2.Install()
	if readStr(t, w2.exe) != "binary-v2" {
		t.Fatal("update did not replace the executable")
	}
}

func TestExclusiveModeDeniesAndUndoRestores(t *testing.T) {
	home := sandbox(t)
	writeFile(t, claudeCodeFile(), `{"mcpServers":{}}`) // Claude Code "instalado" (sem a CLI no PATH)
	writeFile(t, claudeCodeSettings(), `{"permissions":{"deny":["WebFetch"]}}`)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec.exe")
	w.opts.Exclusive = true
	w.Install()
	for _, tool := range append([]string{"WebFetch"}, nativeTools...) {
		if !denied(claudeCodeSettings(), tool) {
			t.Fatalf("%s not denied", tool)
		}
	}
	w.Undo()
	if !denied(claudeCodeSettings(), "WebFetch") {
		t.Fatal("undo removed a rule that was not ours")
	}
	for _, tool := range nativeTools {
		if denied(claudeCodeSettings(), tool) {
			t.Fatalf("%s still denied after undo", tool)
		}
	}
	if strings.Contains(readStr(t, claudeCodeFile()), `"fastexec"`) {
		// o registro no Claude Code sem CLI vai para ~/.claude.json: o undo precisa limpar também
		t.Fatal("undo left fastexec registered in ~/.claude.json")
	}
}

func TestUndoRemovesOnlyFastexec(t *testing.T) {
	home := sandbox(t)
	writeFile(t, cursorFile(home), `{"mcpServers":{"other":{"command":"x","args":[]}}}`)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec.exe")
	w.opts.Exclusive = false
	w.Install()
	w.Undo()
	got := readStr(t, cursorFile(home))
	if strings.Contains(got, `"fastexec"`) || !strings.Contains(got, `"other"`) {
		t.Fatalf("undo result wrong: %s", got)
	}
}

func TestVerifyReadyWithRealMCPServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real binary")
	}
	exe := realExe(t) // antes do sandbox: o PATH isolado não tem o go
	home := sandbox(t)
	t.Cleanup(func() { _ = exec.Command(exe, "daemon", "stop").Run() })
	writeFile(t, cursorFile(home), `{"mcpServers":{}}`)
	w := newWizard()
	w.exe = exe
	w.opts.Exclusive = false
	w.Install()
	w.Verify()
	st := w.snapshot()
	for _, it := range st.Items {
		if it.Required && it.Status != wOK {
			t.Fatalf("required check not green: %+v", it)
		}
	}
	if !st.Ready {
		t.Fatalf("not ready; log: %+v", st.Log)
	}
}

func TestVerifyNotReadyWhenNothingRegistered(t *testing.T) {
	home := sandbox(t)
	w := newWizard()
	w.exe = filepath.Join(home, "missing.exe")
	w.Verify()
	if w.snapshot().Ready {
		t.Fatal("ready with no executable and no client")
	}
}

func TestWebGuardAndEvents(t *testing.T) {
	sandbox(t)
	w := newWizard()
	s, ln, err := newWizardServer(w)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.handler()}
	go srv.Serve(ln)
	defer srv.Close()
	base := "http://" + s.addr

	get := func(path, tok, host string) int {
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
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/api/state", "", ""); c != 403 {
		t.Fatalf("no token: %d", c)
	}
	if c := get("/api/state", "wrong", ""); c != 403 {
		t.Fatalf("wrong token: %d", c)
	}
	if c := get("/api/state", s.token, "evil.example:80"); c != 403 {
		t.Fatalf("foreign host: %d", c)
	}
	if c := get("/api/state", s.token, ""); c != 200 {
		t.Fatalf("valid: %d", c)
	}
	if c := get("/?t="+s.token, "", ""); c != 200 {
		t.Fatalf("page with token: %d", c)
	}
	if c := get("/", "", ""); c != 403 {
		t.Fatalf("page without token: %d", c)
	}

	// SSE: primeira mensagem traz o estado; depois de Inspect chega outra com itens
	resp, err := http.Get(base + "/api/events?t=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	go func() { time.Sleep(100 * time.Millisecond); w.Inspect() }()
	deadline := time.After(10 * time.Second)
	got := make(chan bool, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				var st wState
				if json.Unmarshal([]byte(line[6:]), &st) == nil && len(st.Items) > 0 {
					got <- true
					return
				}
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("no SSE event with items")
	}
}

func TestWebRunRejectsGet(t *testing.T) {
	sandbox(t)
	s, ln, _ := newWizardServer(newWizard())
	srv := &http.Server{Handler: s.handler()}
	go srv.Serve(ln)
	defer srv.Close()
	req, _ := http.NewRequest("GET", "http://"+s.addr+"/api/run/install", nil)
	req.Header.Set("X-Token", s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET must not start a step: %d", resp.StatusCode)
	}
	_ = httptest.NewRecorder
}

// TestInstallerEndToEnd roda o instalador como o usuário o recebe: o mesmo binário com nome fastexec-setup, sem
// argumentos, numa pasta Downloads. Dirige a página pela API HTTP: instalar, conferir tudo verde, desfazer.
func TestInstallerEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real binary")
	}
	built := realExe(t) // antes do sandbox: o PATH isolado não tem o go
	home := sandbox(t)
	t.Setenv("FASTEXEC_NO_BROWSER", "1")
	writeFile(t, cursorFile(home), `{"theme":"dark","mcpServers":{"other":{"command":"x","args":[]}}}`)

	setupName := "fastexec-setup"
	if runtime.GOOS == "windows" {
		setupName += ".exe"
	}
	setup := filepath.Join(home, "Downloads", setupName)
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(setup), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setup, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(setup) // sem argumentos: o nome faz abrir o assistente
	cmd.Env = os.Environ()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.Contains(line, "http://127.0.0.1:") {
		t.Fatalf("installer did not print its address: %q %v", line, err)
	}
	url := strings.TrimSpace(line[strings.Index(line, "http://"):])
	base := url[:strings.Index(url, "/?t=")]
	tok := url[strings.Index(url, "?t=")+3:]

	call := func(method, path string, v any) {
		body := strings.NewReader("")
		if v != nil {
			b, _ := json.Marshal(v)
			body = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, base+path, body)
		req.Header.Set("X-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
		}
	}
	state := func() wState {
		req, _ := http.NewRequest("GET", base+"/api/state", nil)
		req.Header.Set("X-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var st wState
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	waitIdle := func(phase string) wState {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			st := state()
			if !st.Busy && (phase == "" || st.Phase == phase) {
				return st
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("timed out waiting for", phase)
		return wState{}
	}

	st := waitIdle("inspect")
	dest := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "FastExec", installedName())
	if runtime.GOOS != "windows" {
		dest = filepath.Join(home, ".local", "bin", installedName())
	}
	if st.Opts.InstallDir == "" {
		t.Fatal("installer did not suggest an install folder")
	}
	t.Cleanup(func() { _ = exec.Command(dest, "daemon", "stop").Run() })

	call("POST", "/api/opts", wOptions{Exclusive: false, InstallDir: filepath.Dir(dest)})
	call("POST", "/api/run/install", nil)
	st = waitIdle("verify")
	if !st.Ready {
		for _, it := range st.Items {
			t.Logf("%-8s %-5v %s: %s", it.Status, it.Required, it.Title, it.Detail)
		}
		t.Fatal("installer did not reach Ready")
	}
	if !fileExists(dest) {
		t.Fatalf("executable not installed at %s", dest)
	}
	cfg := readStr(t, cursorFile(home))
	if !strings.Contains(cfg, strings.ReplaceAll(dest, `\`, `\\`)) && !strings.Contains(cfg, dest) {
		t.Fatalf("client does not point to %s: %s", dest, cfg)
	}
	if strings.Contains(strings.ToLower(cfg), "setup") {
		t.Fatal("the installer itself was registered as the server")
	}
	if !strings.Contains(cfg, `"other"`) || !strings.Contains(cfg, `"theme"`) {
		t.Fatal("existing entries lost")
	}

	call("POST", "/api/run/undo", nil)
	waitIdle("undo")
	if strings.Contains(readStr(t, cursorFile(home)), `"fastexec"`) {
		t.Fatal("undo left FastExec registered")
	}
	call("POST", "/api/quit", nil)
}

func TestLooksLikeSetup(t *testing.T) {
	for p, want := range map[string]bool{
		`C:\x\fastexec-setup.exe`: true, `/a/FastExec-Setup`: true, `C:\x\fastexec.exe`: false, `/a/fastexec`: false,
	} {
		if got := looksLikeSetup(p); got != want {
			t.Errorf("looksLikeSetup(%q)=%v", p, got)
		}
	}
}

func TestInstallRefusesToRegisterTheInstaller(t *testing.T) {
	home := sandbox(t)
	writeFile(t, cursorFile(home), `{"mcpServers":{}}`)
	w := newWizard()
	w.exe = filepath.Join(home, "fastexec-setup.exe")
	w.opts = wOptions{} // sem pasta de instalação
	w.Install()
	if it := item(w, "copy"); it == nil || it.Status != wFail {
		t.Fatalf("expected a failed copy item: %+v", it)
	}
	if strings.Contains(readStr(t, cursorFile(home)), `"fastexec"`) {
		t.Fatal("installer name was registered")
	}
}
