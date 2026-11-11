package cli

// wizard.go: motor do assistente de instalação (`fastexec setup --ui`). Não sabe nada de HTTP: mantém uma lista de
// checks com estado, um log e assinantes de eventos; a interface web (wizard_web.go) só observa e dispara fases.
// Fases: Inspect (só lê), Install (altera, com backup), Verify (confere tudo de novo, inclusive um MCP de verdade),
// Undo (desfaz o que o Install fez). "Ready" só quando todo check obrigatório está verde.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/jsonedit"
)

type wStatus string

const (
	wPending wStatus = "pending"
	wRunning wStatus = "running"
	wOK      wStatus = "ok"
	wWarn    wStatus = "warn"
	wFail    wStatus = "fail"
	wSkip    wStatus = "skip"
)

// wItem é uma linha da lista de checks.
type wItem struct {
	ID       string  `json:"id"`
	Group    string  `json:"group"`
	Title    string  `json:"title"`
	Status   wStatus `json:"status"`
	Detail   string  `json:"detail"`
	Fix      string  `json:"fix,omitempty"`
	Required bool    `json:"required"`
}

type wLog struct {
	Seq   int    `json:"seq"`
	Time  string `json:"time"`
	Level string `json:"level"` // info, warn, error
	Text  string `json:"text"`
}

// wOptions são as escolhas do usuário na primeira tela.
type wOptions struct {
	Exclusive  bool     `json:"exclusive"`  // negar as ferramentas nativas do Claude Code
	InstallDir string   `json:"installDir"` // vazio = usar o executável onde está
	Skip       []string `json:"skip"`       // caminhos de config de cliente que o usuário desmarcou
}

// wState é o retrato completo enviado à interface.
type wState struct {
	Phase   string    `json:"phase"` // idle, inspect, install, verify, undo
	Busy    bool      `json:"busy"`
	Ready   bool      `json:"ready"`
	Exe     string    `json:"exe"`
	Version string    `json:"version"`
	Items   []*wItem  `json:"items"`
	Log     []wLog    `json:"log"`
	Opts    wOptions  `json:"opts"`
	Clients []wClient `json:"clients"`
}

type wClient struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// nativeTools são as ferramentas do Claude Code que o modo exclusivo nega: o que o FastExec cobre.
// Inclui o servidor embutido `mcp__terminal` das sessões Code do Claude Desktop (run_in_terminal etc.): sem negá-lo o
// modelo contorna o bloqueio do Bash pelo terminal do painel.
var nativeTools = []string{"Bash", "PowerShell", "Monitor", "Read", "Edit", "Write", "Glob", "Grep", "NotebookEdit", "mcp__terminal"}

type wizard struct {
	mu    sync.Mutex
	items []*wItem
	logs  []wLog
	subs  map[chan struct{}]struct{}
	opts  wOptions
	exe   string // caminho que será registrado nos clientes
	phase string
	busy  bool
	ready bool
	// testes: executável usado no smoke test MCP e no passo de cópia
	smokeExe string
}

func newWizard() *wizard {
	w := &wizard{subs: map[chan struct{}]struct{}{}, phase: "idle", opts: wOptions{Exclusive: true}}
	w.exe = daemon.Self().Exe
	return w
}

// suggestInstallDir: rodando como instalador ou de uma pasta passageira, já sugere uma pasta permanente.
func (w *wizard) suggestInstallDir() {
	if looksLikeSetup(w.exe) || unstablePath(w.exe) != "" {
		w.opts.InstallDir = defaultInstallDir()
	}
}

// looksLikeSetup: o executável tem nome de instalador (fastexec-setup.exe). Esse nome nunca deve ser registrado
// nos clientes, porque sem argumentos ele abre o assistente em vez de servir MCP.
func looksLikeSetup(exe string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(exe)), "setup")
}

// installedName é o nome do executável depois de instalado.
func installedName() string {
	if runtime.GOOS == "windows" {
		return "fastexec.exe"
	}
	return "fastexec"
}

