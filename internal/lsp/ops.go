package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrUnsupported means the server did not advertise the capability needed for
// the requested operation. Callers degrade to the name-based graph, so this is
// never a hard failure.
var ErrUnsupported = errors.New("lsp: server does not support this operation")

// References returns every place the symbol at pos is used.
//
// includeDecl controls whether the declaration itself appears in the result;
// callers that want "who calls this" pass false.
func (c *Client) References(ctx context.Context, path string, pos Position, includeDecl bool) ([]Location, error) {
	if !c.capabilities().References {
		return nil, ErrUnsupported
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, err
	}
	params["context"] = map[string]any{"includeDeclaration": includeDecl}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/references", params)
	if err != nil {
		return nil, err
	}
	locs := decodeLocations(raw)
	sortLocations(locs)
	return locs, nil
}

// Definition returns the declaration site(s) of the symbol at pos.
func (c *Client) Definition(ctx context.Context, path string, pos Position) ([]Location, error) {
	return c.navigation(ctx, path, pos, "textDocument/definition", c.capabilities().Definition)
}

// Implementation returns the concrete implementations of the interface/abstract
// member at pos.
func (c *Client) Implementation(ctx context.Context, path string, pos Position) ([]Location, error) {
	return c.navigation(ctx, path, pos, "textDocument/implementation", c.capabilities().Implementation)
}

// TypeDefinition returns where the *type* of the symbol at pos is declared.
func (c *Client) TypeDefinition(ctx context.Context, path string, pos Position) ([]Location, error) {
	return c.navigation(ctx, path, pos, "textDocument/typeDefinition", c.capabilities().TypeDefinition)
}

func (c *Client) navigation(ctx context.Context, path string, pos Position, method string, ok bool) ([]Location, error) {
	if !ok {
		return nil, ErrUnsupported
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, err
	}
	raw, err := c.requestWithDoc(ctx, path, method, params)
	if err != nil {
		return nil, err
	}
	locs := decodeLocations(raw)
	sortLocations(locs)
	return locs, nil
}

