package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"homelab/fastexec/internal/jsonedit"
)

// sandboxEnv monta um perfil de usuário falso (Claude Desktop, Antigravity e Claude Code instalados) e devolve o
// ambiente que faz o CLI enxergar só ele. PATH mínimo: a CLI `claude` real nunca é chamada.
func sandboxEnv(t *testing.T) (env []string, desktopCfg, agCfg, ccFile, ccSettings string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	local, roaming := filepath.Join(root, "local"), filepath.Join(root, "roaming")
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	switch runtime.GOOS {
	case "windows":
		desktopCfg = filepath.Join(local, "Claude-Data", "claude_test", "claude_desktop_config.json")
	case "darwin":
		desktopCfg = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	default:
		desktopCfg = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	write(desktopCfg, `{"mcpServers":{"desktop-commander":{"command":"npx","args":["-y","@wonderwhy-er/desktop-commander"]}},"preferences":{"x":1}}`)
	agCfg = filepath.Join(home, ".gemini", "config", "mcp_config.json")
	write(agCfg, `{"mcpServers":{}}`)
	ccFile = filepath.Join(home, ".claude.json")
	write(ccFile, `{"numStartups":1}`)
	ccSettings = filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(roaming, 0o755)
	pathDir := filepath.Join(root, "bin")
	_ = os.MkdirAll(pathDir, 0o755)
	env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+local, "APPDATA="+roaming,
		"FASTEXEC_HOME="+filepath.Join(root, "fx"), "FASTEXEC_CONFIG=", "PATH="+pathDir)
	return
}

func runIn(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{cliChildArg}, args...)...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

func registeredCommand(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok, err := jsonedit.Get(data, "mcpServers", "fastexec", "command")
	if err != nil || !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// TestSetupRegistersEverywhereAndIsIdempotent: setup grava nos três clientes, preserva o resto, faz backup, avisa
// do servidor concorrente, nega o Bash; rodar de novo não muda nada. O doctor fecha sem falha.
func TestSetupRegistersEverywhereAndIsIdempotent(t *testing.T) {
	env, desktop, ag, cc, ccSettings := sandboxEnv(t)

	out, code := runIn(t, env, "setup", "--dry-run", "--deny-bash")
	if code != 0 || !strings.Contains(out, "Dry run") || registeredCommand(t, desktop) != "" {
		t.Fatalf("dry run changed something or failed (%d):\n%s", code, out)
	}

	out, code = runIn(t, env, "setup", "--deny-bash")
	if code != 0 {
		t.Fatalf("setup exit %d:\n%s", code, out)
	}
	for _, f := range []string{desktop, ag, cc} {
		if registeredCommand(t, f) == "" {
			t.Errorf("%s: fastexec not registered\n%s", f, out)
		}
		if _, err := os.Stat(f + ".fastexec-backup"); err != nil {
			t.Errorf("%s: no backup", f)
		}
	}
	if !strings.Contains(out, `"desktop-commander"`) {
		t.Errorf("competing server not reported:\n%s", out)
	}
	if !denied(ccSettings, "Bash") {
		t.Error("Bash not denied")
	}
	data, _ := os.ReadFile(desktop)
	if !strings.Contains(string(data), `"preferences"`) || strings.Index(string(data), "desktop-commander") > strings.Index(string(data), `"fastexec"`) {
		t.Errorf("other content or order not preserved:\n%s", data)
	}

	before, _ := os.ReadFile(desktop)
	out, code = runIn(t, env, "setup", "--deny-bash")
	after, _ := os.ReadFile(desktop)
	if code != 0 || !strings.Contains(out, "Nothing to change") || !bytes.Equal(before, after) {
		t.Fatalf("second setup was not a no-op (%d):\n%s", code, out)
	}

	out, code = runIn(t, env, "doctor")
	if code != 0 || strings.Contains(out, "FAIL ") || !strings.Contains(out, "Claude Code: registered") {
		t.Fatalf("doctor after setup (%d):\n%s", code, out)
	}
}

func TestSetupLeavesInvalidJSONAlone(t *testing.T) {
	env, desktop, _, _, _ := sandboxEnv(t)
	if err := os.WriteFile(desktop, []byte(`{"mcpServers": {`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runIn(t, env, "setup")
	data, _ := os.ReadFile(desktop)
	if code == 0 || string(data) != `{"mcpServers": {` {
		t.Fatalf("invalid file was touched or error hidden (%d):\n%s", code, out)
	}
}

func TestSamePathAndCompeting(t *testing.T) {
	if runtime.GOOS == "windows" && !samePath(`C:\\Tools\\FastExec.exe`, `c:/tools/fastexec.exe`) {
		t.Error("windows paths should match (case, slashes, doubled backslashes)")
	}
	if samePath("/a/fastexec", "/b/fastexec") {
		t.Error("different paths matched")
	}
	cases := map[string]serverEntry{
		"desktop-commander": {Command: "npx"},
		"fs":                {Command: "npx", Args: []string{"@modelcontextprotocol/server-filesystem"}},
	}
	for n, e := range cases {
		if !isCompeting(n, e) {
			t.Errorf("%s should compete", n)
		}
	}
	if isCompeting("alexa", serverEntry{Command: "cmd", Args: []string{"/c", "node", "alexa.js"}}) || isCompeting("fastexec", serverEntry{}) {
		t.Error("false positive")
	}
}