// defaultInstallDir: pasta permanente por usuário, sem precisar de administrador.
func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		if l := os.Getenv("LOCALAPPDATA"); l != "" {
			return filepath.Join(l, "Programs", "FastExec")
		}
	}
	return filepath.Join(homeDir(), ".local", "bin")
}

// --- estado e eventos ---

func (w *wizard) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() { w.mu.Lock(); delete(w.subs, ch); w.mu.Unlock() }
}

func (w *wizard) notifyLocked() {
	for ch := range w.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (w *wizard) logf(level, f string, a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logs = append(w.logs, wLog{Seq: len(w.logs) + 1, Time: time.Now().Format("15:04:05"), Level: level, Text: fmt.Sprintf(f, a...)})
	w.notifyLocked()
}

// set cria ou atualiza o item id.
func (w *wizard) set(id, group, title string, st wStatus, detail, fix string, required bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var it *wItem
	for _, x := range w.items {
		if x.ID == id {
			it = x
		}
	}
	if it == nil {
		it = &wItem{ID: id}
		w.items = append(w.items, it)
	}
	it.Group, it.Title, it.Status, it.Detail, it.Fix, it.Required = group, title, st, detail, fix, required
	w.notifyLocked()
}

func (w *wizard) clearGroup(group string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.items[:0]
	for _, x := range w.items {
		if x.Group != group {
			kept = append(kept, x)
		}
	}
	w.items = kept
}

func (w *wizard) snapshot() wState {
	w.mu.Lock()
	defer w.mu.Unlock()
	items := make([]*wItem, len(w.items))
	for i, x := range w.items {
		c := *x
		items[i] = &c
	}
	var cl []wClient
	for _, c := range clientFiles() {
		cl = append(cl, wClient{c.Client, c.Path})
	}
	return wState{Phase: w.phase, Busy: w.busy, Ready: w.ready, Exe: w.exe, Version: versionLine(), Items: items,
		Log: append([]wLog(nil), w.logs...), Opts: w.opts, Clients: cl}
}

func (w *wizard) setOpts(o wOptions) {
	w.mu.Lock()
	w.opts = o
	w.notifyLocked()
	w.mu.Unlock()
}

// begin marca a fase; false se já há uma rodando.
func (w *wizard) begin(phase string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.busy {
		return false
	}
	w.busy, w.phase, w.ready = true, phase, false
	w.notifyLocked()
	return true
}

func (w *wizard) end() {
	w.mu.Lock()
	w.busy = false
	w.notifyLocked()
	w.mu.Unlock()
}

func (w *wizard) skipped(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.opts.Skip {
		if samePath(s, path) {
			return true
		}
	}
	return false
}

// --- fase 1: Inspect (somente leitura) ---

func (w *wizard) Inspect() {
	if !w.begin("inspect") {
		return
	}
	defer w.end()
	w.clearGroup("Inspect")
	w.logf("info", "Inspecting this machine (nothing is changed in this step)")

	g := "Inspect"
	if w.exe == "" {
		w.set("exe", g, "Executable", wFail, "cannot find this executable's path", "Run the installer from a normal folder.", true)
	} else if bad := unstablePath(w.exe); bad != "" {
		w.set("exe", g, "Executable", wWarn, w.exe, "Running from "+bad+": the clients keep the path you register. Choose an install folder below so it is copied somewhere permanent.", false)
	} else {
		w.set("exe", g, "Executable", wOK, w.exe, "", true)
	}
	w.set("os", g, "System", wOK, runtime.GOOS+"/"+runtime.GOARCH, "", false)

	home := config.Home()
	if st, err := os.Stat(home); err == nil && st.IsDir() {
		w.set("home", g, "Data folder", wOK, home+" (exists)", "", true)
	} else {
		w.set("home", g, "Data folder", wOK, home+" (will be created)", "", true)
	}

	cf := clientFiles()
	if len(cf) == 0 {
		w.set("clients", g, "AI clients", wWarn, "no supported AI client found", "Install Claude Desktop, Claude Code, Cursor or Antigravity first, then run this again.", false)
	}
	for _, c := range cf {
		id := "client:" + c.Path
		m, names, err := servers(c.Path)
		if err != nil {
			w.set(id, g, c.Client, wFail, "cannot read "+c.Path+": "+err.Error(), "Fix or delete the file; FastExec never edits a file that is not valid JSON.", false)
			w.logf("error", "%s: %v", c.Path, err)
			continue
		}
		detail := c.Path
		if e, ok := m[serverName]; ok && samePath(e.Command, w.exe) {
			detail += " (already registered)"
		}
		w.set(id, g, c.Client, wOK, detail, "", false)
		for _, n := range names {
			if isCompeting(n, m[n]) {
				w.set("compete:"+c.Path+":"+n, g, c.Client+": overlapping server "+n, wWarn,
					"also runs commands or edits files", "Models tend to pick it and rarely call FastExec. Remove it from "+c.Path+" for full FastExec mode.", false)
			}
		}
	}
	if _, err := exec.LookPath("claude"); err == nil || fileExists(claudeCodeFile()) {
		w.set("claudecode", g, "Claude Code", wOK, "found", "", false)
	} else {
		w.set("claudecode", g, "Claude Code", wSkip, "not installed (skipped)", "", false)
	}
	w.logf("info", "Inspection finished")
}

func fileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// unstablePath: pasta onde um executável não deveria ficar registrado (devolve o nome para a mensagem).
func unstablePath(p string) string {
	l := strings.ToLower(filepath.ToSlash(p))
	for _, bad := range []string{"/downloads/", "/temp/", "/tmp/", "/appdata/local/temp/"} {
		if strings.Contains(l, bad) {
			return strings.Trim(bad, "/")
		}
	}
	return ""
}

// --- fase 2: Install ---

func (w *wizard) Install() {
	if !w.begin("install") {
		return
	}
	defer w.end()
	w.clearGroup("Install")
	g := "Install"
	w.logf("info", "Installing")

	// 1. copiar o executável para uma pasta permanente (opcional)
	w.mu.Lock()
	dir := strings.TrimSpace(w.opts.InstallDir)
	w.mu.Unlock()
	if dir == "" && looksLikeSetup(w.exe) {
		w.set("copy", g, "Install location", wFail, "the installer cannot be registered as the server", "Choose an install folder: FastExec is copied there as "+installedName()+".", true)
		return
	}
	if dir != "" && w.exe != "" {
		dest := filepath.Join(dir, installedName())
		if samePath(dest, w.exe) {
			w.set("copy", g, "Install location", wOK, dest+" (already there)", "", true)
		} else if err := copyExe(w.exe, dest); err != nil {
			w.set("copy", g, "Install location", wFail, err.Error(), "Pick another folder you can write to.", true)
			w.logf("error", "copy: %v", err)
			return
		} else {
			w.exe = dest
			w.set("copy", g, "Install location", wOK, "copied to "+dest, "", true)
			w.logf("info", "Copied the executable to %s", dest)
		}
	}
	if w.exe == "" {
		w.set("copy", g, "Install location", wFail, "executable path unknown", "", true)
		return
	}

	// 2. pasta de dados
	if err := os.MkdirAll(config.Home(), 0o755); err != nil {
		w.set("datadir", g, "Data folder", wFail, err.Error(), "Check permissions on "+config.Home(), true)
		w.logf("error", "%v", err)
		return
	}
	w.set("datadir", g, "Data folder", wOK, config.Home(), "", true)

	// 3. registrar nos clientes
	entry := serverEntry{Command: w.exe, Args: []string{}}
	for _, c := range clientFiles() {
		id := "reg:" + c.Path
		if w.skipped(c.Path) {
			w.set(id, g, "Register in "+c.Client, wSkip, "skipped by you", "", false)
			continue
		}
		m, _, err := servers(c.Path)
		if err != nil {
			w.set(id, g, "Register in "+c.Client, wFail, err.Error(), "The file was left untouched.", false)
			w.logf("error", "%s: %v", c.Path, err)
			continue
		}
		if e, ok := m[serverName]; ok && samePath(e.Command, w.exe) {
			w.set(id, g, "Register in "+c.Client, wOK, "already registered", "", false)
			continue
		}
		if err := editJSON(c.Path, entry, "mcpServers", serverName); err != nil {
			w.set(id, g, "Register in "+c.Client, wFail, err.Error(), "", false)
			w.logf("error", "%s: %v", c.Path, err)
			continue
		}
		w.set(id, g, "Register in "+c.Client, wOK, "registered (backup: "+filepath.Base(c.Path)+".fastexec-backup)", "", false)
		w.logf("info", "Registered in %s", c.Path)
	}
	setupClaudeCodeQuiet(w, w.exe, func(_, done string) string { return done })

	// 4. modo exclusivo
	if w.opts.Exclusive {
		if _, found := claudeCodePresent(); found {
			for _, tool := range nativeTools {
				id := "deny:" + tool
				if denied(claudeCodeSettings(), tool) {
					w.set(id, g, "Claude Code: deny built-in "+tool, wOK, "already denied", "", false)
					continue
				}
				if err := addDeny(claudeCodeSettings(), tool); err != nil {
					w.set(id, g, "Claude Code: deny built-in "+tool, wFail, err.Error(), "", false)
					w.logf("error", "deny %s: %v", tool, err)
					continue
				}
				w.set(id, g, "Claude Code: deny built-in "+tool, wOK, "denied in "+claudeCodeSettings(), "", false)
			}
		}
	}
	w.logf("info", "Install finished; run the verification")
}

func claudeCodePresent() (string, bool) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, true
	}
	if fileExists(claudeCodeFile()) {
		return claudeCodeFile(), true
	}
	return "", false
}

