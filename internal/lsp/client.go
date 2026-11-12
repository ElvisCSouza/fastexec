package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Position is a zero-based line/character pair, as used by LSP.
// Callers coming from the tools layer (one-based lines) must convert.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open span between two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range inside a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic is one problem reported by the server for a document.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// TextEdit replaces a range with new text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// DocumentSymbol is a flattened symbol entry.
type DocumentSymbol struct {
	Name      string `json:"name"`
	Kind      int    `json:"kind"`
	Range     Range  `json:"range"`
	Selection Range  `json:"selectionRange"`
	Detail    string `json:"detail,omitempty"`
}

// Hover carries the type/signature text at a position.
type Hover struct {
	Contents string
	Range    *Range
}

// CodeAction is one suggested fix.
type CodeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit  `json:"edit,omitempty"`
	Command     json.RawMessage `json:"command,omitempty"`
}

// FormattingOptions mirrors the LSP struct.
type FormattingOptions struct {
	TabSize                int  `json:"tabSize"`
	InsertSpaces           bool `json:"insertSpaces"`
	TrimTrailingWhitespace bool `json:"trimTrailingWhitespace,omitempty"`
	InsertFinalNewline     bool `json:"insertFinalNewline,omitempty"`
}

// Launch describes how to start a language server process.
type Launch struct {
	Name       string   // for logs and diagnostics, e.g. "gopls"
	LanguageID string   // languageId sent in didOpen, e.g. "go"
	Command    string   // absolute path to the executable
	Args       []string // extra arguments
	Env        []string // extra environment entries ("K=V")
	Root       string   // repository root; also the process working directory
	// InitializationOptions is sent verbatim as the `initializationOptions` of
	// initialize. Nil means "send none", which is what every server that needs
	// nothing gets. Some servers cannot start without it: the TypeScript one
	// does not run at all unless it is told where its tsserver is.
	InitializationOptions map[string]any
}

// Config is a Launch plus the runtime limits.
type Config struct {
	Launch
	// Timeout bounds one request. Zero means DefaultTimeout.
	Timeout time.Duration
	// HandshakeTimeout bounds initialize/shutdown. Zero means 20s.
	HandshakeTimeout time.Duration
	// StderrBytes caps the captured server log. Zero means 64 KiB.
	StderrBytes int
	// Logf, when set, receives lifecycle lines. Secrets are never logged.
	Logf func(format string, args ...any)
}

// DefaultTimeout bounds a single LSP request.
const DefaultTimeout = 30 * time.Second

// document is the client-side state of one open file. The FastExec side owns
// the file on disk; this struct records exactly what was last sent so a
// didChange is emitted only when the bytes really differ.
type document struct {
	path    string
	uri     string
	version int
	sent    []byte
}

// Client is one language server process for one repository root.
//
// It is safe for concurrent use. On a dead server the next call transparently
// restarts it (bounded by maxRestartsPerMinute), reseeding every open document
// from disk.
type Client struct {
	cfg  Config
	root string

	mu       sync.Mutex
	proc     *exec.Cmd
	conn     *conn
	launched bool
	docs     map[string]*document // uri -> state
	diags    map[string][]Diagnostic
	diagSeq  map[string]uint64
	caps     serverCapabilities
	restarts []time.Time

	lastUse time.Time

	stderr *ringBuffer

	// closed is closed by Close so background waiters stop.
	closed   chan struct{}
	closeOne sync.Once
}

// New starts the server and completes the initialize handshake. The caller
// owns the returned client and must Close it.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("lsp: %s: empty command", cfg.Name)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 20 * time.Second
	}
	if cfg.StderrBytes <= 0 {
		cfg.StderrBytes = 64 << 10
	}
	root := cfg.Root
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	c := &Client{
		cfg:     cfg,
		root:    abs,
		docs:    make(map[string]*document),
		diags:   make(map[string][]Diagnostic),
		diagSeq: make(map[string]uint64),
		stderr:  newRingBuffer(cfg.StderrBytes),
		lastUse: time.Now(),
		closed:  make(chan struct{}),
	}
	if err := c.start(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Root returns the absolute repository root this server was started for.
func (c *Client) Root() string { return c.root }

// Name returns the server name given in the launch config.
func (c *Client) Name() string { return c.cfg.Name }

// LanguageID returns the languageId this server owns.
func (c *Client) LanguageID() string { return c.cfg.LanguageID }

// ProjectWarning is the honesty line for a server that is running on a guessed
// project rather than on the project's own config, empty when there is nothing
// to warn about. It is asked of the client, not of the caller's repository root,
// because what matters is the root this server was actually started with.
func (c *Client) ProjectWarning() string {
	lang, ok := LanguageByID(c.cfg.LanguageID)
	if !ok {
		return ""
	}
	return ProjectWarning(lang, c.root)
}

// PID returns the OS process id, or 0 when not running.
func (c *Client) PID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == nil || c.proc.Process == nil {
		return 0
	}
	return c.proc.Process.Pid
}

