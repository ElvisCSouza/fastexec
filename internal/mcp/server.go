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

type Server struct {
	mu        sync.Mutex
	writer    *bufio.Writer
	Forwarder func(name string, rawArgs json.RawMessage) (ToolCallResult, error)
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

func (s *Server) writeNotification(method string, params any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}
	data, err := json.Marshal(notif)
	if err != nil {
		return
	}
	_, _ = s.writer.Write(data)
	_ = s.writer.WriteByte('\n')
	_ = s.writer.Flush()
}

func (s *Server) dispatchTool(name string, rawArgs json.RawMessage) ToolCallResult {
	if s.Forwarder != nil {
		if res, err := s.Forwarder(name, rawArgs); err == nil {
			return res
		}
	}
	return DispatchTool(name, rawArgs)
}

func DispatchTool(name string, rawArgs json.RawMessage) (result ToolCallResult) {
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

	if h, ok := toolHandlers[name]; ok {
		outText, err = h(rawArgs)
	} else {
		if dynamicHandler := tools.GetDynamicHandler(name); dynamicHandler != nil {
			outText, err = dynamicHandler(rawArgs)
		} else {
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
	}

	durationMs := time.Since(start).Milliseconds()
	bytesOut := len(outText)
	success := (err == nil)
	errMsg := ""
	exitCode := 0
	if err != nil {
		errMsg, exitCode = errorCause(err, outText)
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

	tools.SetOnListChanged(func() {
		s.writeNotification("notifications/tools/list_changed", map[string]any{})
	})

	return s.serve(os.Stdin)
}

// serve processa requisições JSON-RPC linha a linha de r. Separado de Run para os testes golden injetarem a entrada.
func (s *Server) serve(r io.Reader) error {
	scanner := bufio.NewScanner(r)

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
						"tools": map[string]any{
							"listChanged": true,
						},
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
			allTools := make([]any, 0, len(serverTools))
			for _, st := range serverTools {
				allTools = append(allTools, st)
			}
			for _, ds := range tools.GetActiveDynamicSchemas() {
				allTools = append(allTools, ds)
			}
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": allTools,
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
