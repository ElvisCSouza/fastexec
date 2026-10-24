package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/tools"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolSchema struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type PropertyDef struct {
	Type        string       `json:"type"`
	Description string       `json:"description"`
	Items       *PropertyDef `json:"items,omitempty"`
}

var serverTools = []ToolSchema{
	{
		Name:        "exec",
		Description: "Executa comando síncrono no nó alvo (orientado a eventos, retorna no término exato do processo). Dois modos: 'command' (linha de shell; no Windows 'local' roda em cmd.exe, que interpreta > < | & %VAR% e aspas antes do programa ver o argumento) ou 'argv' (sem shell: cada item chega ao programa exatamente como enviado; use para código e texto com metacaracteres). Código longo vai por 'input' (stdin): argv ['node','-'] ou ['python','-'] com o script em 'input'. Para PowerShell use argv ['powershell','-NoProfile','-Command','-'] com o script em 'input'. No estouro de timeout_ms a árvore de processos é encerrada e a resposta traz a saída parcial. stdin sem 'input' é EOF imediato (comandos interativos falham em vez de travar). Para arquivos prefira 'file_ops'.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "Alvo: 'noteserver' (default), 'ax6000', 'ax3000', 'local', 'vm-ia-core' ou prefixo 'ssh:<host>'"},
				"command":    {Type: "string", Description: "Linha de shell a executar (PROIBIDO usar sleep/timeout artificial). Informe command OU argv"},
				"argv":       {Type: "array", Description: "Alternativa sem shell: [executável, arg1, arg2...]. Nenhum metacaractere é interpretado. Informe argv OU command", Items: &PropertyDef{Type: "string"}},
				"cwd":        {Type: "string", Description: "Diretório de trabalho para execução (opcional)"},
				"input":      {Type: "string", Description: "Conteúdo enviado diretamente para o stdin do processo (opcional, evita criar arquivos temporários)"},
				"timeout_ms": {Type: "integer", Description: "Teto de segurança em ms (padrão: 300000 = 5 min). Ao estourar, a árvore de processos é encerrada"},
			},
		},
	},
	{
		Name:        "sql",
		Description: "Executa SQL analítico DuckDB (direto em arquivos ou 'session') ou SQLite ('database'). Saída sempre em JSON puro.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session":  {Type: "string", Description: "Nome da sessão analítica DuckDB no PC-Elvis (sandbox em W:\\Data\\sessions\\sess-<session>\\)"},
				"database": {Type: "string", Description: "Caminho do arquivo .db SQLite (usado com 'host')"},
				"host":     {Type: "string", Description: "Alvo para SQLite: 'noteserver' (default) ou 'local'"},
				"query":    {Type: "string", Description: "Instrução SQL a executar (no DuckDB aceita 'SELECT ... FROM \"caminho/arquivo.jsonl\"' diretamente)"},
			},
			Required: []string{"query"},
		},
	},
	{
		Name:        "file_ops",
		Description: "Operações atômicas no sistema de arquivos sem shell: copy, move, delete, mkdir, list.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "Alvo: 'local' (default) ou 'noteserver'"},
				"action": {Type: "string", Description: "Ação a executar: 'copy', 'move', 'delete', 'mkdir', 'list'"},
				"path":   {Type: "string", Description: "Caminho do arquivo ou diretório de origem"},
				"target": {Type: "string", Description: "Caminho de destino (obrigatório para 'copy' e 'move')"},
			},
			Required: []string{"action", "path"},
		},
	},
	{
		Name:        "read_file",
		Description: "Lê conteúdo de arquivo local ou remoto.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "Alvo: 'noteserver' (default), 'ax6000', 'ax3000', 'local'"},
				"path":   {Type: "string", Description: "Caminho do arquivo"},
				"offset": {Type: "integer", Description: "Offset em bytes (opcional)"},
				"limit":  {Type: "integer", Description: "Limite de bytes a ler (opcional)"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "write_file",
		Description: "Grava conteúdo em arquivo local ou remoto (chunked e opcionalmente atômico).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":    {Type: "string", Description: "Alvo: 'noteserver' (default), 'ax6000', 'ax3000', 'local'"},
				"path":    {Type: "string", Description: "Caminho do arquivo de destino"},
				"content": {Type: "string", Description: "Conteúdo a ser gravado"},
				"atomic":  {Type: "boolean", Description: "Se true, grava em temporário e renomeia atomicamente (padrão: true)"},
			},
			Required: []string{"path", "content"},
		},
	},
	{
		Name:        "get_file",
		Description: "Baixa arquivo do nó remoto para o PC-Elvis via socket binário direto.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":        {Type: "string", Description: "Host remoto: 'noteserver' (default), 'ax6000', 'ax3000'"},
				"remote_path": {Type: "string", Description: "Caminho do arquivo no nó remoto"},
				"local_path":  {Type: "string", Description: "Caminho local no PC-Elvis (opcional, default: nome do arquivo no CWD)"},
			},
			Required: []string{"remote_path"},
		},
	},
	{
		Name:        "put_file",
		Description: "Envia arquivo do PC-Elvis para o nó remoto de forma chunked e atômica.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":        {Type: "string", Description: "Host remoto: 'noteserver' (default), 'ax6000', 'ax3000'"},
				"local_path":  {Type: "string", Description: "Caminho local no PC-Elvis"},
				"remote_path": {Type: "string", Description: "Caminho de destino no nó remoto"},
			},
			Required: []string{"local_path", "remote_path"},
		},
	},
	{
		Name:        "job",
		Description: "Gerencia background jobs assíncronos no NoteServer via systemd-run. Use action='wait' para aguardar conclusão por evento (zero sleep).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":    {Type: "string", Description: "Alvo: 'noteserver' (default)"},
				"action":  {Type: "string", Description: "Ação: 'start', 'wait' (aguarda conclusão por evento), 'status', 'list', 'tail', 'stop'"},
				"command": {Type: "string", Description: "Comando para ação 'start'"},
				"job_id":  {Type: "string", Description: "Identificador da tarefa para wait/status/tail/stop"},
				"lines":   {Type: "integer", Description: "Número de linhas para ação 'tail' (padrão: 30)"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "ping",
		Description: "Testa latência e conectividade com os nós do homelab (local, noteserver, ax6000, ax3000).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host": {Type: "string", Description: "Host específico ou 'all' para todos (default: 'all')"},
			},
		},
	},
}

