package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/mcp"
	"homelab/fastexec/internal/transport"
)

const (
	Port           = 9146
	Addr           = "127.0.0.1:9146"
	DialTimeout    = 50 * time.Millisecond
	StartupTimeout = 500 * time.Millisecond
)

type DaemonRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type DaemonResponse struct {
	JSONRPC string              `json:"jsonrpc"`
	ID      any                 `json:"id,omitempty"`
	Result  *mcp.ToolCallResult `json:"result,omitempty"`
	Status  any                 `json:"status_result,omitempty"`
	Error   *RPCError           `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RunDaemon sobe o listener TCP do daemon residente em 127.0.0.1:9146.
func RunDaemon() error {
	transport.HandoffOnTimeout = true // o daemon vive além da chamada: exec que estoura vira job em vez de morrer
	l, err := net.Listen("tcp", Addr)
	if err != nil {
		return fmt.Errorf("daemon already running or port in use: %w", err)
	}
	defer l.Close()

	logger.Info("[daemon] FastExec local daemon listening on %s (PID %d)", Addr, os.Getpid())

	for {
		conn, err := l.Accept()
		if err != nil {
			logger.Error("[daemon] accept error: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req DaemonRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeResp(conn, DaemonResponse{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		switch req.Method {
		case "ping":
			writeResp(conn, DaemonResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Status: map[string]any{
					"status": "ok",
					"pid":    os.Getpid(),
					"exe":    self.Exe,
					"id":     self.ID,
				},
			})
		case "stop":
			writeResp(conn, DaemonResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Status:  map[string]any{"status": "stopping"},
			})
			go func() {
				time.Sleep(50 * time.Millisecond)
				os.Exit(0)
			}()
			return
		case "tools/call":
			var callParams struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &callParams); err != nil {
				writeResp(conn, DaemonResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &RPCError{Code: -32602, Message: "Invalid params"},
				})
				continue
			}
			result := mcp.DispatchTool(callParams.Name, callParams.Arguments)
			writeResp(conn, DaemonResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  &result,
			})
		default:
			writeResp(conn, DaemonResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32601, Message: "Method not found"},
			})
		}
	}
}

func writeResp(conn net.Conn, resp DaemonResponse) {
	data, _ := json.Marshal(resp)
	_, _ = conn.Write(append(data, '\n'))
}

// CallTool despacha uma tool para o daemon local (com auto-spawn transparente).
// Se o daemon não estiver acessível e não conseguir subir, retorna erro para fallback.
func CallTool(name string, args json.RawMessage) (*mcp.ToolCallResult, error) {
	conn, err := ensureDaemon()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	params, _ := json.Marshal(map[string]any{
		"name":      name,
		"arguments": args,
	})
	req := DaemonRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	}
	reqBytes, _ := json.Marshal(req)
	_ = conn.SetDeadline(time.Now().Add(55 * time.Second))
	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	var resp DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("daemon rpc error: %s", resp.Error.Message)
	}
	return resp.Result, nil
}

// PingDaemon verifica se o daemon está respondendo.
func PingDaemon() (int, error) {
	conn, err := net.DialTimeout("tcp", Addr, DialTimeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	req, _ := json.Marshal(DaemonRequest{JSONRPC: "2.0", ID: 1, Method: "ping"})
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return 0, err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return 0, err
	}

	var resp struct {
		Status struct {
			Status string `json:"status"`
			PID    int    `json:"pid"`
		} `json:"status_result"`
	}
	_ = json.Unmarshal(line, &resp)
	return resp.Status.PID, nil
}

// StopDaemon envia comando para o daemon encerrar suavemente.
func StopDaemon() error {
	conn, err := net.DialTimeout("tcp", Addr, DialTimeout)
	if err != nil {
		return fmt.Errorf("daemon is not running: %w", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(DaemonRequest{JSONRPC: "2.0", ID: 1, Method: "stop"})
	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
	_, err = conn.Write(append(req, '\n'))
	return err
}

func ensureDaemon() (net.Conn, error) {
	// 1. Tenta conectar direto
	conn, err := net.DialTimeout("tcp", Addr, DialTimeout)
	if err == nil {
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true)
		}
		return conn, nil
	}

	// 2. Não conseguiu: auto-spawn em background
	if err := spawnDaemon(); err != nil {
		return nil, err
	}

	// 3. Aguarda o daemon subir (polling até StartupTimeout)
	deadline := time.Now().Add(StartupTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
		conn, err = net.DialTimeout("tcp", Addr, DialTimeout)
		if err == nil {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				_ = tcpConn.SetNoDelay(true)
			}
			return conn, nil
		}
	}
	return nil, fmt.Errorf("daemon did not start in time: %w", err)
}
