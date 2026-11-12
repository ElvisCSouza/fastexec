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
	"time"
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
//	"loading"     announces a project load that never ends: the way csharp-ls
//	              behaves while a big solution is being read
//	"loading-once" announces the load and ends it shortly after
type fakeServer struct {
	out *bufio.Writer

	mu   sync.Mutex
	docs map[string]string // uri -> last text sent

	mode  string
	state string

	crashOnceUsed bool
	loadStarted   bool
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

// echo records what actually reached this process, so a test can assert that a
// launch configuration survived the trip: the arguments and extra environment
// the process was started with, and the initializationOptions carried inside
// the initialize request. Nothing is written unless FAKELSP_ECHO names a file.
//
// It is deliberately written from inside the child: an assertion made on the
// Config struct in the parent would pass even if the client never forwarded it.
func (s *fakeServer) echo(params json.RawMessage) {
	path := os.Getenv("FAKELSP_ECHO")
	if path == "" {
		return
	}
	var p struct {
		InitOptions json.RawMessage `json:"initializationOptions"`
		RootURI     string          `json:"rootUri"`
	}
	_ = json.Unmarshal(params, &p)
	b, err := json.MarshalIndent(map[string]any{
		"argv":                  os.Args,
		"rootUri":               p.RootURI,
		"initializationOptions": p.InitOptions,
		"env":                   os.Environ(),
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
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
		s.echo(m.Params)
		s.reply(*m.ID, map[string]any{"capabilities": fakeCapabilities()})
		return false
	case "initialized":
		s.startLoad()
		return false
	case "workspace/didChangeConfiguration", "$/setTrace":
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

// startLoad announces a project load the way a real C# server does: a
// $/progress "begin" whose title says what is being read. Two titles in
// sequence, because that is what csharp-ls sends (the solution, then its
// projects) and it is what makes "the last end opens the gate" testable.
func (s *fakeServer) startLoad() {
	if s.loadStarted {
		return
	}
	s.loadStarted = true
	switch s.mode {
	case "loading", "loading-once":
	default:
		return
	}
	s.begin("load-solution", "Loading solution demo.sln...")
	if s.mode != "loading-once" {
		return
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.begin("load-projects", "Loading 1 project(s)...")
		time.Sleep(150 * time.Millisecond)
		s.end("load-projects")
		s.end("load-solution")
	}()
}

func (s *fakeServer) begin(token, title string) {
	s.notify("$/progress", map[string]any{
		"token": token,
		"value": map[string]any{"kind": "begin", "title": title, "cancellable": false},
	})
}

func (s *fakeServer) end(token string) {
	s.notify("$/progress", map[string]any{"token": token, "value": map[string]any{"kind": "end"}})
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

	case "textDocument/inlayHint":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Range        Range                `json:"range"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s.reply(id, fakeInlayHints(s.text(p.TextDocument.URI), p.Range))

	case "textDocument/hover":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		text := s.text(p.TextDocument.URI)
		name, rng, ok := fakeIdentRange(text, p.Position)
		if !ok {
			s.reply(id, nil)
			return
		}
		s.reply(id, map[string]any{
			"contents": map[string]any{"kind": "markdown", "value": "```go\n" + fakeSignature(text, name) + "\n```"},
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
		s.reply(id, fakeSymbols(strings.Split(s.text(p.TextDocument.URI), "\n")))

	case "textDocument/prepareTypeHierarchy":
		var p fakePosParams
		_ = json.Unmarshal(m.Params, &p)
		item, ok := fakeHierarchyItem(strings.Split(s.text(p.TextDocument.URI), "\n"), p.TextDocument.URI, p.Position.Line)
		if !ok {
			s.reply(id, nil)
			return
		}
		s.reply(id, []map[string]any{item})

	case "typeHierarchy/supertypes":
		var p struct {
			Item struct {
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"item"`
		}
		_ = json.Unmarshal(m.Params, &p)
		lines := strings.Split(s.text(p.Item.URI), "\n")
		var out []map[string]any
		for _, base := range fakeEmbedded(lines, p.Item.Name) {
			if it, ok := fakeTypeItem(lines, p.Item.URI, base); ok {
				out = append(out, it)
			}
		}
		s.reply(id, out)

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
		"inlayHintProvider":          true,
		"documentSymbolProvider":     true,
		"typeHierarchyProvider":      true,
		"renameProvider":             map[string]any{"prepareProvider": true},
		"codeActionProvider":         true,
		"documentFormattingProvider": true,
		"diagnosticProvider":         map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false},
	}
	if os.Getenv("FAKELSP_NO_PULL") != "" {
		// Behave like a push-only server (gopls before pull diagnostics).
		delete(caps, "diagnosticProvider")
	}
	if os.Getenv("FAKELSP_NO_HINTS") != "" {
		// A server that does not know inlay hints: the answer has to come from hover instead.
		delete(caps, "inlayHintProvider")
	}
	if os.Getenv("FAKELSP_NO_HIERARCHY") != "" {
		// A server that does not resolve type hierarchies: "inherits" has no answer here, and the caller
		// has to degrade to what the source line says instead of pretending the server answered.
		delete(caps, "typeHierarchyProvider")
	}
	return caps
}

// fakeInlayHints answers the way a real server does: a hint sits right after the name of a declaration
// whose type is not written. Both legal label shapes appear on purpose — a plain string and the array of
// parts — because a client that understands only one of them loses half the hints.
func fakeInlayHints(text string, rng Range) []map[string]any {
	lines := strings.Split(text, "\n")
	first, last := rng.Start.Line, rng.End.Line
	if last < first {
		last = len(lines) - 1
	}
	out := []map[string]any{}
	for i := max(first, 0); i <= last && i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		name, rhs, ok := fakeShortDecl(line)
		if !ok {
			continue
		}
		col := strings.Index(line, name) + len(name)
		pos := map[string]any{"line": i, "character": col}
		if len(out)%2 == 0 {
			out = append(out, map[string]any{"position": pos, "label": ": " + fakeInferredType(rhs), "kind": 1})
			continue
		}
		out = append(out, map[string]any{
			"position": pos,
			"label":    []map[string]any{{"value": ": "}, {"value": fakeInferredType(rhs)}},
			"kind":     1,
		})
	}
	return out
}

