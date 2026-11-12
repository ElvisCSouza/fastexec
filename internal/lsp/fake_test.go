package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestMain doubles as the fake language server.
//
// With FAKELSP_HELPER=1 the test binary is not a test run at all: it becomes a
// server on stdin/stdout. The fake is therefore a real process speaking the
// protocol over real pipes — it exercises the framing, the process lifecycle
// and the restart path for real, instead of stubbing them out in-process.
// Framing is re-implemented here on purpose: a fake that shared the client's
// reader could not catch a framing bug.
func TestMain(m *testing.M) {
	if os.Getenv("FAKELSP_HELPER") == "1" {
		runFakeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeServer is the test language server. Modes are selected with FAKELSP_MODE:
//
//	""            normal
//	"timeout"     answers the handshake, then never answers a request again
//	"crash"       exits as soon as the first operation request arrives
//	"crash-once"  exits on the first operation request, then behaves normally
//	              when restarted (the marker file is FAKELSP_STATE)
type fakeServer struct {
	out *bufio.Writer

	mu   sync.Mutex
	docs map[string]string // uri -> last text sent

	mode  string
	state string

	crashOnceUsed bool
}

func runFakeServer() {
	s := &fakeServer{
		out:   bufio.NewWriter(os.Stdout),
		docs:  make(map[string]string),
		mode:  os.Getenv("FAKELSP_MODE"),
		state: os.Getenv("FAKELSP_STATE"),
	}
	if s.mode == "crash-once" && s.state != "" {
		if _, err := os.Stat(s.state); err == nil {
			s.mode = "" // a previous incarnation already crashed
		}
	}
	// stderr is captured by the client; the ready line is what the diagnostic
	// test looks for.
	fmt.Fprintln(os.Stderr, "fake-lsp ready")
	if extra := os.Getenv("FAKELSP_STDERR"); extra != "" {
		fmt.Fprintln(os.Stderr, extra)
	}

	in := bufio.NewReader(os.Stdin)
	for {
		body, err := readFakeFrame(in)
		if err != nil {
			return
		}
		var m rpcMessage
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if s.handle(&m) {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Framing (independent of the client's)
// ---------------------------------------------------------------------------

func readFakeFrame(br *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("fake-lsp: no Content-Length")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (s *fakeServer) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(b))
	s.out.Write(b)
	s.out.Flush()
}

func (s *fakeServer) reply(id json.RawMessage, result any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *fakeServer) replyErr(id json.RawMessage, code int, msg string) {
	s.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	})
}

func (s *fakeServer) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

func (s *fakeServer) handle(m *rpcMessage) (exit bool) {
	switch m.Method {
	case "initialize":
		s.reply(*m.ID, map[string]any{"capabilities": fakeCapabilities()})
		return false
	case "initialized", "workspace/didChangeConfiguration", "$/setTrace":
		return false
	case "shutdown":
		if m.ID != nil {
			s.reply(*m.ID, nil)
		}
		return false
	case "exit":
		return true
	case "textDocument/didOpen", "textDocument/didChange":
		s.didOpen(m.Params)
		return false
	case "textDocument/didClose":
		s.didClose(m.Params)
		return false
	}

	if m.ID == nil {
		return false // a notification we do not model
	}

	switch s.mode {
	case "timeout":
		// Answer nothing: the caller must time out, not hang.
		return false
	case "crash":
		os.Exit(1)
	case "crash-once":
		if !s.crashOnceUsed {
			s.crashOnceUsed = true
			if s.state != "" {
				_ = os.WriteFile(s.state, []byte("crashed"), 0o644)
			}
			os.Exit(1)
		}
	}

	s.operation(m)
	return false
}

func (s *fakeServer) operation(m *rpcMessage) {
	id := *m.ID
	switch m.Method {
	case "textDocument/references":
		var p fakePosParams
		if json.Unmarshal(m.Params, &p) != nil {
			s.replyErr(id, -32602, "bad params")
			return
		}
		uri := p.TextDocument.URI
		text := s.text(uri)
		occ := fakeOccurrences(text, fakeIdentAt(text, p.Position))
		if len(occ) > 0 && !p.Context.IncludeDeclaration {
			occ = occ[1:] // the first occurrence is the declaration
		}
		out := make([]map[string]any, 0, len(occ))
		for _, o := range occ {
			out = append(out, map[string]any{"uri": uri, "range": o})
		}
		s.reply(id, out)

	case "textDocument/definition":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		occ := fakeOccurrences(s.text(p.TextDocument.URI), fakeIdentAt(s.text(p.TextDocument.URI), p.Position))
		if len(occ) == 0 {
			s.reply(id, nil)
			return
		}
		s.reply(id, []map[string]any{{"uri": p.TextDocument.URI, "range": occ[0]}})

	case "textDocument/prepareRename":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		text := s.text(p.TextDocument.URI)
		name, rng, ok := fakeIdentRange(text, p.Position)
		if !ok {
			s.reply(id, nil)
			return
		}
		s.reply(id, map[string]any{"range": rng, "placeholder": name})

	case "textDocument/rename":
		var p struct {
			fakePosParams
			NewName string `json:"newName"`
		}
		_ = json.Unmarshal(m.Params, &p)
		text := s.text(p.TextDocument.URI)
		name, _, ok := fakeIdentRange(text, p.Position)
		if !ok {
			s.reply(id, nil)
			return
		}
		edits := []map[string]any{}
		for _, o := range fakeOccurrences(text, name) {
			edits = append(edits, map[string]any{"range": o, "newText": p.NewName})
		}
		s.reply(id, map[string]any{"changes": map[string]any{p.TextDocument.URI: edits}})

	case "textDocument/hover":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		name, rng, ok := fakeIdentRange(s.text(p.TextDocument.URI), p.Position)
		if !ok {
			s.reply(id, nil)
			return
		}
		s.reply(id, map[string]any{
			"contents": map[string]any{"kind": "markdown", "value": "```go\nvar " + name + " string\n```"},
			"range":    rng,
		})

	case "textDocument/implementation", "textDocument/typeDefinition":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		occ := fakeOccurrences(s.text(p.TextDocument.URI), fakeIdentAt(s.text(p.TextDocument.URI), p.Position))
		if len(occ) == 0 {
			s.reply(id, nil)
			return
		}
		s.reply(id, []map[string]any{{"uri": p.TextDocument.URI, "range": occ[0]}})

	case "textDocument/documentSymbol":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(m.Params, &p)
		lines := strings.Split(s.text(p.TextDocument.URI), "\n")
		syms := []map[string]any{}
		for i, line := range lines {
			trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
			if !strings.HasPrefix(trimmed, "func ") {
				continue
			}
			name := strings.TrimSuffix(strings.Fields(trimmed)[1], "()")
			r := map[string]any{
				"start": map[string]any{"line": i, "character": 0},
				"end":   map[string]any{"line": i, "character": len(line)},
			}
			syms = append(syms, map[string]any{
				"name": name, "kind": 12, "range": r, "selectionRange": r,
			})
		}
		s.reply(id, syms)

	case "textDocument/formatting":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(m.Params, &p)
		lines := strings.Split(s.text(p.TextDocument.URI), "\n")
		edits := []map[string]any{}
		for i, line := range lines {
			trimmed := strings.TrimRight(line, " \t\r")
			if trimmed == line {
				continue
			}
			edits = append(edits, map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": i, "character": len(trimmed)},
					"end":   map[string]any{"line": i, "character": len(line)},
				},
				"newText": "",
			})
		}
		s.reply(id, edits)

	case "textDocument/codeAction":
		s.reply(id, []map[string]any{{
			"title": "Organize Imports",
			"kind":  "source.organizeImports",
			"edit":  map[string]any{"changes": map[string]any{}},
		}})

	case "textDocument/diagnostic":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s.reply(id, map[string]any{
			"kind":  "full",
			"items": fakeDiagnostics(s.text(p.TextDocument.URI)),
		})

	default:
		s.replyErr(id, -32601, "fake-lsp: unsupported "+m.Method)
	}
}

// ---------------------------------------------------------------------------
// Documents
// ---------------------------------------------------------------------------

type fakePosParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
	Context  struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

func (s *fakeServer) didOpen(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		return
	}
	text := p.TextDocument.Text
	if len(p.ContentChanges) > 0 {
		text = p.ContentChanges[len(p.ContentChanges)-1].Text
	}
	s.mu.Lock()
	s.docs[p.TextDocument.URI] = text
	s.mu.Unlock()
	// Push diagnostics, the way a real server does after a change.
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": fakeDiagnostics(text),
	})
}

