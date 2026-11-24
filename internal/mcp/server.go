package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
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
	Type string `json:"type"`
	// omitempty: parâmetro cujo nome já diz tudo vai sem 'description' (o schema é imposto fixo por turno; ver
	// tool_schemas.go). A descrição é opcional no MCP e nada no código a lê.
	Description string       `json:"description,omitempty"`
	Items       *PropertyDef `json:"items,omitempty"`
}

type Server struct {
	clientMu   sync.Mutex
	clientName string // clientInfo.name do initialize (ex.: claude-ai, local-agent-mode-fastexec)
	mu         sync.Mutex
	writer     *bufio.Writer
	Forwarder  func(name string, rawArgs json.RawMessage) (ToolCallResult, error)
}

func NewServer() *Server {
	if config.Get().TelemetryOn() {
		telemetry.Init()
	}
	return &Server{
		writer: bufio.NewWriter(os.Stdout),
	}
}

func (s *Server) writeResponse(resp JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		logger.Error("JSON-RPC response serialization error: %v", err)
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

// AfterSendError: a chamada já foi escrita no daemon e a resposta não voltou (prazo, conexão): ela pode ter rodado.
// Refazê-la neste processo duplicaria o efeito (job iniciado duas vezes, arquivo gravado duas vezes).
type AfterSendError struct{ Err error }

func (e *AfterSendError) Error() string { return e.Err.Error() }
func (e *AfterSendError) Unwrap() error { return e.Err }

// transportLostEntry: linha da chamada cuja resposta se perdeu depois do envio ao daemon. start é o instante ANTES de
// o Forwarder rodar, para que DurationMs seja a duração real da chamada (inclui a espera até a perda).
func transportLostEntry(name string, rawArgs json.RawMessage, err error, start time.Time) telemetry.AuditEntry {
	client, conversation := callerOf(rawArgs)
	host, summary := telemetry.SummarizeCall(name, rawArgs)
	return telemetry.AuditEntry{
		Tool:           name,
		Host:           host,
		DurationMs:     time.Since(start).Milliseconds(),
		BytesIn:        len(rawArgs),
		Success:        false,
		ExitCode:       1,
		ErrorMessage:   "transport_lost: " + err.Error(),
		CommandSummary: summary,
		Client:         client,
		Conversation:   conversation,
		ArgsHash:       telemetry.ArgsHash(rawArgs),
		Outcome:        outcomePtr(telemetry.LostOutcome()),
	}
}

func (s *Server) dispatchTool(name string, rawArgs json.RawMessage) ToolCallResult {
	if s.Forwarder != nil {
		start := time.Now()
		res, err := s.Forwarder(name, rawArgs)
		if err == nil {
			return res
		}
		var sent *AfterSendError
		if errors.As(err, &sent) {
			// A perda da resposta não gerava linha nenhuma. Agora gera uma (transport_ok=0) no proxy. Se o daemon
			// também gravou a execução, a chamada aparece duas vezes (docs/telemetry.md, "Desfecho").
			telemetry.Record(transportLostEntry(name, rawArgs, err, start))
			return ToolCallResult{IsError: true, Content: []ToolContent{{Type: "text", Text: "[FastExec] The call reached the daemon but no reply came back (" + err.Error() + "). It may have run: check its effect (job status, file contents) before repeating it."}}}
		}
	}
	return DispatchTool(name, rawArgs)
}

// cmdMsPtr: nil quando a chamada não rodou comando (o banco guarda -1 = não medido).
func cmdMsPtr(ms int64) *int64 {
	if ms < 0 {
		return nil
	}
	return &ms
}

// outcomePtr: o desfecho como ponteiro para AuditEntry.Outcome (nil = linha legada).
func outcomePtr(o telemetry.Outcome) *telemetry.Outcome { return &o }

func DispatchTool(name string, rawArgs json.RawMessage) (result ToolCallResult) {
	start := time.Now()
	rawArgs, notes, nerr := normalizeCall(name, rawArgs) // argnorm.go: nenhuma chave some em silêncio
	client, conversation := callerOf(rawArgs)
	bytesIn := len(rawArgs)
	if client != "" {
		bytesIn -= len(`,"_caller":""`) + len(client) // não conta os campos internos como entrada da LLM
	}
	if conversation != "" {
		bytesIn -= len(`,"_conversation":""`) + len(conversation)
	}
	host, summary := telemetry.SummarizeCall(name, rawArgs)
	if nerr != nil {
		telemetry.Record(telemetry.AuditEntry{
			Tool:           name,
			Host:           host,
			DurationMs:     time.Since(start).Milliseconds(),
			BytesIn:        bytesIn,
			BytesOut:       len(nerr.Error()),
			Success:        false,
			ExitCode:       1,
			ErrorMessage:   nerr.Error(),
			CommandSummary: summary,
			Client:         client,
			Conversation:   conversation,
			ArgsHash:       telemetry.ArgsHash(rawArgs),
			Marks:          "args-refused",
			Outcome:        outcomePtr(telemetry.InvalidOutcome()),
		})
		return ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: nerr.Error()}},
			IsError: true,
		}
	}

	defer func() {
		if r := recover(); r != nil {
			telemetry.EndCall()
			logger.Error("PANIC caught in tool %s: %v\nStack: %s", name, r, string(debug.Stack()))
			result = ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("[FATAL ERROR INTERCEPTED] Panic in tool %s: %v", name, r)}},
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
				Outcome:        outcomePtr(telemetry.PanicOutcome()),
			})
		}
	}()

	var outText string
	var err error

	telemetry.BeginCall()
	// Toda chamada com caminho local já diz em que repositório se está trabalhando: a indexação de fundo do grafo
	// começa aqui, em paralelo com a própria tool, e chega pronta para a consulta seguinte. Uma vez por repositório,
	// barata e silenciosa — quem decide é internal/graph (singleflight, teto de CPU, FASTEXEC_BG_INDEX=off).
	tools.KickBackgroundIndex(name, rawArgs)
	if h, ok := toolHandlers[name]; ok {
		outText, err = h(rawArgs)
	} else {
		if dynamicHandler := tools.GetDynamicHandler(name); dynamicHandler != nil {
			outText, err = dynamicHandler(rawArgs)
		} else {
			telemetry.EndCall()
			errMsg := fmt.Sprintf("Tool '%s' not found", name)
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
				Outcome:        outcomePtr(telemetry.NotFoundOutcome()),
			})
			return ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: errMsg}},
				IsError: true,
			}
		}
	}

	cmdMs := telemetry.CommandMs()
	savings := telemetry.EndCall()
	durationMs := time.Since(start).Milliseconds()
	bytesOut := len(outText)
	success := (err == nil)
	errMsg := ""
	exitCode := 0
	outcome := telemetry.SuccessOutcome(cmdMs >= 0)
	if err != nil {
		outcome = telemetry.FailureOutcome(err, outText) // antes de outText receber o texto do erro
		errMsg, exitCode = errorCause(err, outText)
		if outText == "" {
			outText = err.Error()
		}
	}

	docID := "conv:" + conversation
	if conversation == "" {
		docID = "chan:" + client
	}
	doc, docFrom, docHow, docLinks := trackDoc(docID, name, host, rawArgs, outText, success)

	telemetry.Record(telemetry.AuditEntry{
		Doc:            doc,
		DocFrom:        docFrom,
		DocHow:         docHow,
		DocLinks:       docLinks,
		Tool:           name,
		Host:           host,
		DurationMs:     durationMs,
		CmdMs:          cmdMsPtr(cmdMs),
		BytesIn:        bytesIn,
		BytesOut:       bytesOut,
		Success:        success,
		ExitCode:       exitCode,
		ErrorMessage:   errMsg,
		CommandSummary: summary,
		Client:         client,
		Conversation:   conversation,
		Graph:          graphDecision(outText),
		GraphSavedKB:   graphSavedKB(outText),
		ArgsHash:       telemetry.ArgsHash(rawArgs),
		OutHash:        telemetry.ShortHash(outText),
		Marks:          telemetry.Marks(outText, len(notes) > 0),
		MarkDefs:       telemetry.MarkDefs(outText),
		Truncated:      name == "exec" && tools.IsTruncated(outText),
		NoResult:       name == "exec" && tools.IsNoResult(outText),
		Savings:        savings,
		Outcome:        &outcome,
	})

	return ToolCallResult{
		// Diagnóstico de uma edição anterior que ficou pronto depois da resposta dela (validate.go, D5): sai UMA vez,
		// na próxima resposta de qualquer ferramenta.
		Content: []ToolContent{{Type: "text", Text: withNotes(name, notes, outText) + tools.TakeLate()}},
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
			logger.Error("JSON-RPC parse error: %v", err)
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		switch req.Method {
		case "initialize":
			s.setClient(req.Params)
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
			if n := tools.InactiveSchemaBytes(); n > 0 && config.Get().TelemetryOn() {
				go telemetry.RecordSaving(telemetry.Saving{Cat: telemetry.CatSchema, Bytes: n, Exact: true,
					Note: fmt.Sprintf("%d B of optional tool schemas were left out of this tool list; they load on demand", n)})
			}
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"tools": toolsList(s.clientNameSafe())},
			})

		case "tools/call":
			go func(r JSONRPCRequest) {
				var callParams struct {
					Name      string                     `json:"name"`
					Arguments json.RawMessage            `json:"arguments"`
					Meta      map[string]json.RawMessage `json:"_meta"`
				}
				if err := json.Unmarshal(r.Params, &callParams); err != nil {
					s.writeResponse(JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      r.ID,
						Error:   &RPCError{Code: -32602, Message: "Invalid params"},
					})
					return
				}

				result := s.dispatchTool(callParams.Name, withClientCwd(callParams.Name,
					withCaller(callParams.Arguments, s.callerID(), conversationOf(callParams.Meta)), s.clientCwd()))
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
		logger.Error("stdin read error: %v", err)
		return err
	}
	return nil
}