// setupClaudeCodeQuiet registra no Claude Code reaproveitando setupClaudeCode, que imprime no stdout; aqui o
// resultado vira item. Se o Claude Code não existe, não cria item.
func setupClaudeCodeQuiet(w *wizard, exe string, verb func(string, string) string) (changed, found bool) {
	if _, ok := claudeCodePresent(); !ok {
		return false, false
	}
	id, g := "reg:claudecode", "Install"
	if w.skipped("claudecode") {
		w.set(id, g, "Register in Claude Code", wSkip, "skipped by you", "", false)
		return false, true
	}
	old := os.Stdout
	r, wr, _ := os.Pipe()
	os.Stdout = wr
	changed, found = setupClaudeCode(exe, false, verb)
	wr.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "failed") || strings.Contains(msg, "Claude Code:") && strings.Contains(msg, "could not") {
		w.set(id, g, "Register in Claude Code", wFail, msg, "Run `claude mcp add -s user fastexec -- <path>` by hand.", false)
		w.logf("error", "%s", msg)
	} else {
		w.set(id, g, "Register in Claude Code", wOK, strings.TrimPrefix(msg, "  Claude Code: "), "", false)
		w.logf("info", "%s", msg)
	}
	return changed, found
}

// copyExe copia src para dest: arquivo temporário + rename. No Windows um exe em uso não se sobrescreve, mas se
// renomeia: o antigo vai para <dest>.old (apagado na próxima vez que couber).
func copyExe(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Remove(dest + ".old")
	if fileExists(dest) {
		if err := os.Rename(dest, dest+".old"); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("cannot replace %s (is it running?): %w", dest, err)
		}
	}
	return os.Rename(tmp, dest)
}