// HoverAt returns the type/signature text shown at pos.
func (c *Client) HoverAt(ctx context.Context, path string, pos Position) (*Hover, error) {
	if !c.capabilities().Hover {
		return nil, ErrUnsupported
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, err
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/hover", params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
		Range    *Range          `json:"range"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	text := plainText(h.Contents)
	if text == "" {
		return nil, nil
	}
	return &Hover{Contents: text, Range: h.Range}, nil
}

// InlayHints returns the annotations the server suggests inside rng, in the
// order the server listed them. The usual one is the type it inferred for a
// declaration that does not spell it out ("x := ...", "var x = ...").
func (c *Client) InlayHints(ctx context.Context, path string, rng Range) ([]InlayHint, error) {
	if !c.capabilities().InlayHint {
		return nil, ErrUnsupported
	}
	abs, err := absOf(path)
	if err != nil {
		return nil, err
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/inlayHint", map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(abs)},
		"range":        rng,
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var hints []struct {
		Position Position        `json:"position"`
		Label    json.RawMessage `json:"label"`
		Kind     int             `json:"kind"`
	}
	if err := json.Unmarshal(raw, &hints); err != nil {
		return nil, err
	}
	out := make([]InlayHint, 0, len(hints))
	for _, h := range hints {
		label := hintLabel(h.Label)
		if label == "" {
			continue
		}
		out = append(out, InlayHint{Position: h.Position, Label: label, Kind: h.Kind})
	}
	return out, nil
}

// hintLabel flattens the two shapes the protocol allows for an inlay hint
// label: a plain string, or a list of parts whose text is either a string or a
// nested {value} object.
func hintLabel(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		var text string
		if json.Unmarshal(p.Value, &text) != nil {
			var nested struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(p.Value, &nested) != nil {
				continue
			}
			text = nested.Value
		}
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
}

// DocumentSymbols lists the symbols of one file, flattened depth first so
// callers see a single deterministic list. Each entry keeps what the server
// nested under it in Children.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	tree, err := c.DocumentSymbolsTree(ctx, path)
	if err != nil || len(tree) == 0 {
		return nil, err
	}
	return flattenSymbols(tree), nil
}

// DocumentSymbolsTree is DocumentSymbols without the flattening: the tree is
// what tells a type's members from the functions next to it, and the server is
// the only one who knows which is which.
func (c *Client) DocumentSymbolsTree(ctx context.Context, path string) ([]DocumentSymbol, error) {
	if !c.capabilities().DocumentSymbol {
		return nil, ErrUnsupported
	}
	abs, err := absOf(path)
	if err != nil {
		return nil, err
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(abs)},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	nodes := raw2nodes(raw)
	var out []DocumentSymbol
	for _, n := range nodes {
		if sym, ok := symbolNode(n); ok {
			out = append(out, sym)
		}
	}
	return nestReceiverMethods(out), nil
}

// nestReceiverMethods recoloca sob o tipo os métodos que o servidor lista soltos no arquivo.
//
// O gopls lista `func (o Order) Read()` na RAIZ do arquivo, com o nome "(Order).Read": na hierarquia dele o
// método não é filho de Order, mas É um membro de Order — e quem pergunta "o que este tipo tem dentro"
// espera vê-lo. Sem isto, os membros de um tipo Go sairiam só com os campos, que é a metade menos
// interessante da resposta.
//
// O nome do dono é casado com o nome do símbolo do tipo no MESMO arquivo. Um nome que não casa (tipo de
// outro arquivo, genérico instanciado) fica solto onde o servidor o pôs: continua sendo um símbolo do
// arquivo, e inventar um dono seria inventar hierarquia.
func nestReceiverMethods(nodes []DocumentSymbol) []DocumentSymbol {
	idx := map[string]int{}
	out := make([]DocumentSymbol, 0, len(nodes))
	for _, n := range nodes {
		if _, _, ok := receiverName(n.Name); ok {
			continue
		}
		idx[n.Name] = len(out)
		out = append(out, n)
	}
	for _, n := range nodes {
		owner, method, ok := receiverName(n.Name)
		if !ok {
			continue
		}
		i, found := idx[owner]
		if !found {
			out = append(out, n)
			continue
		}
		m := n
		m.Name = method
		out[i].Children = append(out[i].Children, m)
	}
	return out
}

// receiverName reconhece o nome que o servidor dá a um método de receptor: "(Order).Read" -> "Order",
// "Read", true.
func receiverName(name string) (string, string, bool) {
	if !strings.HasPrefix(name, "(") {
		return "", "", false
	}
	i := strings.Index(name, ")")
	if i < 0 || i+1 >= len(name) || name[i+1] != '.' {
		return "", "", false
	}
	owner := name[1:i]
	// Genérico instanciado: "(Box[T]).Get" é o método de Box. O dono no arquivo se chama "Box".
	if j := strings.IndexByte(owner, '['); j > 0 {
		owner = owner[:j]
	}
	method := name[i+2:]
	if owner == "" || method == "" {
		return "", "", false
	}
	return owner, method, true
}

// rawSymbol is the wire shape of a symbol in either of the two answers the
// protocol allows: DocumentSymbol (range + children) or SymbolInformation
// (location, no children) for servers that ignore the hierarchical capability.
type rawSymbol struct {
	Name           string            `json:"name"`
	Detail         string            `json:"detail"`
	Kind           int               `json:"kind"`
	Range          Range             `json:"range"`
	SelectionRange Range             `json:"selectionRange"`
	Children       []json.RawMessage `json:"children"`
	Location       *struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	} `json:"location"`
}

// symbolNode turns one wire node into a symbol, children included, and reports
// whether the node was a symbol at all.
func symbolNode(n json.RawMessage) (DocumentSymbol, bool) {
	var d rawSymbol
	if json.Unmarshal(n, &d) != nil || d.Name == "" {
		return DocumentSymbol{}, false
	}
	sym := DocumentSymbol{Name: d.Name, Kind: d.Kind, Detail: d.Detail}
	if d.Location != nil {
		sym.Range, sym.Selection = d.Location.Range, d.Location.Range
	} else {
		sym.Range, sym.Selection = d.Range, d.SelectionRange
	}
	for _, ch := range d.Children {
		if c, ok := symbolNode(ch); ok {
			sym.Children = append(sym.Children, c)
		}
	}
	return sym, true
}

// flattenSymbols lists a symbol tree depth first, each symbol once.
func flattenSymbols(nodes []DocumentSymbol) []DocumentSymbol {
	var out []DocumentSymbol
	for _, n := range nodes {
		out = append(out, n)
		out = append(out, flattenSymbols(n.Children)...)
	}
	return out
}

// raw2nodes decodes the top-level array without losing the child trees.
func raw2nodes(raw json.RawMessage) []json.RawMessage {
	var nodes []json.RawMessage
	_ = json.Unmarshal(raw, &nodes)
	return nodes
}

// TypeHierarchyItem is one node of the server's type hierarchy: a type or a
// member of one, with the place it is declared.
type TypeHierarchyItem struct {
	Name           string          `json:"name"`
	Detail         string          `json:"detail,omitempty"`
	Kind           int             `json:"kind"`
	URI            string          `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	data           json.RawMessage // opaque: the server hands it back with the item
}

// Supertypes returns the types the type (or member) at pos extends or
// implements, as the server resolves them. This is the only answer to
// "inherits" that is not a guess: the source line names the base type, the
// server resolves it through the module graph, generics and all.
func (c *Client) Supertypes(ctx context.Context, path string, pos Position) ([]TypeHierarchyItem, error) {
	return c.hierarchy(ctx, path, pos, "typeHierarchy/supertypes")
}

func (c *Client) hierarchy(ctx context.Context, path string, pos Position, relation string) ([]TypeHierarchyItem, error) {
	if !c.capabilities().TypeHierarchy {
		return nil, ErrUnsupported
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, err
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/prepareTypeHierarchy", params)
	if err != nil {
		return nil, err
	}
	var out []TypeHierarchyItem
	seen := map[string]bool{}
	for _, item := range raw2nodes(raw) {
		var it TypeHierarchyItem
		if json.Unmarshal(item, &it) != nil || it.URI == "" {
			continue
		}
		it.data = item
		sub, err := c.call(ctx, relation, map[string]any{"item": json.RawMessage(item)})
		if err != nil {
			return nil, err
		}
		for _, sn := range raw2nodes(sub) {
			var s TypeHierarchyItem
			if json.Unmarshal(sn, &s) != nil || s.URI == "" {
				continue
			}
			key := fmt.Sprintf("%s:%d", s.URI, s.Range.Start.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, s)
		}
		if len(out) > 0 {
			break // the first item that resolves is the symbol the caller asked about
		}
	}
	sortHierarchy(out)
	return out, nil
}

// sortHierarchy orders items by file then line, so the same answer always comes
// out in the same order.
func sortHierarchy(items []TypeHierarchyItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].URI != items[j].URI {
			return items[i].URI < items[j].URI
		}
		return items[i].Range.Start.Line < items[j].Range.Start.Line
	})
}

