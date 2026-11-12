package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/transport"
)

// Pasta do projeto do cliente.
//
// O daemon é um só e compartilhado: ele roda os comandos locais na pasta onde subiu, que não tem relação com a pasta
// em que o Claude Code está trabalhando. O proxy MCP, ao contrário, é filho do cliente e herda a pasta do projeto.
// Medido no ciclo6 do benchmark: um `dir /b` sem caminho listou a pasta do benchmark em vez da tarefa, e o agente
// gastou oito chamadas perdido (CS6, repetição 2).
//
// Para o Claude Code (a pasta herdada é a do projeto; em outros clientes ela não significa nada), no host local:
//   - `cwd` ausente vira a pasta do cliente; `cwd` relativo é resolvido a partir dela;
//   - `path`/`file_path`/`repo_path` relativos são resolvidos a partir dela.
//
// Caminho absoluto, host remoto e qualquer outro cliente passam intactos. Nenhum parâmetro novo no schema.

func isClaudeCodeClient(name string) bool { return strings.HasPrefix(name, "claude-code") }

func isRelativeToCwd(p string) bool {
	if p == "" || filepath.IsAbs(p) {
		return false
	}
	// "/w/Data/x" (Git Bash), "\\servidor\x" e "C:\x" já apontam para um lugar certo; "~" é da shell remota.
	return !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "\\") && !strings.HasPrefix(p, "~") &&
		!(len(p) >= 2 && p[1] == ':')
}

var pathKeysByTool = map[string][]string{
	"exec":         {"cwd"},
	"job":          {"cwd"},
	"read_file":    {"path", "file_path"},
	"write_file":   {"path", "file_path"},
	"edit_file":    {"path", "file_path"},
	"code_outline": {"path", "file_path"},
	"code_graph":   {"repo_path"},
}

// withClientCwd devolve os argumentos com a pasta do cliente aplicada; sem o que aplicar, devolve o original.
func withClientCwd(tool string, args json.RawMessage, cwd string) json.RawMessage {
	keys, ok := pathKeysByTool[tool]
	if !ok || cwd == "" {
		return args
	}
	var m map[string]json.RawMessage
	if len(args) == 0 {
		m = map[string]json.RawMessage{}
	} else if json.Unmarshal(args, &m) != nil || m == nil {
		return args
	}
	var host string
	if raw, has := m["host"]; has {
		_ = json.Unmarshal(raw, &host)
	}
	target, err := transport.ResolveHost(host)
	if err != nil || !target.IsLocal {
		return args
	}
	changed := false
	for _, k := range keys {
		var cur string
		raw, has := m[k]
		if has {
			if json.Unmarshal(raw, &cur) != nil {
				continue
			}
		}
		switch {
		case !has || cur == "":
			if k != "cwd" { // só o cwd tem padrão; um path ausente é erro do chamador e segue como está
				continue
			}
			b, _ := json.Marshal(cwd)
			m[k] = b
			changed = true
		case isRelativeToCwd(cur):
			b, _ := json.Marshal(filepath.Join(cwd, cur))
			m[k] = b
			changed = true
		}
	}
	if !changed {
		return args
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

// clientCwd é a pasta herdada pelo proxy, só para clientes em que ela é a do projeto.
func (s *Server) clientCwd() string {
	if !isClaudeCodeClient(s.clientNameSafe()) {
		return ""
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