// fakeSymbols is the hierarchical answer a real server gives: a type at the top with its members
// nested under it, and the free functions beside it. The nesting is the point — it is what tells the
// members of a type from the declarations next to it.
func fakeSymbols(lines []string) []map[string]any {
	// Método declarado FORA do bloco do tipo (o normal em Go) continua sendo membro do tipo do receiver:
	// é assim que um servidor de verdade aninha, e é o que faz "membros de Order" incluir Sum.
	byRecv := map[string][]map[string]any{}
	attached := map[int]bool{}
	for i, line := range lines {
		t := fakeTrim(line)
		name, isMethod, ok := fakeFuncDecl(t)
		if !ok || !isMethod {
			continue
		}
		recv, ok := fakeReceiver(t)
		if !ok {
			continue
		}
		node := fakeSymbolNode(name, 6, i, i, line, line, nil)
		node["detail"] = fakeFuncHead(t)
		byRecv[recv] = append(byRecv[recv], node)
		attached[i] = true
	}

	out := []map[string]any{}
	for i := 0; i < len(lines); i++ {
		trimmed := fakeTrim(lines[i])
		if name, kind, ok := fakeTypeDecl(trimmed); ok {
			end := i
			var kids []map[string]any
			for j := i + 1; j < len(lines); j++ {
				t := fakeTrim(lines[j])
				if t == "}" {
					end = j
					break
				}
				if k, ok := fakeMember(lines[j], j); ok {
					kids = append(kids, k)
				}
			}
			kids = append(kids, byRecv[name]...)
			out = append(out, fakeSymbolNode(name, kind, i, end, lines[i], lines[end], kids))
			i = end
			continue
		}
		if attached[i] {
			continue // já saiu como membro do tipo
		}
		if name, _, ok := fakeFuncDecl(trimmed); ok {
			out = append(out, fakeSymbolNode(name, 12, i, i, lines[i], lines[i], nil))
		}
	}
	return out
}

// fakeFuncHead é o texto de uma declaração sem o corpo — o "detail" que um servidor põe no símbolo.
func fakeFuncHead(t string) string {
	if i := strings.IndexByte(t, '{'); i > 0 {
		return strings.TrimSpace(t[:i])
	}
	return t
}

// fakeSymbolNode builds one node in the wire shape (DocumentSymbol: range + selectionRange + children).
func fakeSymbolNode(name string, kind, start, end int, at, endAt string, kids []map[string]any) map[string]any {
	col := strings.Index(at, name)
	if col < 0 {
		col = 0
	}
	node := map[string]any{
		"name": name, "kind": kind,
		"range": map[string]any{
			"start": map[string]any{"line": start, "character": 0},
			"end":   map[string]any{"line": end, "character": len(endAt)},
		},
		"selectionRange": map[string]any{
			"start": map[string]any{"line": start, "character": col},
			"end":   map[string]any{"line": start, "character": col + len(name)},
		},
	}
	if len(kids) > 0 {
		node["children"] = kids
	}
	return node
}