// Stderr returns the captured server log (tail, bounded by StderrBytes).
func (c *Client) Stderr() string { return c.stderr.String() }

// Touch records that the client was just used, resetting the idle timer.
func (c *Client) Touch() {
	c.mu.Lock()
	c.lastUse = time.Now()
	c.mu.Unlock()
}

// Idle returns how long the client has been unused.
func (c *Client) Idle() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastUse)
}

func (c *Client) logf(format string, args ...any) {
	if c.cfg.Logf != nil {
		c.cfg.Logf(format, args...)
	}
}

// start spawns the process and runs the initialize/initialized handshake.
func (c *Client) start(ctx context.Context) error {
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	cmd.Dir = c.root
	cmd.Env = append(os.Environ(), c.cfg.Env...)
	cmd.SysProcAttr = sysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("lsp: %s: stdin: %w", c.cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("lsp: %s: stdout: %w", c.cfg.Name, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("lsp: %s: stderr: %w", c.cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("lsp: %s: start: %w", c.cfg.Name, err)
	}

	c.mu.Lock()
	c.proc = cmd
	c.launched = true
	c.stderr.reset()
	c.mu.Unlock()

	go c.drainStderr(stderrPipe)

	cn := newConn(stdin, stdout)
	cn.onNotify = c.handleNotify
	cn.onRequest = c.handleRequest

	c.mu.Lock()
	c.conn = cn
	c.mu.Unlock()

	hctx, cancel := context.WithTimeout(ctx, c.cfg.HandshakeTimeout)
	defer cancel()
	if err := c.handshake(hctx, cn); err != nil {
		cn.closeWith(err)
		c.kill()
		return fmt.Errorf("lsp: %s: initialize: %w", c.cfg.Name, err)
	}

	// Reap the process and mark the connection dead when it exits.
	go func() {
		werr := cmd.Wait()
		select {
		case <-c.closed:
			return
		default:
		}
		if werr != nil {
			c.logf("lsp: %s: server exited: %v", c.cfg.Name, werr)
		} else {
			c.logf("lsp: %s: server exited", c.cfg.Name)
		}
		cn.closeWith(fmt.Errorf("lsp: %s: server exited", c.cfg.Name))
	}()
	return nil
}

func (c *Client) drainStderr(r io.Reader) {
	buf := make([]byte, 8<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) kill() {
	c.mu.Lock()
	cmd := c.proc
	c.proc = nil
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	killProcessTree(cmd)
}

// Close shuts the server down cleanly, falling back to killing it.
func (c *Client) Close() {
	c.closeOne.Do(func() { close(c.closed) })
	c.mu.Lock()
	cn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if cn != nil && cn.alive() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := cn.call(ctx, "shutdown", nil)
		cancel()
		if err == nil {
			_ = cn.notify("exit", nil)
			// Give the server a moment to leave on its own.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && cn.alive() {
				time.Sleep(20 * time.Millisecond)
			}
		}
		cn.closeWith(errConnClosed)
	}
	c.kill()
}

// ensureConnected returns the live connection, restarting the server if it
// died. Restarts are rate limited so a server that crashes on every request
// cannot spin.
func (c *Client) ensureConnected(ctx context.Context) (*conn, error) {
	c.mu.Lock()
	cn := c.conn
	c.mu.Unlock()
	if cn != nil && cn.alive() {
		return cn, nil
	}

	c.mu.Lock()
	now := time.Now()
	kept := c.restarts[:0]
	for _, t := range c.restarts {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	c.restarts = kept
	tooMany := len(c.restarts) >= maxRestartsPerMinute
	if !tooMany {
		c.restarts = append(c.restarts, now)
	}
	c.mu.Unlock()
	if tooMany {
		return nil, fmt.Errorf("lsp: %s: restarting too often, giving up", c.cfg.Name)
	}

	c.logf("lsp: %s: restarting server", c.cfg.Name)
	c.kill()
	if err := c.start(ctx); err != nil {
		return nil, err
	}
	// Re-seed open documents from disk: the new process knows nothing.
	c.mu.Lock()
	docs := make([]*document, 0, len(c.docs))
	for _, d := range c.docs {
		d.sent = nil
		d.version = 0
		docs = append(docs, d)
	}
	cn = c.conn
	c.mu.Unlock()
	for _, d := range docs {
		if err := c.ensureOpen(ctx, d.path); err != nil {
			c.logf("lsp: %s: resync %s: %v", c.cfg.Name, d.path, err)
		}
	}
	return cn, nil
}

const maxRestartsPerMinute = 5

// call sends a request on the live connection with the per-call timeout.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, errConnClosed
	default:
	}
	cn, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	c.Touch()
	rctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	return cn.call(rctx, method, params)
}