// --- fase 3: Verify ---

func (w *wizard) Verify() {
	if !w.begin("verify") {
		return
	}
	defer w.end()
	w.clearGroup("Verify")
	g := "Verify"
	w.logf("info", "Verifying the installation")

	exe := w.exe
	if exe == "" || !fileExists(exe) {
		w.set("v-exe", g, "Executable exists", wFail, exe, "Run the Install step again.", true)
		return
	}
	w.set("v-exe", g, "Executable exists", wOK, exe, "", true)

	registered := 0
	for _, c := range clientFiles() {
		if w.skipped(c.Path) {
			continue
		}
		id := "v-reg:" + c.Path
		m, _, err := servers(c.Path)
		switch {
		case err != nil:
			w.set(id, g, c.Client+" config is valid JSON", wFail, err.Error(), "", false)
		case !samePath(m[serverName].Command, exe):
			w.set(id, g, c.Client+" points to this build", wFail, "FastExec is not registered here", "Run the Install step again.", false)
		default:
			registered++
			w.set(id, g, c.Client+" points to this build", wOK, c.Path, "", false)
		}
	}
	if cc, ok := claudeCodePresent(); ok && !w.skipped("claudecode") {
		ccOK := false
		if fileExists(claudeCodeFile()) {
			if m, _, err := servers(claudeCodeFile()); err == nil && samePath(m[serverName].Command, exe) {
				ccOK = true
			}
		}
		if ccOK {
			registered++
			w.set("v-regcc", g, "Claude Code points to this build", wOK, cc, "", false)
		} else {
			w.set("v-regcc", g, "Claude Code points to this build", wFail, "FastExec is not registered here", "Run the Install step again.", false)
		}
	}
	if registered == 0 {
		w.set("v-any", g, "At least one client uses FastExec", wFail, "none registered", "Install a supported AI client and run the installer again.", true)
	} else {
		w.set("v-any", g, "At least one client uses FastExec", wOK, fmt.Sprintf("%d client(s)", registered), "", true)
	}

	if w.opts.Exclusive {
		if _, ok := claudeCodePresent(); ok {
			var missing []string
			for _, t := range nativeTools {
				if !denied(claudeCodeSettings(), t) {
					missing = append(missing, t)
				}
			}
			if len(missing) == 0 {
				w.set("v-deny", g, "Claude Code built-in tools denied", wOK, strings.Join(nativeTools, ", "), "", false)
			} else {
				w.set("v-deny", g, "Claude Code built-in tools denied", wFail, "still allowed: "+strings.Join(missing, ", "), "Run the Install step again.", false)
			}
		}
	}

	// teste de verdade: sobe o executável como um cliente MCP faria
	w.set("v-mcp", g, "FastExec answers as an MCP server", wRunning, "starting it like a client would...", "", true)
	exeForSmoke := exe
	if w.smokeExe != "" {
		exeForSmoke = w.smokeExe
	}
	tools, err := smokeMCP(exeForSmoke)
	if err != nil {
		w.set("v-mcp", g, "FastExec answers as an MCP server", wFail, err.Error(), "Run `fastexec doctor` in a terminal for details.", true)
		w.logf("error", "MCP smoke test: %v", err)
	} else {
		w.set("v-mcp", g, "FastExec answers as an MCP server", wOK, fmt.Sprintf("%d tools listed", len(tools)), "", true)
		w.logf("info", "MCP initialize and tools/list OK: %s", strings.Join(tools, ", "))
	}

	w.mu.Lock()
	ready := true
	for _, it := range w.items {
		if it.Required && it.Status != wOK {
			ready = false
		}
	}
	w.ready = ready
	w.notifyLocked()
	w.mu.Unlock()
	if ready {
		w.logf("info", "Everything required is green: FastExec is ready. Restart your AI clients.")
	} else {
		w.logf("warn", "Some required checks are not green yet")
	}
}