type Server struct {
	mu     sync.Mutex
	writer *bufio.Writer
}

func NewServer() *Server {
	telemetry.Init()
	return &Server{
		writer: bufio.NewWriter(os.Stdout),
	}
}

func (s *Server) writeResponse(resp JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		logger.Error("Erro serializando resposta JSON-RPC: %v", err)
		return
	}
	_, _ = s.writer.Write(data)
	_ = s.writer.WriteByte('\n')
	_ = s.writer.Flush()
}

func (s *Server) dispatchTool(name string, rawArgs json.RawMessage) (result ToolCallResult) {
	start := time.Now()
	bytesIn := len(rawArgs)
	host, summary := telemetry.SummarizeCall(name, rawArgs)

	defer func() {
		if r := recover(); r != nil {
			logger.Error("PANIC interceptado na ferramenta %s: %v\nStack: %s", name, r, string(debug.Stack()))
			result = ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("[FATAL ERROR INTERCEPTED] Panic na ferramenta %s: %v", name, r)}},
				IsError: true,
			}
			telemetry.Record(telemetry.AuditEntry{
				Tool:           name,
				Host:           host,
				DurationMs:     time.Since(start).Milliseconds(),
				BytesIn:        bytesIn,
				BytesOut:       len(result.Content[0].Text),
				Success:        false,
				ExitCode:       1,
				ErrorMessage:   fmt.Sprintf("PANIC: %v", r),
				CommandSummary: summary,
			})
		}
	}()

	var outText string
	var err error

	switch name {
	case "exec":
		var args tools.ExecArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleExec(args)
	case "sql":
		var args tools.SqlArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleSql(args)
	case "file_ops":
		var args tools.FileOpsArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleFileOps(args)
	case "read_file":
		var args tools.ReadFileArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleReadFile(args)
	case "write_file":
		var args tools.WriteFileArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleWriteFile(args)
	case "get_file":
		var args tools.GetFileArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleGetFile(args)
	case "put_file":
		var args tools.PutFileArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandlePutFile(args)
	case "job":
		var args tools.JobArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandleJob(args)
	case "ping":
		var args tools.PingArgs
		_ = json.Unmarshal(rawArgs, &args)
		outText, err = tools.HandlePing(args)
	default:
		errMsg := fmt.Sprintf("Ferramenta '%s' não encontrada", name)
		telemetry.Record(telemetry.AuditEntry{
			Tool:           name,
			Host:           host,
			DurationMs:     time.Since(start).Milliseconds(),
			BytesIn:        bytesIn,
			BytesOut:       len(errMsg),
			Success:        false,
			ExitCode:       1,
			ErrorMessage:   errMsg,
			CommandSummary: summary,
		})
		return ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: errMsg}},
			IsError: true,
		}
	}

	durationMs := time.Since(start).Milliseconds()
	bytesOut := len(outText)
	success := (err == nil)
	errMsg := ""
	exitCode := 0
	if err != nil {
		errMsg = err.Error()
		exitCode = 1
		if outText == "" {
			outText = err.Error()
		}
	}

	telemetry.Record(telemetry.AuditEntry{
		Tool:           name,
		Host:           host,
		DurationMs:     durationMs,
		BytesIn:        bytesIn,
		BytesOut:       bytesOut,
		Success:        success,
		ExitCode:       exitCode,
		ErrorMessage:   errMsg,
		CommandSummary: summary,
		Truncated:      name == "exec" && tools.IsTruncated(outText),
		NoResult:       name == "exec" && tools.IsNoResult(outText),
	})

	return ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: outText}},
		IsError: !success,
	}
}

func (s *Server) Run() error {
	logger.Info("Iniciando MCP Server 'fastexec' via stdio...")
	scanner := bufio.NewScanner(os.Stdin)

	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			logger.Error("Erro parsing JSON-RPC: %v", err)
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		switch req.Method {
		case "initialize":
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools": map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "fastexec",
						"version": "1.3.0",
					},
				},
			})

		case "notifications/initialized":
			logger.Info("Cliente MCP conectado e inicializado")

		case "ping":
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{},
			})

		case "tools/list":
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": serverTools,
				},
			})

		case "tools/call":
			go func(r JSONRPCRequest) {
				var callParams struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if err := json.Unmarshal(r.Params, &callParams); err != nil {
					s.writeResponse(JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      r.ID,
						Error:   &RPCError{Code: -32602, Message: "Invalid params"},
					})
					return
				}

				result := s.dispatchTool(callParams.Name, callParams.Arguments)
				s.writeResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      r.ID,
					Result:  result,
				})
			}(req)

		default:
			if req.ID != nil {
				s.writeResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
				})
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		logger.Error("Erro lendo stdin: %v", err)
		return err
	}
	return nil
}