func (s *fakeServer) didClose(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	delete(s.docs, p.TextDocument.URI)
	s.mu.Unlock()
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri": p.TextDocument.URI, "diagnostics": []any{},
	})
}

// text returns the document as the server sees it: the text most recently sent
// for an open document, or the file on disk otherwise.
func (s *fakeServer) text(uri string) string {
	s.mu.Lock()
	t, ok := s.docs[uri]
	s.mu.Unlock()
	if ok {
		return t
	}
	b, err := os.ReadFile(URItoPath(uri))
	if err != nil {
		return ""
	}
	return string(b)
}

func fakeCapabilities() map[string]any {
	caps := map[string]any{
		"textDocumentSync":           1,
		"referencesProvider":         true,
		"definitionProvider":         true,
		"implementationProvider":     true,
		"typeDefinitionProvider":     true,
		"hoverProvider":              true,
		"documentSymbolProvider":     true,
		"renameProvider":             map[string]any{"prepareProvider": true},
		"codeActionProvider":         true,
		"documentFormattingProvider": true,
		"diagnosticProvider":         map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false},
	}
	if os.Getenv("FAKELSP_NO_PULL") != "" {
		// Behave like a push-only server (gopls before pull diagnostics).
		delete(caps, "diagnosticProvider")
	}
	return caps
}