// smokeMCP sobe exe como servidor MCP stdio, faz initialize, tools/list e um exec de eco, e devolve os nomes das tools.
func smokeMCP(exe string) ([]string, error) {
	cmd := exec.Command(exe)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %s: %w", exe, err)
	}
	defer func() { in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	resp := make(chan map[string]json.RawMessage, 8)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]json.RawMessage
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				resp <- m
			}
		}
		close(resp)
	}()
	send := func(v any) { b, _ := json.Marshal(v); fmt.Fprintf(in, "%s\n", b) }
	wait := func(id int) (map[string]json.RawMessage, error) {
		timeout := time.After(30 * time.Second)
		for {
			select {
			case m, ok := <-resp:
				if !ok {
					return nil, fmt.Errorf("the process exited before answering request %d", id)
				}
				var got int
				if json.Unmarshal(m["id"], &got) == nil && got == id {
					if e, bad := m["error"]; bad {
						return nil, fmt.Errorf("request %d failed: %s", id, e)
					}
					return m, nil
				}
			case <-timeout:
				return nil, fmt.Errorf("no answer to request %d within 30 s", id)
			}
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fastexec-installer", "version": "1"}}})
	if _, err := wait(1); err != nil {
		return nil, err
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	m, err := wait(2)
	if err != nil {
		return nil, err
	}
	var res struct{ Tools []struct{ Name string } }
	if err := json.Unmarshal(m["result"], &res); err != nil || len(res.Tools) == 0 {
		return nil, fmt.Errorf("tools/list returned no tools")
	}
	names := make([]string, len(res.Tools))
	for i, t := range res.Tools {
		names[i] = t.Name
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
		"name": "exec", "arguments": map[string]any{"host": "local", "argv": echoArgv()}}})
	m, err = wait(3)
	if err != nil {
		return names, fmt.Errorf("exec test: %w", err)
	}
	if !strings.Contains(string(m["result"]), "fastexec-ok") {
		return names, fmt.Errorf("exec test did not return the expected output: %.200s", m["result"])
	}
	return names, nil
}

func echoArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", "fastexec-ok"}
	}
	return []string{"echo", "fastexec-ok"}
}

// --- fase 4: Undo ---

func (w *wizard) Undo() {
	if !w.begin("undo") {
		return
	}
	defer w.end()
	w.clearGroup("Undo")
	g := "Undo"
	w.logf("info", "Removing FastExec from your AI clients")
	for _, c := range clientFiles() {
		id := "undo:" + c.Path
		data, err := os.ReadFile(c.Path)
		if err != nil {
			w.set(id, g, c.Client, wFail, err.Error(), "", false)
			continue
		}
		if _, ok, _ := jsonedit.Get(data, "mcpServers", serverName); !ok {
			w.set(id, g, c.Client, wOK, "was not registered", "", false)
			continue
		}
		out, err := jsonedit.Set(data, nil, "mcpServers", serverName)
		if err != nil {
			w.set(id, g, c.Client, wFail, err.Error(), "", false)
			continue
		}
		if err := os.WriteFile(c.Path+".fastexec-backup", data, 0o600); err == nil {
			tmp := c.Path + ".fastexec-tmp"
			if err = os.WriteFile(tmp, out, 0o600); err == nil {
				err = os.Rename(tmp, c.Path)
			}
		}
		if err != nil {
			w.set(id, g, c.Client, wFail, err.Error(), "", false)
			continue
		}
		w.set(id, g, c.Client, wOK, "removed", "", false)
		w.logf("info", "Removed from %s", c.Path)
	}
	if cli, err := exec.LookPath("claude"); err == nil {
		_ = exec.Command(cli, "mcp", "remove", serverName, "-s", "user").Run()
		w.set("undo:claudecode", g, "Claude Code", wOK, "removed", "", false)
	}
	if data, err := os.ReadFile(claudeCodeFile()); err == nil { // registro feito sem a CLI, direto no arquivo
		if _, ok, _ := jsonedit.Get(data, "mcpServers", serverName); ok {
			if err := editJSON(claudeCodeFile(), nil, "mcpServers", serverName); err != nil {
				w.set("undo:claudecode-file", g, "Claude Code (~/.claude.json)", wFail, err.Error(), "", false)
			} else {
				w.set("undo:claudecode-file", g, "Claude Code (~/.claude.json)", wOK, "removed", "", false)
			}
		}
	}
	for _, t := range nativeTools {
		if denied(claudeCodeSettings(), t) {
			if err := removeDeny(claudeCodeSettings(), t); err == nil {
				w.set("undo:deny:"+t, g, "Claude Code: allow built-in "+t+" again", wOK, "removed from deny list", "", false)
			}
		}
	}
	w.logf("info", "Undo finished")
}

func removeDeny(settings, rule string) error {
	var list []string
	data, err := os.ReadFile(settings)
	if err != nil {
		return err
	}
	if raw, ok, _ := jsonedit.Get(data, "permissions", "deny"); ok {
		_ = json.Unmarshal(raw, &list)
	}
	kept := []string{}
	for _, r := range list {
		if r != rule {
			kept = append(kept, r)
		}
	}
	return editJSON(settings, kept, "permissions", "deny")
}