// PrepareRename reports the range the server would rename at pos and the
// placeholder text it suggests. A nil range with nil error means the symbol is
// not renameable here.
func (c *Client) PrepareRename(ctx context.Context, path string, pos Position) (*Range, string, error) {
	if !c.capabilities().Rename {
		return nil, "", ErrUnsupported
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, "", err
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/prepareRename", params)
	if err != nil {
		return nil, "", err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, "", nil
	}
	var only Range
	if json.Unmarshal(raw, &only) == nil && (only != Range{}) {
		return &only, "", nil
	}
	var withText struct {
		Range       Range  `json:"range"`
		Placeholder string `json:"placeholder"`
	}
	if json.Unmarshal(raw, &withText) == nil && (withText.Range != Range{}) {
		return &withText.Range, withText.Placeholder, nil
	}
	return nil, "", nil
}

// Rename computes the edits that rename the symbol at pos. Nothing is written:
// the caller decides whether to apply the WorkspaceEdit.
//
// The server answers this itself unless the language declared that it cannot
// (see Launch.RenameByRefs), in which case the edits come from the reference
// list instead.
func (c *Client) Rename(ctx context.Context, path string, pos Position, newName string) (*WorkspaceEdit, error) {
	if !c.capabilities().Rename {
		return nil, ErrUnsupported
	}
	if c.cfg.RenameByRefs {
		return c.renameByRefs(ctx, path, pos, newName)
	}
	params, err := textDocumentPosition(path, pos)
	if err != nil {
		return nil, err
	}
	params["newName"] = newName
	raw, err := c.requestWithDoc(ctx, path, "textDocument/rename", params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var we WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		return nil, err
	}
	if we.empty() {
		return nil, nil
	}
	return &we, nil
}

// FileRename is one old -> new pair of a file rename. Both are absolute paths;
// the request carries them as file: URIs.
type FileRename struct{ From, To string }

// WillRenameFiles asks the server what a file rename implies: the imports and
// re-exports that point at the file, including through a barrel. It is the same
// question `git mv` cannot answer.
//
// Nothing is written: the rename itself is the caller's, and the returned edit
// is applied (or not) through ApplyWorkspaceEdit. A server that does not
// advertise the capability returns ErrUnsupported, which callers turn into the
// by-name answer like any other degradation.
func (c *Client) WillRenameFiles(ctx context.Context, renames []FileRename) (*WorkspaceEdit, error) {
	if !c.capabilities().WillRenameFiles {
		return nil, ErrUnsupported
	}
	if len(renames) == 0 {
		return nil, nil
	}
	files := make([]map[string]any, 0, len(renames))
	for _, r := range renames {
		from, err := absPath(r.From)
		if err != nil {
			return nil, err
		}
		to, err := absPath(r.To)
		if err != nil {
			return nil, err
		}
		files = append(files, map[string]any{
			"oldUri": PathToURI(from),
			"newUri": PathToURI(to),
		})
	}
	raw, err := c.call(ctx, "workspace/willRenameFiles", map[string]any{"files": files})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var we WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		return nil, err
	}
	if we.empty() {
		return nil, nil
	}
	return &we, nil
}

