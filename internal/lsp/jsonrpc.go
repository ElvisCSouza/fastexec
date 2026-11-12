// Package lsp implements a Language Server Protocol client over stdio.
//
// It speaks JSON-RPC 2.0 with the LSP Content-Length framing, manages one
// server process per (repository root, language), keeps documents in sync
// with the files on disk and exposes the usual navigation operations
// (references, definition, rename, diagnostics, ...).
//
// The package never downloads anything and never edits a file by itself:
// editing is done through ApplyWorkspaceEdit, which the caller invokes
// explicitly.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxMessageBytes caps a single inbound frame so a runaway or hostile
	// server cannot exhaust the daemon's memory.
	maxMessageBytes = 64 << 20
	// maxHeaderBytes caps the header block that precedes the blank line.
	maxHeaderBytes = 8 << 10
)

var (
	errConnClosed  = errors.New("lsp: connection closed")
	errMsgTooLarge = errors.New("lsp: message too large")
	errNoServer    = errors.New("lsp: no server")
)

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("lsp: %s (code %d)", e.Message, e.Code)
}

// rpcMessage is one framed JSON-RPC message in either direction. Fields are
// kept raw: the package forwards results to the caller untouched.
type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// conn is a JSON-RPC 2.0 connection. One goroutine reads frames and
// dispatches them; callers may call()/notify() from any goroutine.
type conn struct {
	w   io.Writer
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan *rpcMessage

	// onNotify handles server -> client notifications (e.g.
	// textDocument/publishDiagnostics).
	onNotify func(method string, params json.RawMessage)
	// onRequest handles server -> client requests (workspace/configuration,
	// client/registerCapability, ...). Returning a nil error answers with
	// result (which may be nil, encoded as null).
	onRequest func(method string, params json.RawMessage) (any, *rpcError)

	done      chan struct{}
	closeOnce sync.Once
	readErr   error
}

func newConn(w io.Writer, r io.Reader) *conn {
	c := &conn{
		w:       w,
		pending: make(map[int64]chan *rpcMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// readLoop reads frames until the reader fails, then fails every in-flight
// call so no caller blocks forever on a dead server.
func (c *conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	var err error
	for {
		var body []byte
		body, err = readFrame(br)
		if err != nil {
			break
		}
		var m rpcMessage
		if json.Unmarshal(body, &m) != nil {
			continue // ignore unparsable frame rather than killing the server
		}
		c.dispatch(&m)
	}
	c.closeWith(err)
}

// readFrame reads one Content-Length framed message. Header parsing is
// case-insensitive and tolerates extra headers (Content-Type).
func readFrame(br *bufio.Reader) ([]byte, error) {
	length := -1
	headerBytes := 0
	for {
		line, err := br.ReadString('\n')
		headerBytes += len(line)
		if err != nil {
			return nil, err
		}
		if headerBytes > maxHeaderBytes {
			return nil, errors.New("lsp: header block too large")
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, convErr := strconv.Atoi(strings.TrimSpace(value))
			if convErr != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length")
	}
	if length > maxMessageBytes {
		return nil, errMsgTooLarge
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *conn) dispatch(m *rpcMessage) {
	switch {
	case m.Method == "" && m.ID != nil:
		// Response to one of our calls.
		id, ok := decodeID(*m.ID)
		if !ok {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case m.Method != "" && m.ID != nil:
		// Server -> client request: answer so the server never blocks.
		go c.answer(*m)
	case m.Method != "":
		if c.onNotify != nil {
			c.onNotify(m.Method, m.Params)
		}
	}
}

func (c *conn) answer(m rpcMessage) {
	var (
		result any
		rerr   *rpcError
	)
	if c.onRequest != nil {
		result, rerr = c.onRequest(m.Method, m.Params)
	} else {
		rerr = &rpcError{Code: -32601, Message: "method not supported: " + m.Method}
	}
	var payload []byte
	if rerr != nil {
		payload, _ = json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": m.ID, "error": rerr,
		})
	} else {
		payload, _ = json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": m.ID, "result": result,
		})
	}
	_ = c.writeRaw(payload)
}

// decodeID turns a raw JSON id into an int64. String ids (server originated)
// are not used for our pending table, so they decode to ok=false.
func decodeID(raw json.RawMessage) (int64, bool) {
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

func (c *conn) writeRaw(payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := io.WriteString(c.w, header); err != nil {
		return err
	}
	_, err := c.w.Write(payload)
	return err
}

// notify sends a notification (no response expected).
func (c *conn) notify(method string, params any) error {
	select {
	case <-c.done:
		return c.err()
	default:
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	return c.writeRaw(payload)
}

// call sends a request and waits for its response, honouring ctx for both
// cancellation (a $/cancelRequest notification is sent) and timeout.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	select {
	case <-c.done:
		return nil, c.err()
	default:
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan *rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		c.forget(id)
		return nil, err
	}
	if err := c.writeRaw(payload); err != nil {
		c.forget(id)
		return nil, err
	}

	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		c.forget(id)
		// Best effort: tell the server to stop working on it. A server that
		// ignores the cancellation still gets its reply dropped.
		_ = c.notify("$/cancelRequest", map[string]any{"id": id})
		return nil, ctx.Err()
	case <-c.done:
		c.forget(id)
		return nil, c.err()
	}
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// closeWith tears the connection down once and wakes every waiter.
func (c *conn) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.readErr = err
		pending := c.pending
		c.pending = make(map[int64]chan *rpcMessage)
		c.mu.Unlock()
		close(c.done)
		for id, ch := range pending {
			select {
			case ch <- &rpcMessage{Error: &rpcError{Code: -32099, Message: "server exited"}}:
			default:
			}
			_ = id
		}
	})
}

func (c *conn) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr == nil {
		return errConnClosed
	}
	return c.readErr
}

// alive reports whether the read loop is still running.
func (c *conn) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// defaultTimeout is used when a call has no explicit deadline.
const defaultTimeout = 30 * time.Second
