package cli

// setup.go: `fastexec setup` registra este executável nos clientes MCP instalados. Idempotente: rodar de novo
// só muda o que estiver diferente. Antes de mudar um arquivo, guarda a versão anterior em <arquivo>.fastexec-backup.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/jsonedit"
)

const setupUsage = `Usage: fastexec setup [--dry-run] [--deny-bash]
  Registers this executable as the "fastexec" MCP server in every AI client found on this machine
  (Claude Desktop, Claude Code, Antigravity, Cursor) and creates ~/.fastexec.
  --dry-run    show what would change, change nothing
  --deny-bash  also deny Claude Code's built-in Bash tool, so shell commands go through FastExec`

func cmdSetup(args []string) {
	for _, a := range args {
		if a == "--ui" {
			cmdSetupUI(args)
			return
		}
	}
	dry, denyBash := false, false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dry = true
		case "--deny-bash":
			denyBash = true
		default:
			fmt.Println(setupUsage)
			os.Exit(2)
		}
	}
	exe := daemon.Self().Exe
	if exe == "" {
		fmt.Fprintln(os.Stderr, "cannot find this executable's path")
		os.Exit(1)
	}
	// verb("register", "registered"): o que vai acontecer (dry-run) ou o que aconteceu
	verb := func(future, done string) string {
		if dry {
			return "would " + future
		}
		return done
	}
	fmt.Printf("Registering %s\n", exe)

	if !dry {
		if err := os.MkdirAll(config.Home(), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "cannot create %s: %v\n", config.Home(), err)
			os.Exit(1)
		}
	}

	changed, failed := []string{}, false
	entry := serverEntry{Command: exe, Args: []string{}}
	for _, cf := range clientFiles() {
		m, names, err := servers(cf.Path)
		if err != nil {
			fmt.Printf("  %s: cannot read %s: %v (left untouched)\n", cf.Client, cf.Path, err)
			failed = true
			continue
		}
		if e, ok := m[serverName]; ok && samePath(e.Command, exe) {
			fmt.Printf("  %s: already registered\n", cf.Client)
		} else {
			if !dry {
				if err := editJSON(cf.Path, entry, "mcpServers", serverName); err != nil {
					fmt.Printf("  %s: %v\n", cf.Client, err)
					failed = true
					continue
				}
			}
			fmt.Printf("  %s: %s (%s)\n", cf.Client, verb("register", "registered"), cf.Path)
			changed = append(changed, cf.Client)
		}
		warnCompeting(cf.Client, cf.Path, m, names)
	}

	if cc, ok := setupClaudeCode(exe, dry, verb); ok {
		if cc {
			changed = append(changed, "Claude Code")
		}
		if denyBash {
			if denied(claudeCodeSettings(), "Bash") {
				fmt.Println("  Claude Code: built-in Bash already denied")
			} else {
				if !dry {
					if err := addDeny(claudeCodeSettings(), "Bash"); err != nil {
						fmt.Printf("  Claude Code: could not deny Bash: %v\n", err)
						failed = true
					}
				}
				fmt.Printf("  Claude Code: %s (%s)\n", verb("deny the built-in Bash tool", "denied the built-in Bash tool"), claudeCodeSettings())
			}
		}
	} else if denyBash {
		fmt.Println("  --deny-bash: Claude Code not found, nothing to change")
	}

	switch {
	case dry:
		fmt.Println("\nDry run: nothing was changed.")
	case len(changed) > 0:
		fmt.Printf("\nRestart %s to load FastExec, then run `fastexec doctor`.\n", strings.Join(changed, ", "))
	default:
		fmt.Println("\nNothing to change. `fastexec doctor` checks the whole installation.")
	}
	if config.Get().Path() == "" {
		fmt.Println("Only host 'local' is configured; `fastexec config init` writes a sample with remote hosts.")
	}
	if failed {
		os.Exit(1)
	}
}

// setupClaudeCode registra no escopo de usuário do Claude Code. found=false quando não há Claude Code.
func setupClaudeCode(exe string, dry bool, verb func(string, string) string) (changed, found bool) {
	cli, cliErr := exec.LookPath("claude")
	data, fileErr := os.ReadFile(claudeCodeFile())
	if cliErr != nil && fileErr != nil {
		return false, false
	}
	var cur serverEntry
	has := false
	if fileErr == nil {
		if raw, ok, _ := jsonedit.Get(data, "mcpServers", serverName); ok {
			has = json.Unmarshal(raw, &cur) == nil
		}
	}
	if has && samePath(cur.Command, exe) {
		fmt.Println("  Claude Code: already registered")
		return false, true
	}
	if dry {
		fmt.Printf("  Claude Code: %s\n", verb("register (user scope)", "registered (user scope)"))
		return true, true
	}
	if cliErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if has {
			_ = exec.CommandContext(ctx, cli, "mcp", "remove", serverName, "-s", "user").Run()
		}
		if out, err := exec.CommandContext(ctx, cli, "mcp", "add", "-s", "user", serverName, "--", exe).CombinedOutput(); err != nil {
			fmt.Printf("  Claude Code: `claude mcp add` failed: %v %s\n", err, strings.TrimSpace(string(out)))
			return false, true
		}
	} else if err := editJSON(claudeCodeFile(), struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}{"stdio", exe, []string{}, map[string]string{}}, "mcpServers", serverName); err != nil {
		fmt.Printf("  Claude Code: %v\n", err)
		return false, true
	}
	fmt.Println("  Claude Code: registered (user scope)")
	return true, true
}

func warnCompeting(client, path string, m map[string]serverEntry, names []string) {
	for _, n := range names {
		if isCompeting(n, m[n]) {
			fmt.Printf("  %s: also has %q, which overlaps with FastExec; models tend to pick it and rarely call FastExec. Remove it from %s if you want FastExec used.\n", client, n, path)
		}
	}
}

// editJSON grava value em path dentro do arquivo, com backup e troca atômica (temporário + rename).
func editJSON(file string, value any, path ...string) error {
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := jsonedit.Set(data, value, path...)
	if err != nil {
		return fmt.Errorf("%s is not valid JSON (left untouched): %w", file, err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if len(data) > 0 {
		if err := os.WriteFile(file+".fastexec-backup", data, 0o600); err != nil {
			return fmt.Errorf("could not back up %s: %w", file, err)
		}
	}
	tmp := file + ".fastexec-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func addDeny(settings, rule string) error {
	var list []string
	if data, err := os.ReadFile(settings); err == nil {
		if raw, ok, _ := jsonedit.Get(data, "permissions", "deny"); ok {
			_ = json.Unmarshal(raw, &list)
		}
	}
	return editJSON(settings, append(list, rule), "permissions", "deny")
}
