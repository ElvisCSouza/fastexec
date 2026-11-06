package cli

// doctor.go: `fastexec doctor` confere a instalação de ponta a ponta e diz o que fazer em cada problema.
// Só lê (o teste de escrita na pasta de dados cria e apaga um arquivo). Sai com 1 se algum item falhou.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/jsonedit"
	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/transport"
)

type report struct{ fails, warns int }

func (r *report) ok(f string, a ...any)   { fmt.Printf("  OK    "+f+"\n", a...) }
func (r *report) info(f string, a ...any) { fmt.Printf("  INFO  "+f+"\n", a...) }
func (r *report) warn(f string, a ...any) { r.warns++; fmt.Printf("  WARN  "+f+"\n", a...) }
func (r *report) fail(f string, a ...any) { r.fails++; fmt.Printf("  FAIL  "+f+"\n", a...) }

func cmdDoctor(_ []string) {
	r := &report{}
	self := daemon.Self()
	cfg := config.Get()

	fmt.Println("FastExec")
	r.info("%s", versionLine())
	r.info("executable: %s", self.Exe)
	if p := strings.ToLower(self.Exe); strings.Contains(p, `\temp\`) || strings.Contains(p, "/tmp/") || strings.Contains(p, "downloads") {
		r.warn("running from a temporary or Downloads folder: move it somewhere stable and run `fastexec setup` again")
	}

	fmt.Println("Config")
	switch {
	case cfg.Err() != nil:
		r.fail("%v: running with host 'local' only until it is fixed", cfg.Err())
	case cfg.Path() == "":
		r.ok("no config file (%s): host 'local' only; `fastexec config init` for remote hosts", config.FilePath())
	default:
		r.ok("%s: default host %s, %d remote host(s)", cfg.Path(), cfg.Default(), len(cfg.Remote(false)))
	}

	fmt.Println("Data")
	checkData(r, cfg)

	fmt.Println("Local daemon")
	checkDaemon(r, self)

	fmt.Println("Clients")
	checkClients(r, self)

	if hosts := cfg.Remote(false); len(hosts) > 0 {
		fmt.Println("Remote hosts")
		checkHosts(r, cfg, hosts)
	}

	fmt.Printf("\n%d failure(s), %d warning(s)\n", r.fails, r.warns)
	if r.fails > 0 {
		os.Exit(1)
	}
}

func versionLine() string {
	line := "fastexec " + Version
	if len(registered) > 0 {
		line += " + " + strings.Join(registered, ", ")
	}
	return line
}

func checkData(r *report, cfg *config.Config) {
	dir := cfg.Data()
	probe := filepath.Join(dir, fmt.Sprintf(".doctor-%d", os.Getpid()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.fail("data folder %s cannot be created: %v", dir, err)
	} else if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		r.fail("data folder %s is not writable: %v", dir, err)
	} else {
		_ = os.Remove(probe)
		r.ok("data folder %s", dir)
	}
	if runtime.GOOS == "windows" {
		low := strings.ToLower(dir)
		for _, env := range []string{"LOCALAPPDATA", "APPDATA"} {
			if v := strings.ToLower(os.Getenv(env)); v != "" && strings.HasPrefix(low, v) {
				r.warn("data folder is under %%%s%%: a Claude Desktop from the Microsoft Store sees a private copy of it, so each client would keep separate telemetry; prefer the default (~/.fastexec)", env)
			}
		}
		old, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "Claude_*", "LocalCache", "Local", "fastexec", "telemetry", "fastexec.db"))
		for _, p := range old {
			r.info("older FastExec telemetry kept by Claude Desktop's sandbox: %s (copy fastexec.db* to %s to see it in `fastexec stats`)", p, filepath.Join(dir, "telemetry"))
		}
	}

	if !cfg.TelemetryOn() {
		r.ok("telemetry off")
		return
	}
	path, exists, total, last, err := telemetry.Peek()
	switch {
	case err != nil:
		r.fail("telemetry database %s: %v", path, err)
	case !exists || total == 0:
		r.info("telemetry on, nothing recorded yet in %s (normal before the first tool call; if a client already used FastExec, it runs another binary or data folder)", path)
	default:
		r.ok("telemetry on: %d calls in %s, last %s", total, path, last)
	}
}

func checkDaemon(r *report, self daemon.Identity) {
	cur, err := daemon.Running()
	switch {
	case err != nil:
		r.ok("not running (starts on the first tool call)")
	case cur.ID == "":
		r.warn("PID %d is an older FastExec: restart your AI client (or `fastexec daemon stop`) so tools run this version", cur.PID)
	case cur.ID != self.ID:
		r.warn("PID %d runs another binary (%s): it is replaced when an AI client starts this one; now: `fastexec daemon stop`", cur.PID, cur.Exe)
	default:
		r.ok("PID %d, same binary", cur.PID)
	}
}

func checkClients(r *report, self daemon.Identity) {
	found := 0
	for _, cf := range clientFiles() {
		found++
		m, names, err := servers(cf.Path)
		if err != nil {
			r.fail("%s (%s): cannot read: %v", cf.Client, cf.Path, err)
			continue
		}
		reportEntry(r, cf.Client, cf.Path, m, names, self)
	}

	// Claude Code: escopo de usuário no ~/.claude.json
	if data, err := os.ReadFile(claudeCodeFile()); err == nil {
		found++
		raw, ok, _ := jsonedit.Get(data, "mcpServers")
		m, names := map[string]serverEntry{}, []string(nil)
		if ok {
			if mm, nn, err := serversFromRaw(raw); err == nil {
				m, names = mm, nn
			}
		}
		reportEntry(r, "Claude Code", claudeCodeFile(), m, names, self)
		if denied(claudeCodeSettings(), "Bash") {
			r.info("Claude Code: built-in Bash denied, shell commands go through FastExec")
		} else {
			r.info("Claude Code: built-in Bash allowed (it will often be used instead of FastExec; `fastexec setup --deny-bash` to switch)")
		}
	}
	if found == 0 {
		r.warn("no AI client config found (Claude Desktop, Claude Code, Antigravity, Cursor): see docs/install.md to register FastExec by hand")
	}
}

func reportEntry(r *report, client, path string, m map[string]serverEntry, names []string, self daemon.Identity) {
	e, ok := m[serverName]
	switch {
	case !ok:
		r.warn("%s: FastExec not registered (%s): run `fastexec setup`", client, path)
	case !samePath(e.Command, self.Exe):
		switch id := daemon.FileID(strings.ReplaceAll(e.Command, `\\`, `\`)); {
		case id == "":
			r.fail("%s: registered command does not exist: %s (run `fastexec setup`)", client, e.Command)
		case id == self.ID:
			r.ok("%s: registered (same build at %s)", client, e.Command)
		default:
			r.warn("%s: runs a different build at %s; `fastexec setup` points it to this one", client, e.Command)
		}
	default:
		r.ok("%s: registered", client)
	}
	var comp []string
	for _, n := range names {
		if isCompeting(n, m[n]) {
			comp = append(comp, n)
		}
	}
	if len(comp) > 0 {
		r.warn("%s: also has %s, which overlap with FastExec; models tend to pick those and rarely call FastExec", client, strings.Join(comp, ", "))
	}
}

func checkHosts(r *report, cfg *config.Config, hosts []string) {
	for _, name := range hosts {
		t, err := transport.ResolveHost(name)
		if err != nil {
			r.fail("%s: %v", name, err)
			continue
		}
		if t.IsSSH {
			r.info("%s: ssh %s (not checked)", name, t.SSHHost)
			continue
		}
		start := time.Now()
		sx, err := transport.DaemonSupportsX(t.TCPAddr)
		ms := float64(time.Since(start).Microseconds()) / 1000
		switch {
		case err != nil:
			r.fail("%s (%s): %v", name, t.TCPAddr, err)
		case !sx:
			r.warn("%s (%s): reachable, but server-exec is an old version (no direct argv/stdin); update it from server/", name, t.TCPAddr)
		default:
			r.ok("%s (%s): %.1f ms", name, t.TCPAddr, ms)
		}
	}
}