func fakeDiagnostics(text string) []map[string]any {
	out := []map[string]any{}
	for i, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "TODO") {
			continue
		}
		content := strings.TrimRight(line, "\r")
		out = append(out, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": i, "character": 0},
				"end":   map[string]any{"line": i, "character": len(content)},
			},
			"severity": 2,
			"source":   "fake-lsp",
			"message":  "TODO left in code",
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Text helpers (ASCII fixtures; positions are treated as byte offsets in a
// line, which matches UTF-16 code units for the ASCII the tests use)
// ---------------------------------------------------------------------------

func fakeIdentChar(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func fakeOffsetAt(text string, pos Position) (int, bool) {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return 0, false
	}
	off := 0
	for i := 0; i < pos.Line; i++ {
		off += len(lines[i]) + 1
	}
	line := lines[pos.Line]
	if pos.Character < 0 || pos.Character > len(line) {
		return 0, false
	}
	return off + pos.Character, true
}

func fakeLineChar(text string, off int) Position {
	line := 0
	start := 0
	for i := 0; i < len(text) && i < off; i++ {
		if text[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return Position{Line: line, Character: off - start}
}

// fakeIdentAt is the identifier under a position, used as "the symbol being asked
// about" by the navigation handlers.
func fakeIdentAt(text string, pos Position) string {
	name, _, _ := fakeIdentRange(text, pos)
	return name
}

func fakeIdentRange(text string, pos Position) (string, Range, bool) {
	off, ok := fakeOffsetAt(text, pos)
	if !ok {
		return "", Range{}, false
	}
	// A cursor sitting just after the identifier still refers to it.
	if off >= len(text) || !fakeIdentChar(text[off]) {
		if off > 0 && fakeIdentChar(text[off-1]) {
			off--
		} else {
			return "", Range{}, false
		}
	}
	start := off
	for start > 0 && fakeIdentChar(text[start-1]) {
		start--
	}
	end := off
	for end < len(text) && fakeIdentChar(text[end]) {
		end++
	}
	if start == end {
		return "", Range{}, false
	}
	return text[start:end], Range{Start: fakeLineChar(text, start), End: fakeLineChar(text, end)}, true
}

// fakeOccurrences finds every whole-word occurrence of name.
func fakeOccurrences(text, name string) []Range {
	if name == "" {
		return nil
	}
	out := []Range{}
	for i := 0; i+len(name) <= len(text); {
		j := strings.Index(text[i:], name)
		if j < 0 {
			break
		}
		at := i + j
		before := at == 0 || !fakeIdentChar(text[at-1])
		after := at+len(name) >= len(text) || !fakeIdentChar(text[at+len(name)])
		if before && after {
			out = append(out, Range{Start: fakeLineChar(text, at), End: fakeLineChar(text, at+len(name))})
		}
		i = at + len(name)
	}
	return out
}

// writeFixture writes a Go file and returns its path.
func writeFixture(t *testing.T, dir, name, body string) string {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
