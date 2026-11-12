package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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

// DocumentSymbols lists the symbols of one file. Hierarchical answers are
// flattened depth first so callers see a single deterministic list.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
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
	var hier []struct {
		Name           string            `json:"name"`
		Detail         string            `json:"detail"`
		Kind           int               `json:"kind"`
		Range          Range             `json:"range"`
		SelectionRange Range             `json:"selectionRange"`
		Children       []json.RawMessage `json:"children"`
	}
	if json.Unmarshal(raw, &hier) == nil && len(hier) > 0 {
		var out []DocumentSymbol
		var walk func(nodes []json.RawMessage)
		walk = func(nodes []json.RawMessage) {
			for _, n := range nodes {
				var d struct {
					Name           string            `json:"name"`
					Detail         string            `json:"detail"`
					Kind           int               `json:"kind"`
					Range          Range             `json:"range"`
					SelectionRange Range             `json:"selectionRange"`
					Children       []json.RawMessage `json:"children"`
					// SymbolInformation shape, used when the server ignores
					// the "hierarchical" client capability.
					Location *struct {
						URI   string `json:"uri"`
						Range Range  `json:"range"`
					} `json:"location"`
				}
				if json.Unmarshal(n, &d) != nil {
					continue
				}
				sym := DocumentSymbol{Name: d.Name, Kind: d.Kind, Detail: d.Detail}
				if d.Location != nil {
					sym.Range = d.Location.Range
					sym.Selection = d.Location.Range
				} else {
					sym.Range = d.Range
					sym.Selection = d.SelectionRange
				}
				out = append(out, sym)
				walk(d.Children)
			}
		}
		walk(raw2nodes(raw))
		return out, nil
	}
	return nil, nil
}

// raw2nodes decodes the top-level array without losing the child trees.
func raw2nodes(raw json.RawMessage) []json.RawMessage {
	var nodes []json.RawMessage
	_ = json.Unmarshal(raw, &nodes)
	return nodes
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
func (c *Client) Rename(ctx context.Context, path string, pos Position, newName string) (*WorkspaceEdit, error) {
	if !c.capabilities().Rename {
		return nil, ErrUnsupported
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
				return report.Items, nil
			}
		}
		// Fall through to the push path rather than failing.
	}

	c.mu.Lock()
	before := c.diagSeq[uri]
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
		seq := c.diagSeq[uri]
		items := c.diags[uri]
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
	// Give the server the diagnostics it already published for this range so
	// quickfixes can be computed without a second round trip.
	diags := c.diagnosticsIn(abs, rng)
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
	uri := PathToURI(abs)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Diagnostic
	for _, d := range c.diags[uri] {
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