// ---------------------------------------------------------------------------
// Handshake and server-originated messages
// ---------------------------------------------------------------------------

func (c *Client) handshake(ctx context.Context, cn *conn) error {
	rootURI := PathToURI(c.root)
	params := map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name": "fastexec",
		},
		"rootUri": rootURI,
		"workspaceFolders": []map[string]any{
			{"uri": rootURI, "name": filepath.Base(c.root)},
		},
		"capabilities": clientCapabilities(),
		"trace":        "off",
	}
	if len(c.cfg.InitializationOptions) > 0 {
		params["initializationOptions"] = c.cfg.InitializationOptions
	}
	raw, err := cn.call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var res struct {
		Capabilities struct {
			TextDocumentSync       json.RawMessage `json:"textDocumentSync"`
			ReferencesProvider     capFlag         `json:"referencesProvider"`
			DefinitionProvider     capFlag         `json:"definitionProvider"`
			ImplementationProvider capFlag         `json:"implementationProvider"`
			TypeDefinitionProvider capFlag         `json:"typeDefinitionProvider"`
			HoverProvider          capFlag         `json:"hoverProvider"`
			DocumentSymbolProvider capFlag         `json:"documentSymbolProvider"`
			RenameProvider         capFlag         `json:"renameProvider"`
			CodeActionProvider     capFlag         `json:"codeActionProvider"`
			FormattingProvider     capFlag         `json:"documentFormattingProvider"`
			DiagnosticProvider     json.RawMessage `json:"diagnosticProvider"`
			Workspace              struct {
				FileOperations struct {
					WillRename json.RawMessage `json:"willRename"`
				} `json:"fileOperations"`
			} `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	cap := res.Capabilities
	c.mu.Lock()
	c.caps = serverCapabilities{
		References:     cap.ReferencesProvider.present,
		Definition:     cap.DefinitionProvider.present,
		Implementation: cap.ImplementationProvider.present,
		TypeDefinition: cap.TypeDefinitionProvider.present,
		Hover:          cap.HoverProvider.present,
		DocumentSymbol: cap.DocumentSymbolProvider.present,
		Rename:         cap.RenameProvider.present,
		PrepareRename:  cap.RenameProvider.has("prepareProvider"),
		CodeAction:     cap.CodeActionProvider.present,
		Formatting:     cap.FormattingProvider.present,
		Diagnostics:    len(cap.DiagnosticProvider) > 0 && !bytes.Equal(bytes.TrimSpace(cap.DiagnosticProvider), []byte("null")),
		// willRenameFiles is the only thing that makes a *file* rename update
		// the imports pointing at it. Announced as either `true` or a filter
		// object; anything non-null means the server answers the request.
		WillRenameFiles: jsonPresent(cap.Workspace.FileOperations.WillRename),
	}
	c.mu.Unlock()

	return cn.notify("initialized", map[string]any{})
}

// handleNotify records server push notifications. publishDiagnostics is the
// only one that carries state FastExec consumes.
func (c *Client) handleNotify(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string       `json:"uri"`
		Version     *int         `json:"version"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil || p.URI == "" {
		return
	}
	key := uriKey(p.URI)
	c.mu.Lock()
	c.diags[key] = p.Diagnostics
	c.diagSeq[key]++
	c.mu.Unlock()
}

// handleRequest answers the handful of server -> client requests that a
// language server will block on. Answering "null" (or an empty list of the
// right length) is always better than not answering.
func (c *Client) handleRequest(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(params, &p) != nil || len(p.Items) == 0 {
			return []any{}, nil
		}
		out := make([]any, len(p.Items))
		return out, nil
	case "workspace/workspaceFolders":
		rootURI := PathToURI(c.root)
		return []map[string]any{{"uri": rootURI, "name": filepath.Base(c.root)}}, nil
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest":
		return nil, nil
	case "workspace/applyEdit":
		// FastExec never lets the server edit files behind its back; the
		// caller applies edits explicitly through ApplyWorkspaceEdit.
		return map[string]any{"applied": false, "failureReason": "applyEdit disabled"}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not supported: " + method}
	}
}