func fakeTrim(line string) string { return strings.TrimSpace(strings.TrimRight(line, "\r")) }

// fakeTypeDecl recognizes `type Nome struct {` / `type Nome interface {` and answers the symbol kind a
// real server uses for each (Struct = 23, Interface = 11).
func fakeTypeDecl(trimmed string) (string, int, bool) {
	if !strings.HasPrefix(trimmed, "type ") {
		return "", 0, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 3 {
		return "", 0, false
	}
	switch {
	case strings.HasPrefix(fields[2], "struct"):
		return fields[1], 23, true
	case strings.HasPrefix(fields[2], "interface"):
		return fields[1], 11, true
	}
	return "", 0, false
}

// fakeFuncDecl answers the name of a `func ...` line and whether it is a method (the receiver comes
// first) rather than a free function.
func fakeFuncDecl(trimmed string) (string, bool, bool) {
	if !strings.HasPrefix(trimmed, "func ") {
		return "", false, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", false, false
	}
	rest := strings.TrimSpace(trimmed[len("func "):])
	isMethod := false
	if strings.HasPrefix(rest, "(") { // método: func (r T) Nome(...)
		close := strings.IndexByte(rest, ')')
		if close < 0 {
			return "", true, false
		}
		rest, isMethod = strings.TrimSpace(rest[close+1:]), true
	}
	name := rest
	if i := strings.IndexAny(rest, "(["); i >= 0 {
		name = rest[:i] // "Nome(...)" → "Nome"; o que vem depois é assinatura, não nome
	}
	name = strings.TrimSpace(name)
	if name == "" || !fakeIdent(name) {
		return "", isMethod, false
	}
	return name, isMethod, true
}

// fakeMember is one member inside a type block: a method (kind 6) or a field / embedded type (kind 8).
func fakeMember(line string, idx int) (map[string]any, bool) {
	trimmed := fakeTrim(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "}") {
		return nil, false
	}
	if name, ok := fakeMethodLine(trimmed); ok {
		node := fakeSymbolNode(name, 6, idx, idx, line, line, nil)
		node["detail"] = trimmed
		return node, true
	}
	fields := strings.Fields(trimmed)
	name := fields[0]
	node := fakeSymbolNode(name, 8, idx, idx, line, line, nil)
	if len(fields) > 1 {
		node["detail"] = strings.Join(fields[1:], " ")
	}
	return node, true
}

// fakeMethodLine reconhece um método de bloco (`Nome(args) resultado`): a interface declara os seus assim,
// e um servidor os reporta como método (kind 6), não como campo.
func fakeMethodLine(trimmed string) (string, bool) {
	open := strings.IndexByte(trimmed, '(')
	if open <= 0 || !strings.Contains(trimmed[open:], ")") {
		return "", false
	}
	name := strings.TrimSpace(trimmed[:open])
	if !fakeIdent(name) {
		return "", false
	}
	return name, true
}

// fakeHierarchyItem answers the type the position belongs to: the type declared on the line, the type
// whose block contains the line, or the receiver of the method declared there.
func fakeHierarchyItem(lines []string, uri string, line int) (map[string]any, bool) {
	if line < 0 || line >= len(lines) {
		return nil, false
	}
	trimmed := fakeTrim(lines[line])
	if name, _, ok := fakeTypeDecl(trimmed); ok {
		return fakeTypeItem(lines, uri, name)
	}
	if name, isMethod, ok := fakeFuncDecl(trimmed); ok && isMethod {
		_ = name
		if recv, ok := fakeReceiver(trimmed); ok {
			return fakeTypeItem(lines, uri, recv)
		}
	}
	// Dentro de um bloco: o tipo é o que abriu o bloco ainda aberto mais próximo acima.
	for i := line - 1; i >= 0; i-- {
		t := fakeTrim(lines[i])
		if t == "}" {
			return nil, false
		}
		if name, _, ok := fakeTypeDecl(t); ok {
			return fakeTypeItem(lines, uri, name)
		}
	}
	return nil, false
}

// fakeReceiver reads the receiver type out of `func (r *T) M()`.
func fakeReceiver(trimmed string) (string, bool) {
	open := strings.IndexByte(trimmed, '(')
	close := strings.IndexByte(trimmed, ')')
	if open < 0 || close < open {
		return "", false
	}
	fields := strings.Fields(trimmed[open+1 : close])
	if len(fields) == 0 {
		return "", false
	}
	return strings.TrimPrefix(fields[len(fields)-1], "*"), true
}

// fakeTypeItem is the hierarchy item of a named type in the file, or false when the file has no such type.
func fakeTypeItem(lines []string, uri, name string) (map[string]any, bool) {
	for i, line := range lines {
		n, kind, ok := fakeTypeDecl(fakeTrim(line))
		if !ok || n != name {
			continue
		}
		end := i
		for j := i + 1; j < len(lines); j++ {
			if fakeTrim(lines[j]) == "}" {
				end = j
				break
			}
		}
		col := strings.Index(line, name)
		return map[string]any{
			"name": name, "kind": kind, "uri": uri,
			"range": map[string]any{
				"start": map[string]any{"line": i, "character": 0},
				"end":   map[string]any{"line": end, "character": len(lines[end])},
			},
			"selectionRange": map[string]any{
				"start": map[string]any{"line": i, "character": col},
				"end":   map[string]any{"line": i, "character": col + len(name)},
			},
		}, true
	}
	return nil, false
}

// fakeEmbedded lists the types embedded in `type <name> struct {` — one identifier alone on the line,
// which is what Go calls a base type.
func fakeEmbedded(lines []string, name string) []string {
	var out []string
	inside := false
	for _, line := range lines {
		t := fakeTrim(line)
		if n, _, ok := fakeTypeDecl(t); ok {
			inside = n == name
			continue
		}
		if !inside {
			continue
		}
		if t == "}" {
			inside = false
			continue
		}
		if fields := strings.Fields(t); len(fields) == 1 && fakeIdent(fields[0]) {
			out = append(out, fields[0])
		}
	}
	return out
}

// fakeSignature is what a server puts in a hover: the declaration of the symbol, with its types resolved.
// The tools read a signature out of this (code_graph def) and a type out of it (the "types:" line), so the
// fake has to answer in the shape a real server answers: the declared line for a func or a type, and
// "var nome tipo" for a name whose type was only inferred.
func fakeSignature(text, name string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		t := fakeTrim(line)
		if n, _, ok := fakeTypeDecl(t); ok && n == name {
			return strings.TrimSpace(strings.TrimSuffix(t, "{"))
		}
		if n, _, ok := fakeFuncDecl(t); ok && n == name {
			if i := strings.IndexByte(t, '{'); i > 0 {
				return strings.TrimSpace(t[:i])
			}
			return t
		}
	}
	for _, line := range lines {
		if n, rhs, ok := fakeShortDecl(line); ok && n == name {
			return "var " + name + " " + fakeInferredType(rhs)
		}
		if n, rhs, ok := fakeVarDecl(line); ok && n == name {
			return "var " + name + " " + fakeInferredType(rhs)
		}
	}
	return "var " + name + " string"
}

