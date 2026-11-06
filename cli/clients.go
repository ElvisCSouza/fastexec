package cli

// clients.go: onde cada cliente MCP guarda a config, para o `setup` (registrar o FastExec) e o `doctor` (conferir).
// Só arquivos que já existem são editados: arquivo presente = cliente instalado. O Claude Code é registrado pela
// própria CLI (`claude mcp add`) quando ela está no PATH, porque ele reescreve o ~/.claude.json o tempo todo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"homelab/fastexec/internal/jsonedit"
)

const serverName = "fastexec"

// clientFile é um arquivo de config de cliente MCP com a lista em "mcpServers".
type clientFile struct {
	Client string // nome para o usuário
	Path   string
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// clientFiles lista os arquivos de config existentes, na ordem de exibição.
func clientFiles() []clientFile {
	home := homeDir()
	var c []clientFile
	add := func(client string, paths ...string) {
		for _, p := range paths {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				c = append(c, clientFile{client, p})
			}
		}
	}
	switch runtime.GOOS {
	case "windows":
		local, roaming := os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA")
		// Claude Desktop da Microsoft Store (MSIX) guarda tudo em %LOCALAPPDATA%\Claude-Data\claude_<id>; o
		// instalador antigo, em %APPDATA%\Claude. Os dois podem existir: o primeiro é o que o app atual lê.
		msix, _ := filepath.Glob(filepath.Join(local, "Claude-Data", "claude_*", "claude_desktop_config.json"))
		sort.Strings(msix)
		add("Claude Desktop", msix...)
		add("Claude Desktop (legacy location)", filepath.Join(roaming, "Claude", "claude_desktop_config.json"))
	case "darwin":
		add("Claude Desktop", filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"))
	default:
		add("Claude Desktop", filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"))
	}
	add("Antigravity",
		filepath.Join(home, ".gemini", "config", "mcp_config.json"),
		filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"))
	add("Cursor", filepath.Join(home, ".cursor", "mcp.json"))
	return c
}

// claudeCodeFile é o arquivo do escopo de usuário do Claude Code (lido pelo doctor; o setup usa a CLI).
func claudeCodeFile() string { return filepath.Join(homeDir(), ".claude.json") }

// claudeCodeSettings guarda permissions.deny (o setup --deny-bash põe "Bash" lá).
func claudeCodeSettings() string { return filepath.Join(homeDir(), ".claude", "settings.json") }

// serverEntry é o que um cliente tem registrado para um servidor MCP.
type serverEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// servers lê "mcpServers" de um arquivo de cliente.
func servers(path string) (map[string]serverEntry, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	raw, ok, err := jsonedit.Get(data, "mcpServers")
	if err != nil || !ok {
		return map[string]serverEntry{}, nil, err
	}
	return serversFromRaw(raw)
}

func serversFromRaw(raw json.RawMessage) (map[string]serverEntry, []string, error) {
	m := map[string]serverEntry{}
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, nil, err
		}
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return m, names, nil
}

// denied: o settings.json do Claude Code tem rule em permissions.deny.
func denied(settingsPath, rule string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	raw, ok, err := jsonedit.Get(data, "permissions", "deny")
	if err != nil || !ok {
		return false
	}
	var list []string
	_ = json.Unmarshal(raw, &list)
	for _, r := range list {
		if r == rule {
			return true
		}
	}
	return false
}

// samePath compara caminhos de executável como o sistema compara: sem diferenciar maiúsculas no Windows, com
// barras normalizadas e barras invertidas duplicadas (JSON escapado duas vezes) desfeitas.
func samePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\\`, `\`)
		p = filepath.Clean(filepath.FromSlash(p))
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return norm(a) == norm(b)
}

// competing: servidores que também executam comando ou mexem em arquivo. Com eles no mesmo cliente o modelo
// costuma escolher o outro e o FastExec quase não é chamado.
// Casa pelo nome e pelos argumentos (nome do pacote), não pelo comando: "cmd /c npx ..." é só o jeito de rodar npx.
var competingHints = []string{"desktop-commander", "desktop_commander", "commander", "filesystem", "shell", "terminal", "tabby"}

func isCompeting(name string, e serverEntry) bool {
	if strings.EqualFold(name, serverName) {
		return false
	}
	s := strings.ToLower(name + " " + strings.Join(e.Args, " "))
	for _, h := range competingHints {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}