// capFlag decodes a capability that may be a bool or an options object.
type capFlag struct {
	present bool
	obj     map[string]json.RawMessage
}

func (f *capFlag) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var v bool
	if json.Unmarshal(b, &v) == nil {
		f.present = v
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) == nil {
		f.present = true
		f.obj = m
		return nil
	}
	return nil
}

func (f capFlag) has(key string) bool {
	if f.obj == nil {
		return false
	}
	raw, ok := f.obj[key]
	if !ok {
		return false
	}
	var v bool
	return json.Unmarshal(raw, &v) == nil && v
}

// serverCapabilities is the subset of server capabilities FastExec uses to
// decide whether an operation is worth sending.
type serverCapabilities struct {
	// WillRenameFiles is workspace.fileOperations.willRename: the server can
	// compute the edits a file rename implies (the imports that point at it).
	WillRenameFiles bool
	References      bool
	Definition     bool
	Implementation bool
	TypeDefinition bool
	Hover          bool
	DocumentSymbol bool
	Rename         bool
	PrepareRename  bool
	CodeAction     bool
	Formatting     bool
	Diagnostics    bool
}

func (c *Client) capabilities() serverCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Capabilities reports what the server told us it supports.
func (c *Client) Capabilities() serverCapabilities { return c.capabilities() }

// clientCapabilities is what FastExec advertises. Full-document sync is
// requested on purpose: it makes the daemon the single source of the bytes
// and removes a whole class of position-arithmetic bugs.
func clientCapabilities() map[string]any {
	return map[string]any{
		"workspace": map[string]any{
			"workspaceFolders": true,
			"configuration":    false,
			"applyEdit":        false,
			"workspaceEdit": map[string]any{
				"documentChanges": true,
				"resourceOperations": []string{
					"create", "rename", "delete",
				},
				"failureHandling": "abort",
			},
			"didChangeConfiguration": map[string]any{"dynamicRegistration": false},
			// Announcing willRename is what makes a server offer to update the
			// imports that point at a renamed file. A server that does not
			// support it simply never sends the request.
			"fileOperations": map[string]any{
				"willRename": map[string]any{
					"dynamicRegistration": false,
					"filters": []map[string]any{
						{
							"scheme": "file",
							"pattern": map[string]any{
								"glob":   "**/*.{ts,tsx,mts,cts,js,jsx,mjs,cjs}",
								"format": "relative",
							},
						},
					},
				},
			},
		},
		"textDocument": map[string]any{
			"synchronization": map[string]any{
				"dynamicRegistration": false,
				"willSave":            false,
				"willSaveWaitUntil":   false,
				"didSave":             false,
			},
			"publishDiagnostics": map[string]any{
				"relatedInformation": true,
				"versionSupport":     false,
			},
			"definition":     map[string]any{"linkSupport": false},
			"typeDefinition": map[string]any{"linkSupport": false},
			"implementation": map[string]any{"linkSupport": false},
			"references":     map[string]any{},
			"hover":          map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
			"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": false},
			"rename":         map[string]any{"prepareSupport": true},
			"codeAction":     map[string]any{"codeActionLiteralSupport": map[string]any{"codeActionKind": map[string]any{"valueSet": []string{"", "quickfix", "refactor", "source", "source.organizeImports", "source.addMissingImports"}}}},
			"formatting":     map[string]any{},
			"diagnostic":     map[string]any{"relatedDocumentSupport": false},
		},
		"window":  map[string]any{"workDoneProgress": false, "showMessage": map[string]any{}},
		"general": map[string]any{"positionEncodings": []string{"utf-16"}},
	}
}

// ---------------------------------------------------------------------------
// Document synchronisation
// ---------------------------------------------------------------------------

