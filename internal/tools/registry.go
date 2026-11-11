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
	Name        string                                        `json:"name"`
	Description string                                        `json:"description"`
	Schema      any                                           `json:"schema"`
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
		Description: "Docker container management and monitoring",
		Tools: map[string]DynamicToolDef{
			"docker_ps": {
				Name:        "docker_ps",
				Description: "List running (or all) Docker containers with status and ports",
				Schema: map[string]any{
					"name":        "docker_ps",
					"description": "List running (or all) Docker containers with status and ports",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host": map[string]any{"type": "string", "description": "Target: {hosts}"},
							"all":  map[string]any{"type": "boolean", "description": "List all containers (-a)"},
						},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host string `json:"host"`
						All  bool   `json:"all"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					h := p.Host
					cmd := "docker ps"
					if p.All {
						cmd += " -a"
					}
					return HandleExec(ExecArgs{Host: h, Command: cmd})
				},
			},
			"docker_logs": {
				Name:        "docker_logs",
				Description: "Last log lines of a Docker container",
				Schema: map[string]any{
					"name":        "docker_logs",
					"description": "Last log lines of a Docker container",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host":      map[string]any{"type": "string", "description": "Target: {hosts}"},
							"container": map[string]any{"type": "string", "description": "Container name or ID"},
							"lines":     map[string]any{"type": "integer", "description": "Log lines (default 50)"},
						},
						"required": []string{"container"},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var p struct {
						Host      string `json:"host"`
						Container string `json:"container"`
						Lines     int    `json:"lines"`
					}
					_ = json.Unmarshal(rawArgs, &p)
					if p.Container == "" {
						return "", fmt.Errorf("parameter 'container' is required")
					}
					h := p.Host
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
		Name:        "sql",
		Description: "Analytic SQL on DuckDB and SQLite with plain JSON output",
		Tools: map[string]DynamicToolDef{
			"sql": {
				Name:        "sql",
				Description: "Run analytic SQL: DuckDB (straight on files, or a 'session') or SQLite ('database'). Output is plain JSON.",
				Schema: map[string]any{
					"name":        "sql",
					"description": "Run analytic SQL: DuckDB (straight on files, or a 'session') or SQLite ('database'). Output is plain JSON.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"session":  map[string]any{"type": "string", "description": "DuckDB analytic session name (one sandbox folder per session)"},
							"database": map[string]any{"type": "string", "description": "SQLite .db file path (with 'host')"},
							"host":     map[string]any{"type": "string", "description": "Target for SQLite: {hosts}"},
							"query":    map[string]any{"type": "string", "description": "SQL to run (DuckDB accepts 'SELECT ... FROM \"path/file.jsonl\"' directly)"},
						},
						"required": []string{"query"},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args SqlArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for sql: %w", err)
					}
					return HandleSql(args)
				},
			},
		},
	})

	RegisterModule(&ModuleDef{
		Name:        "hosts",
		Description: "Host tools: connectivity check and binary file transfer",
		Tools: map[string]DynamicToolDef{
			"ping": {
				Name:        "ping",
				Description: "Latency and connectivity check against the configured hosts.",
				Schema: map[string]any{
					"name":        "ping",
					"description": "Latency and connectivity check against the configured hosts.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host": map[string]any{"type": "string", "description": "A host, or 'all' (default)"},
						},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args PingArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for ping: %w", err)
					}
					return HandlePing(args)
				},
			},
			"get_file": {
				Name:        "get_file",
				Description: "Download a file from a remote host over the binary socket.",
				Schema: map[string]any{
					"name":        "get_file",
					"description": "Download a file from a remote host over the binary socket.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host":        map[string]any{"type": "string", "description": "Remote host: {hosts}"},
							"remote_path": map[string]any{"type": "string", "description": "File path on the remote host"},
							"local_path":  map[string]any{"type": "string", "description": "Local path (default: the file name in the current directory)"},
						},
						"required": []string{"remote_path"},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args GetFileArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for get_file: %w", err)
					}
					return HandleGetFile(args)
				},
			},
			"put_file": {
				Name:        "put_file",
				Description: "Upload a local file to a remote host (chunked, atomic).",
				Schema: map[string]any{
					"name":        "put_file",
					"description": "Upload a local file to a remote host (chunked, atomic).",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host":        map[string]any{"type": "string", "description": "Remote host: {hosts}"},
							"local_path":  map[string]any{"type": "string", "description": "Local file path"},
							"remote_path": map[string]any{"type": "string", "description": "Destination path on the remote host"},
						},
						"required": []string{"local_path", "remote_path"},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args PutFileArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for put_file: %w", err)
					}
					return HandlePutFile(args)
				},
			},
		},
	})

	RegisterModule(&ModuleDef{
		Name:        "files",
		Description: "File system operations without a shell: copy, move, delete, mkdir, list",
		Tools: map[string]DynamicToolDef{
			"file_ops": {
				Name:        "file_ops",
				Description: "File system operations without a shell: copy, move, delete, mkdir, list.",
				Schema: map[string]any{
					"name":        "file_ops",
					"description": "File system operations without a shell: copy, move, delete, mkdir, list.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"host":   map[string]any{"type": "string", "description": "'local' (default) or a configured host"},
							"action": map[string]any{"type": "string", "description": "'copy', 'move', 'delete', 'mkdir' or 'list'"},
							"path":   map[string]any{"type": "string", "description": "Source file or directory"},
							"target": map[string]any{"type": "string", "description": "Destination (required for copy and move)"},
						},
						"required": []string{"action", "path"},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args FileOpsArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for file_ops: %w", err)
					}
					return HandleFileOps(args)
				},
			},
		},
	})
	RegisterModule(&ModuleDef{
		Name:        "handoff",
		Description: "Session handoff and long-term memory built from real telemetry (no LLM)",
		Tools: map[string]DynamicToolDef{
			"handoff": {
				Name:        "handoff",
				Description: "Session handoff and long-term memory (no LLM): 'latest' returns the last project briefing for a cold start; 'save' records the session; 'search' finds past decisions; 'list' lists handoffs.",
				Schema: map[string]any{
					"name":        "handoff",
					"description": "Session handoff and long-term memory (no LLM): 'latest' returns the last project briefing for a cold start; 'save' records the session; 'search' finds past decisions; 'list' lists handoffs.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action":  map[string]any{"type": "string", "description": "'latest' (default), 'save', 'search' or 'list'"},
							"topic":   map[string]any{"type": "string", "description": "Session topic for 'save', e.g. 'auth-refactor'"},
							"notes":   map[string]any{"type": "string", "description": "Session summary for 'save'"},
							"pending": map[string]any{"type": "string", "description": "Pending items or next steps for 'save'"},
							"query":   map[string]any{"type": "string", "description": "Search term for 'search'"},
							"limit":   map[string]any{"type": "integer", "description": "Max results for search/list (default 5)"},
						},
					},
				},
				Handler: func(rawArgs json.RawMessage) (string, error) {
					var args HandoffArgs
					if err := json.Unmarshal(rawArgs, &args); err != nil {
						return "", fmt.Errorf("invalid parameters for handoff: %w", err)
					}
					return HandleHandoff(args)
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

	// Ordem determinística (módulo, depois ferramenta): o schema vai ao LLM e precisa ser estável entre listagens.
	var modNames []string
	for modName, active := range globalRegistry.activeModules {
		if active {
			modNames = append(modNames, modName)
		}
	}
	sort.Strings(modNames)

	var res []any
	for _, modName := range modNames {
		mod, ok := globalRegistry.modules[modName]
		if !ok {
			continue
		}
		toolNames := make([]string, 0, len(mod.Tools))
		for toolName := range mod.Tools {
			toolNames = append(toolNames, toolName)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			if t := mod.Tools[toolName]; t.Schema != nil {
				res = append(res, t.Schema)
			}
		}
	}
	return res
}

func HandleToolManage(rawArgs json.RawMessage) (string, error) {
	var args ToolManageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
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
		b.WriteString("FASTEXEC MODULES (loaded on demand):\n")
		var names []string
		for k := range globalRegistry.modules {
			names = append(names, k)
		}
		sort.Strings(names)

		for _, name := range names {
			mod := globalRegistry.modules[name]
			status := "[disabled]"
			if globalRegistry.activeModules[name] {
				status = "[ACTIVE]"
			}
			fmt.Fprintf(&b, "- %s: %s (%d tools) %s\n", mod.Name, mod.Description, len(mod.Tools), status)
		}
		b.WriteString("\nActions:\n")
		b.WriteString("* tool_manage(action='search', query='<term>') ➔ find tools without loading them into the schema\n")
		b.WriteString("* tool_manage(action='call', tool='<name>', params='{...}') ➔ run a tool directly (no schema tokens)\n")
		b.WriteString("* tool_manage(action='enable', module='<name>') ➔ load the module's tools into the active catalog\n")
		b.WriteString("* tool_manage(action='disable', module='<name>') ➔ unload them\n")
		return b.String(), nil

	case "search":
		query := strings.ToLower(strings.TrimSpace(args.Query))
		var b strings.Builder
		fmt.Fprintf(&b, "SEARCH RESULTS FOR '%s':\n", query)
		found := 0

		for modName, mod := range globalRegistry.modules {
			for toolName, t := range mod.Tools {
				match := strings.Contains(strings.ToLower(toolName), query) ||
					strings.Contains(strings.ToLower(t.Description), query) ||
					strings.Contains(strings.ToLower(modName), query)
				if match {
					found++
					fmt.Fprintf(&b, "\n• %s (module: %s)\n  Description: %s\n  Direct call: tool_manage(action='call', tool='%s', params='{...}')\n",
						toolName, modName, t.Description, toolName)
				}
			}
		}

		if found == 0 {
			return fmt.Sprintf("No tool found for '%s'. Use tool_manage(action='list') to see the modules.", query), nil
		}
		return b.String(), nil

	case "enable":
		modName := strings.ToLower(strings.TrimSpace(args.Module))
		if modName == "" {
			return "", fmt.Errorf("parameter 'module' is required for 'enable'")
		}
		mod, ok := globalRegistry.modules[modName]
		if !ok {
			return "", fmt.Errorf("module '%s' not found. Use tool_manage(action='list')", modName)
		}
		globalRegistry.activeModules[modName] = true
		if globalRegistry.onListChanged != nil {
			go globalRegistry.onListChanged()
		}
		return fmt.Sprintf("Module '%s' enabled (%d tools loaded into the catalog).", modName, len(mod.Tools)), nil

	case "disable":
		modName := strings.ToLower(strings.TrimSpace(args.Module))
		if modName == "" {
			return "", fmt.Errorf("parameter 'module' is required for 'disable'")
		}
		delete(globalRegistry.activeModules, modName)
		if globalRegistry.onListChanged != nil {
			go globalRegistry.onListChanged()
		}
		return fmt.Sprintf("Module '%s' disabled (its tools were unloaded).", modName), nil

	case "call":
		toolName := strings.TrimSpace(args.Tool)
		if toolName == "" {
			return "", fmt.Errorf("parameter 'tool' is required for 'call'")
		}
		var handler func(rawArgs json.RawMessage) (string, error)
		for _, mod := range globalRegistry.modules {
			if t, ok := mod.Tools[toolName]; ok {
				handler = t.Handler
				break
			}
		}
		if handler == nil {
			return "", fmt.Errorf("dynamic tool '%s' not found", toolName)
		}

		rawParams := json.RawMessage([]byte(args.Params))
		if len(rawParams) == 0 || string(rawParams) == `""` {
			rawParams = json.RawMessage([]byte(`{}`))
		}
		return handler(rawParams)

	default:
		return "", fmt.Errorf("unknown action '%s' (supported: list, search, enable, disable, call)", action)
	}
}