// renameByRefs computes a rename from the reference list instead of from the
// server's own textDocument/rename.
//
// It exists for a server whose rename is not per-symbol. csharp-ls 0.16 answers
// textDocument/rename with one edit for EVERY overload that shares the name, so
// applying its answer rewrites methods the caller never selected. The reference
// list, by contrast, resolves the symbol under the cursor exactly, so the edits
// are built from it — and each edit is checked against the file on disk before
// it is offered, because an edit aimed at the wrong range is how a rename
// corrupts the file it lands in.
//
// Nothing is written here: this only builds the WorkspaceEdit.
func (c *Client) renameByRefs(ctx context.Context, path string, pos Position, newName string) (*WorkspaceEdit, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil, errors.New("lsp: rename: empty new name")
	}
	at, placeholder, err := c.PrepareRename(ctx, path, pos)
	if err != nil {
		return nil, err
	}
	if at == nil {
		// The server says this position holds nothing renameable.
		return nil, nil
	}
	locs, err := c.References(ctx, path, pos, true)
	if err != nil {
		return nil, err
	}
	if len(locs) == 0 {
		return nil, nil
	}

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{}}
	texts := map[string]string{}
	seen := map[string]bool{}
	for _, l := range locs {
		file := URItoPath(l.URI)
		uri := PathToURI(file)
		text, ok := texts[uri]
		if !ok {
			data, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			text = string(data)
			texts[uri] = text
		}
		// What to replace: the name the server itself proposed, or the token
		// sitting at the reference when it proposed none.
		old := placeholder
		off, ok := offsetOfText(text, l.Range.Start)
		if !ok {
			return nil, fmt.Errorf("lsp: rename: %s:%d is outside the file", file, l.Range.Start.Line+1)
		}
		if old == "" {
			old = identifierAt(text, off)
		}
		if old == "" || !strings.HasPrefix(text[off:], old) {
			return nil, fmt.Errorf("lsp: rename: refusing to edit %s:%d: the file has no %q there",
				file, l.Range.Start.Line+1, old)
		}
		key := uri + ":" + itoa(l.Range.Start.Line) + ":" + itoa(l.Range.Start.Character)
		if seen[key] {
			continue
		}
		seen[key] = true
		we.Changes[uri] = append(we.Changes[uri], TextEdit{
			Range: Range{
				Start: l.Range.Start,
				// The token is there, so its end is its start plus its own
				// width — never the range the server happened to report.
				End: Position{Line: l.Range.Start.Line, Character: l.Range.Start.Character + utf16Len(old)},
			},
			NewText: newName,
		})
	}
	if we.empty() {
		return nil, nil
	}
	return we, nil
}

// identifierAt returns the identifier starting at the byte offset off, so a
// rename can learn the old name from the file when the server proposes none.
func identifierAt(text string, off int) string {
	end := off
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isIdentRune(r, end == off) {
			break
		}
		end += size
	}
	return text[off:end]
}

func isIdentRune(r rune, first bool) bool {
	if r == '@' && first {
		return true // a verbatim identifier: @class
	}
	if r == '_' || unicode.IsLetter(r) {
		return true
	}
	return !first && unicode.IsDigit(r)
}

// utf16Len is the width of s in UTF-16 code units, which is how the protocol
// counts columns.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Width(r)
	}
	return n
}

