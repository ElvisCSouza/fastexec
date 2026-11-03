package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ToolManageArgs struct {
	Action string `json:"action"` // "search", "list", "enable", "disable", "call"
	Module string `json:"module,omitempty"`
	Query  string `json:"query,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Params string `json:"params,omitempty"`
}

type DynamicToolDef struct {
	Name        string                                         `json:"name"`
	Description string                                         `json:"description"`
	Schema      any                                            `json:"schema"`
	Handler     func(rawArgs json.RawMessage) (string, error) `json:"-"`
}

type ModuleDef struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Tools       map[string]DynamicToolDef `json:"tools"`
}

type ToolRegistry struct {
	mu            sync.RWMutex
	modules       map[string]*ModuleDef
	activeModules map[string]bool
	onListChanged func()
}

var globalRegistry = &ToolRegistry{
	modules:       make(map[string]*ModuleDef),
	activeModules: make(map[string]bool),
}

func init() {
	// Módulos especializados pré-registrados para expansão modular zero-token
	RegisterModule(&ModuleDef{
		Name:        "docker",
		Description: "Gerenciamento e monitoramento de containers Docker no homelab",
		Tools: map[string]DynamicToolDef{
			"docker_ps": {
				Name:        "docker_ps",
				Description: "Lista containers Docker ativos ou todos com status e portas",
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host string `json:"host"`
						All  bool   `json:"all"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					h := p.Host
					if h == "" {
						h = "noteserver"
					}
					cmd := "docker ps"
					if p.All {
						cmd += " -a"
					}
					return HandleExec(ExecArgs{Host: h, Command: cmd})
				},
			},
			"docker_logs": {
				Name:        "docker_logs",
				Description: "Obtém as últimas linhas de log de um container Docker",
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host      string `json:"host"`
						Container string `json:"container"`
						Lines     int    `json:"lines"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					if p.Container == "" {
						return "", fmt.Errorf("parâmetro 'container' é obrigatório")
					}
					h := p.Host
					if h == "" {
						h = "noteserver"
					}
					lines := p.Lines
					if lines <= 0 {
						lines = 50
					}
					cmd := fmt.Sprintf("docker logs --tail %d %s", lines, p.Container)
					return HandleExec(ExecArgs{Host: h, Command: cmd})
				},
			},
		},
	})

	RegisterModule(&ModuleDef{
		Name:        "git",
		Description: "Inspeções semânticas de Git nos repositórios locais e remotos",
		Tools: map[string]DynamicToolDef{
			"git_status": {
				Name:        "git_status",
				Description: "Status curto e determinístico do repositório git",
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host string `json:"host"`
						Cwd  string `json:"cwd"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					return HandleExec(ExecArgs{Host: p.Host, Cwd: p.Cwd, Command: "git status -s"})
				},
			},
			"git_log": {
				Name:        "git_log",
				Description: "Histórico resumido em 1 linha dos últimos commits",
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host  string `json:"host"`
						Cwd   string `json:"cwd"`
						Count int    `json:"count"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					count := p.Count
					if count <= 0 {
						count = 10
					}
					cmd := fmt.Sprintf("git log --oneline -n %d", count)
					return HandleExec(ExecArgs{Host: p.Host, Cwd: p.Cwd, Command: cmd})
				},
			},
		},
	})
}

// RegisterModule registra um módulo de ferramentas especializadas no catálogo dinâmico.
func RegisterModule(mod *ModuleDef) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.modules[mod.Name] = mod
}

// SetOnListChanged define o callback disparado quando o catálogo de tools ativas muda.
func SetOnListChanged(fn func()) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.onListChanged = fn
}

// GetDynamicHandler busca o handler de uma ferramenta registrada em módulos (ativos ou chamados direto).
func GetDynamicHandler(name string) func(rawArgs json.RawMessage) (string, error) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	for _, mod := range globalRegistry.modules {
		if t, ok := mod.Tools[name]; ok {
			return t.Handler
		}
	}
	return nil
}

// GetActiveDynamicSchemas retorna os schemas das ferramentas dos módulos atualmente ativos.
func GetActiveDynamicSchemas() []any {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	var res []any
	for modName, active := range globalRegistry.activeModules {
		if !active {
			continue
		}
		if mod, ok := globalRegistry.modules[modName]; ok {
			for _, t := range mod.Tools {
				if t.Schema != nil {
					res = append(res, t.Schema)
				}
			}
		}
	}
	return res
}

func HandleToolManage(rawArgs json.RawMessage) (string, error) {
	var args ToolManageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("parâmetros inválidos: %w", err)
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		action = "list"
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	switch action {
	case "list":
		var b strings.Builder
		b.WriteString("MÓDULOS DISPONÍVEIS NO FASTEXEC (Ativação Sob Demanda):\n")
		var names []string
		for k := range globalRegistry.modules {
			names = append(names, k)
		}
		sort.Strings(names)

		for _, name := range names {
			mod := globalRegistry.modules[name]
			status := "[desativado]"
			if globalRegistry.activeModules[name] {
				status = "[ATIVO]"
			}
			fmt.Fprintf(&b, "- %s: %s (%d tools) %s\n", mod.Name, mod.Description, len(mod.Tools), status)
		}
		b.WriteString("\nAções disponíveis:\n")
		b.WriteString("* tool_manage(action='search', query='<termo>') ➔ pesquisa ferramentas sem carregá-las no schema\n")
		b.WriteString("* tool_manage(action='call', tool='<nome>', params='{...}') ➔ executa ferramenta direto (zero-token de schema)\n")
		b.WriteString("* tool_manage(action='enable', module='<nome>') ➔ carrega ferramentas no catálogo ativo\n")
		b.WriteString("* tool_manage(action='disable', module='<nome>') ➔ descarrega ferramentas do catálogo\n")
		return b.String(), nil

	case "search":
		query := strings.ToLower(strings.TrimSpace(args.Query))
		var b strings.Builder
		fmt.Fprintf(&b, "RESULTADOS DA BUSCA POR '%s':\n", query)
		found := 0

		for modName, mod := range globalRegistry.modules {
			for toolName, t := range mod.Tools {
				match := strings.Contains(strings.ToLower(toolName), query) ||
					strings.Contains(strings.ToLower(t.Description), query) ||
					strings.Contains(strings.ToLower(modName), query)
				if match {
					found++
					fmt.Fprintf(&b, "\n• %s (módulo: %s)\n  Descrição: %s\n  Chamada direta: tool_manage(action='call', tool='%s', params='{...}')\n",
						toolName, modName, t.Description, toolName)
				}
			}
		}

		if found == 0 {
			return fmt.Sprintf("Nenhuma ferramenta encontrada para '%s'. Use tool_manage(action='list') para ver os módulos.", query), nil
		}
		return b.String(), nil

	case "enable":
		modName := strings.ToLower(strings.TrimSpace(args.Module))
		if modName == "" {
			return "", fmt.Errorf("parâmetro 'module' é obrigatório para ação 'enable'")
		}
		mod, ok := globalRegistry.modules[modName]
		if !ok {
			return "", fmt.Errorf("módulo '%s' não encontrado. Use tool_manage(action='list')", modName)
		}
		globalRegistry.activeModules[modName] = true
		if globalRegistry.onListChanged != nil {
			go globalRegistry.onListChanged()
		}
		return fmt.Sprintf("Módulo '%s' ativado com sucesso (%d ferramentas carregadas no catálogo).", modName, len(mod.Tools)), nil

	case "disable":
		modName := strings.ToLower(strings.TrimSpace(args.Module))
		if modName == "" {
			return "", fmt.Errorf("parâmetro 'module' é obrigatório para ação 'disable'")
		}
		delete(globalRegistry.activeModules, modName)
		if globalRegistry.onListChanged != nil {
			go globalRegistry.onListChanged()
		}
		return fmt.Sprintf("Módulo '%s' desativado (ferramentas descarregadas do catálogo).", modName), nil

	case "call":
		toolName := strings.TrimSpace(args.Tool)
		if toolName == "" {
			return "", fmt.Errorf("parâmetro 'tool' é obrigatório para ação 'call'")
		}
		var handler func(rawArgs json.RawMessage) (string, error)
		for _, mod := range globalRegistry.modules {
			if t, ok := mod.Tools[toolName]; ok {
				handler = t.Handler
				break
			}
		}
		if handler == nil {
			return "", fmt.Errorf("ferramenta dinâmica '%s' não encontrada", toolName)
		}

		rawParams := json.RawMessage([]byte(args.Params))
		if len(rawParams) == 0 || string(rawParams) == `""` {
			rawParams = json.RawMessage([]byte(`{}`))
		}
		return handler(rawParams)

	default:
		return "", fmt.Errorf("ação desconhecida '%s'. Ações suportadas: list, search, enable, disable, call", action)
	}
}