// ensureOpen makes sure the server has the current bytes of path. The file is
// always read from disk: FastExec is the writer, so disk is the truth.
func (c *Client) ensureOpen(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	uri := PathToURI(abs)
	key := uriKey(uri)

	c.mu.Lock()
	d := c.docs[key]
	c.mu.Unlock()

	if d == nil {
		d = &document{path: abs, uri: uri, version: 1, sent: content}
		c.mu.Lock()
		c.docs[key] = d
		c.mu.Unlock()
		return c.notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": c.cfg.LanguageID,
				"version":    d.version,
				"text":       string(content),
			},
		})
	}
	if bytes.Equal(d.sent, content) {
		return nil
	}
	c.mu.Lock()
	d.version++
	d.sent = content
	version := d.version
	c.mu.Unlock()
	return c.notify(ctx, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]any{{"text": string(content)}},
	})
}

// DefaultSettle bounds how long a caller waits for a freshly opened document to
// be analysed. It is generous because the first query in a cold repository pays
// for the whole index, and bounded because a query must still answer.
const DefaultSettle = 15 * time.Second

// ErrLoading means the server has not finished analysing the document, so an
// empty result would be "not known yet" rather than "nothing there". The caller
// degrades to the name-based graph instead of reporting an exact emptiness.
var ErrLoading = errors.New("lsp: server is still loading the project")

// WaitSettled blocks until the server has published diagnostics for path at
// least once, which is how every server signals it has analysed the document.
//
// A server answers references and definitions with an empty list while its
// workspace is still loading, and an empty list is indistinguishable from "this
// symbol really has no uses". Reporting the first as the second would be a lie,
// so any caller that is about to trust an empty answer must call this first and
// degrade on ErrLoading.
func (c *Client) WaitSettled(ctx context.Context, path string, settle time.Duration) error {
	if err := c.ensureOpen(ctx, path); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uri := uriKey(PathToURI(abs))
	if settle <= 0 {
		settle = DefaultSettle
	}
	deadline := time.Now().Add(settle)
	for {
		c.mu.Lock()
		// Any publication for this document means the server has looked at it.
		// The version is not compared: servers republish with the version they
		// analysed, and waiting for the newest one here would make a fast query
		// pay for a slow republish.
		seen := c.diagSeq[uri] > 0
		c.mu.Unlock()
		if seen {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrLoading
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closed:
			return ErrLoading
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// closeDoc tells the server to forget a document.
func (c *Client) closeDoc(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uri := PathToURI(abs)
	key := uriKey(uri)
	c.mu.Lock()
	_, ok := c.docs[key]
	delete(c.docs, key)
	delete(c.diags, key)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	return c.notify(ctx, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

// notify sends a notification without restarting a dead server: a lost
// notification is not worth a restart on its own.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	cn := c.conn
	c.mu.Unlock()
	if cn == nil || !cn.alive() {
		return errConnClosed
	}
	c.Touch()
	return cn.notify(method, params)
}

// ---------------------------------------------------------------------------
// Small helpers shared by the operations
// ---------------------------------------------------------------------------

// textDocumentPosition is the params shape used by most navigation requests.
func textDocumentPosition(path string, pos Position) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(abs)},
		"position":     pos,
	}, nil
}

// decodeLocations accepts both Location[] and LocationLink[] answers.
func decodeLocations(raw json.RawMessage) []Location {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var plain []Location
	if json.Unmarshal(raw, &plain) == nil && len(plain) > 0 {
		return plain
	}
	var links []struct {
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange Range  `json:"targetSelectionRange"`
	}
	if json.Unmarshal(raw, &links) == nil {
		out := make([]Location, 0, len(links))
		for _, l := range links {
			r := l.TargetSelectionRange
			if r == (Range{}) {
				r = l.TargetRange
			}
			out = append(out, Location{URI: l.TargetURI, Range: r})
		}
		return out
	}
	return nil
}

// sortLocations gives a stable, path-then-position order so results are
// deterministic across runs and servers.
func sortLocations(locs []Location) {
	sort.SliceStable(locs, func(i, j int) bool {
		if locs[i].URI != locs[j].URI {
			return locs[i].URI < locs[j].URI
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Character < locs[j].Range.Start.Character
	})
}

// PlainText renders hover contents (string | MarkedString | MarkedString[]).
func plainText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var marked struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &marked) == nil && marked.Value != "" {
		return marked.Value
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			if t := plainText(item); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