// Diagnostics returns the problems the server reports for one file.
//
// Servers that support pull diagnostics are asked directly. Servers that only
// push are waited on: the file is (re)sent and the client waits for the
// publishDiagnostics notification that follows, up to settle.
func (c *Client) Diagnostics(ctx context.Context, path string, settle time.Duration) ([]Diagnostic, error) {
	abs, err := absOf(path)
	if err != nil {
		return nil, err
	}
	uri := PathToURI(abs)

	if c.capabilities().Diagnostics {
		raw, err := c.requestWithDoc(ctx, path, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
		if err == nil {
			var report struct {
				Kind  string       `json:"kind"`
				Items []Diagnostic `json:"items"`
			}
			if json.Unmarshal(raw, &report) == nil {
				// Keep them: a codeAction has to carry the diagnostics of the
				// range it asks about, and a pull-only server never pushes any.
				c.mu.Lock()
				c.pulled[uri] = report.Items
				c.mu.Unlock()
				return report.Items, nil
			}
		}
		// Fall through to the push path rather than failing.
	}

	// The push path compares against what the server publishes, and the server
	// spells the URI its own way: the key is the normalised form.
	key := uriKey(uri)

	c.mu.Lock()
	before := c.diagSeq[key]
	c.mu.Unlock()

	if err := c.ensureOpen(ctx, path); err != nil {
		return nil, err
	}

	if settle <= 0 {
		settle = 3 * time.Second
	}
	deadline := time.Now().Add(settle)
	for {
		c.mu.Lock()
		seq := c.diagSeq[key]
		items := c.diags[key]
		c.mu.Unlock()
		if seq > before {
			return items, nil
		}
		if time.Now().After(deadline) {
			// No notification arrived: report what we have (usually nothing)
			// rather than inventing an error.
			return items, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Published returns the diagnostics the server last pushed for a file, without asking it anything.
// It is the state the client already holds, and it is what a caller needs as the "before" of an edit:
// against it, the diagnostics that come back afterwards can be told apart into the ones the edit
// caused and the ones that were already there. Asking Diagnostics instead would wait for a fresh
// publish, which for a file nothing touched never comes.
func (c *Client) Published(path string) []Diagnostic {
	abs, err := absOf(path)
	if err != nil {
		return nil
	}
	key := uriKey(PathToURI(abs))
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Diagnostic, len(c.diags[key]))
	copy(out, c.diags[key])
	return out
}

// Diagnosed reports whether the server has published diagnostics for a file at least once.
//
// It is what tells an empty Published apart from an unknown one: a file the server never looked at
// has no baseline, and "no errors" cannot be claimed about it — nor can any later error be called new.
func (c *Client) Diagnosed(path string) bool {
	abs, err := absOf(path)
	if err != nil {
		return false
	}
	key := uriKey(PathToURI(abs))
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diagSeq[key] > 0
}

// CodeActions returns fixes for a range. only filters by kind prefix, e.g.
// "quickfix" or "source.organizeImports".
func (c *Client) CodeActions(ctx context.Context, path string, rng Range, only []string) ([]CodeAction, error) {
	if !c.capabilities().CodeAction {
		return nil, ErrUnsupported
	}
	abs, err := absOf(path)
	if err != nil {
		return nil, err
	}
	// Give the server the diagnostics it already reported for this range so
	// quickfixes can be computed without a second round trip. This is not an
	// optimisation: csharp-ls builds the quickfix list from these alone, and
	// rejects a null list outright — by dying.
	diags := c.diagnosticsIn(abs, rng)
	if diags == nil {
		diags = []Diagnostic{}
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(abs)},
		"range":        rng,
		"context": map[string]any{
			"diagnostics": diags,
			"only":        only,
		},
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/codeAction", params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var actions []CodeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, err
	}
	return actions, nil
}

func (c *Client) diagnosticsIn(abs string, rng Range) []Diagnostic {
	uri := uriKey(PathToURI(abs))
	c.mu.Lock()
	items := c.diags[uri]
	if len(items) == 0 {
		items = c.pulled[uri]
	}
	c.mu.Unlock()
	var out []Diagnostic
	for _, d := range items {
		if d.Range.Start.Line >= rng.Start.Line && d.Range.End.Line <= rng.End.Line {
			out = append(out, d)
		}
	}
	return out
}

// Formatting returns the whole-document reformat edits.
func (c *Client) Formatting(ctx context.Context, path string, opts FormattingOptions) ([]TextEdit, error) {
	if !c.capabilities().Formatting {
		return nil, ErrUnsupported
	}
	abs, err := absOf(path)
	if err != nil {
		return nil, err
	}
	if opts.TabSize <= 0 {
		opts.TabSize = 4
	}
	raw, err := c.requestWithDoc(ctx, path, "textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(abs)},
		"options":      opts,
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var edits []TextEdit
	if err := json.Unmarshal(raw, &edits); err != nil {
		return nil, err
	}
	return edits, nil
}

// requestWithDoc syncs the file to disk state before sending the request, so a
// caller can never query a stale document version.
func (c *Client) requestWithDoc(ctx context.Context, path, method string, params any) (json.RawMessage, error) {
	if err := c.ensureOpen(ctx, path); err != nil {
		return nil, err
	}
	return c.call(ctx, method, params)
}

func absOf(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("lsp: empty path")
	}
	return absPath(path)
}

// Raw sends an unmodelled request. Used by `lsp doctor` and tests.
func (c *Client) Raw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.call(ctx, method, params)
}