// fakeVarDecl recognizes `var nome = ...` / `const nome = ...` — a declaration that does not write the type.
func fakeVarDecl(line string) (string, string, bool) {
	t := fakeTrim(line)
	for _, kw := range []string{"var ", "const "} {
		rest, ok := strings.CutPrefix(t, kw)
		if !ok {
			continue
		}
		name, rhs, hasEq := strings.Cut(rest, "=")
		name = strings.TrimSpace(name)
		if !hasEq || name == "" || strings.ContainsAny(name, " \t") {
			return "", "", false // "var x int = 1" escreve o tipo: não é candidato a inferência
		}
		return name, strings.TrimSpace(rhs), true
	}
	return "", "", false
}

// fakeIdent reports whether the text is a single Go-style identifier — the shape of an embedded field.
func fakeIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// fakeShortDecl is the fake's idea of a declaration with an inferred type: `nome := <rhs>`.
func fakeShortDecl(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return "", "", false
	}
	name, rhs, ok := strings.Cut(trimmed, ":=")
	name = strings.TrimSpace(name)
	if !ok || name == "" || strings.ContainsAny(name, " \t(){}[]") {
		return "", "", false
	}
	return name, strings.TrimSpace(rhs), true
}

// fakeInferredType turns a right-hand side into the type a server would report, deterministically.
func fakeInferredType(rhs string) string {
	switch {
	case strings.HasPrefix(rhs, `"`), strings.HasPrefix(rhs, "`"):
		return "string"
	case rhs != "" && rhs[0] >= '0' && rhs[0] <= '9':
		return "int64"
	case strings.HasPrefix(rhs, "[]"):
		if i := strings.IndexByte(rhs, '{'); i > 0 {
			return rhs[:i]
		}
	}
	if i := strings.IndexAny(rhs, "("); i > 0 {
		return rhs[:i] // uma chamada: o fake devolve o nome da função, como um tipo nomeado
	}
	return "interface{}"
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
