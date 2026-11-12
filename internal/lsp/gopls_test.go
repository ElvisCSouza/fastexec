package lsp

// gopls_test.go: the only test that drives a REAL language server. Everything
// else in this package talks to the fake server in fake_test.go, which proves
// the protocol but cannot prove that FastExec gets the answer a human would call
// correct.
//
// What this test is for is the single claim the whole layer exists to make: a
// name-based index cannot tell two same-named methods apart, and the language
// server can. The fixture declares Do() on two types and calls each one from a
// third file; the assertions are that References from one declaration returns
// only that type's call site, and that Rename touches only that type.
//
// The test skips (never fails) when gopls is not installed: a language server is
// an optional program, and a missing one is not a broken checkout.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Fixture files. alpha.go and beta.go each declare Do(); caller.go calls both.
const (
	goplsAlpha = `package demo

type Alpha struct{}

func (a Alpha) Do() int { return 1 }
`
	goplsBeta = `package demo

type Beta struct{}

func (b Beta) Do() int { return 2 }
`
	goplsCallers = `package demo

func CallAlpha(a Alpha) int { return a.Do() }

func CallBeta(b Beta) int { return b.Do() }
`
)

// goplsFixture writes the module and returns its root.
func goplsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	writeFixture(t, root, "alpha.go", goplsAlpha)
	writeFixture(t, root, "beta.go", goplsBeta)
	writeFixture(t, root, "caller.go", goplsCallers)
	return root
}

// realGopls starts a real gopls for the fixture, or skips the test. The launch
// goes through the same discovery the daemon uses, so this also proves that
// Find/Languages actually locate gopls on the machine running the suite.
func realGopls(t *testing.T, root string) *Client {
	t.Helper()
	if Disabled() {
		t.Skip("FASTEXEC_LSP is off")
	}
	lang := goLanguage(t)
	launch, ok := LaunchFor(lang, root)
	if !ok {
		t.Skipf("%s not installed: %s", lang.Server, lang.Install)
	}
	// gopls loads and type-checks the module on the first request, so the bound
	// here is generous: a cold module takes seconds, not milliseconds.
	c, err := New(context.Background(), Config{
		Launch:           launch,
		HandshakeTimeout: 60 * time.Second,
		Timeout:          60 * time.Second,
	})
	if err != nil {
		t.Fatalf("starting the real %s: %v", lang.Server, err)
	}
	t.Cleanup(c.Close)
	return c
}

// posOf finds name on the line of a file that contains marker, and returns the
// path of the file plus the LSP position of name. The fixtures are pure ASCII,
// so a byte offset is also the UTF-16 column.
func posInFile(t *testing.T, root, file, marker, name string) (string, Position) {
	t.Helper()
	path := filepath.Join(root, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		at := strings.Index(line, marker)
		if at < 0 {
			continue
		}
		col := strings.Index(line[at:], name)
		if col < 0 {
			t.Fatalf("%q is not on the line containing %q in %s", name, marker, file)
		}
		return path, Position{Line: i, Character: at + col}
	}
	t.Fatalf("no line containing %q in %s", marker, file)
	return "", Position{}
}

// locKeys reduces a location list to sorted "file:line" keys relative to root,
// so a comparison reads the same on every platform.
func locKeys(t *testing.T, root string, locs []Location) []string {
	t.Helper()
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		out = append(out, fileAndLine(t, root, l.URI, l.Range.Start.Line+1))
	}
	sort.Strings(out)
	return out
}

// fileAndLine renders one location as "file.go:12" with a slash-separated path
// relative to root.
func fileAndLine(t *testing.T, root, uri string, oneBasedLine int) string {
	t.Helper()
	rel := relURI(t, root, uri)
	return rel + ":" + itoa(oneBasedLine)
}

// editedFiles flattens a WorkspaceEdit into "file.go" -> edits, accepting both
// protocol shapes.
func editedFiles(t *testing.T, root string, we *WorkspaceEdit) map[string][]TextEdit {
	t.Helper()
	out := map[string][]TextEdit{}
	for _, d := range we.DocumentChanges {
		if d.TextDocument == nil {
			continue // a file create/rename/delete arm; the rename tests below have none
		}
		out[relURI(t, root, d.TextDocument.URI)] = append(out[relURI(t, root, d.TextDocument.URI)], d.Edits...)
	}
	for uri, edits := range we.Changes {
		out[relURI(t, root, uri)] = append(out[relURI(t, root, uri)], edits...)
	}
	return out
}

// TestGoplsReferencesTellSameNamedMethodsApart is the reason this layer exists:
// the index by name would return both call sites for "Do".
func TestGoplsReferencesTellSameNamedMethodsApart(t *testing.T) {
	root := goplsFixture(t)
	c := realGopls(t, root)
	ctx := context.Background()

	alphaPath, alphaPos := posInFile(t, root, "alpha.go", "func (a Alpha) Do()", "Do")
	locs, err := c.References(ctx, alphaPath, alphaPos, false)
	if err != nil {
		t.Fatalf("References on Alpha.Do: %v", err)
	}
	if len(locs) == 0 {
		t.Fatal("References on Alpha.Do returned nothing, but caller.go calls it")
	}
	got := locKeys(t, root, locs)
	if !contains(got, "caller.go:3") {
		t.Errorf("Alpha.Do references = %v, want caller.go:3 (the CallAlpha body)", got)
	}
	if contains(got, "caller.go:5") {
		t.Errorf("Alpha.Do references = %v, must NOT contain caller.go:5: that is Beta.Do, a same-named method on another type", got)
	}

	// The mirror image, so the test cannot pass by returning everything.
	betaPath, betaPos := posInFile(t, root, "beta.go", "func (b Beta) Do()", "Do")
	locs, err = c.References(ctx, betaPath, betaPos, false)
	if err != nil {
		t.Fatalf("References on Beta.Do: %v", err)
	}
	got = locKeys(t, root, locs)
	if !contains(got, "caller.go:5") {
		t.Errorf("Beta.Do references = %v, want caller.go:5", got)
	}
	if contains(got, "caller.go:3") {
		t.Errorf("Beta.Do references = %v, must NOT contain caller.go:3", got)
	}
}

// TestGoplsRenameEditsExactlyOneType checks that the WorkspaceEdit a rename
// produces changes the declaration and its own call site, and leaves the other
// type alone. Nothing is written here: the edit is inspected, not applied.
func TestGoplsRenameEditsExactlyOneType(t *testing.T) {
	root := goplsFixture(t)
	c := realGopls(t, root)
	ctx := context.Background()

	alphaPath, alphaPos := posInFile(t, root, "alpha.go", "func (a Alpha) Do()", "Do")
	we, err := c.Rename(ctx, alphaPath, alphaPos, "Run")
	if err != nil {
		t.Fatalf("Rename on Alpha.Do: %v", err)
	}
	if we == nil {
		t.Fatal("Rename returned no WorkspaceEdit")
	}
	edited := editedFiles(t, root, we)

	alphaEdits := edited["alpha.go"]
	if len(alphaEdits) != 1 {
		t.Fatalf("alpha.go edits = %d, want exactly 1 (the declaration)", len(alphaEdits))
	}
	if alphaEdits[0].NewText != "Run" {
		t.Errorf("alpha.go edit text = %q, want %q", alphaEdits[0].NewText, "Run")
	}

	callerEdits := edited["caller.go"]
	if len(callerEdits) != 1 {
		t.Fatalf("caller.go edits = %d, want exactly 1 (CallAlpha only); Beta.Do must be untouched", len(callerEdits))
	}
	if callerEdits[0].NewText != "Run" {
		t.Errorf("caller.go edit text = %q, want %q", callerEdits[0].NewText, "Run")
	}
	if callerEdits[0].Range.Start.Line != 2 {
		t.Errorf("caller.go edit is on line %d, want line 3 (CallAlpha)", callerEdits[0].Range.Start.Line+1)
	}
	if _, ok := edited["beta.go"]; ok {
		t.Error("Rename on Alpha.Do produced an edit in beta.go: it renamed the wrong method")
	}
}

// TestGoplsDefinitionIsExact pins the other navigation call the tools layer
// uses: the definition of a call site resolves to that type's declaration.
func TestGoplsDefinitionIsExact(t *testing.T) {
	root := goplsFixture(t)
	c := realGopls(t, root)
	ctx := context.Background()

	callerPath, callPos := posInFile(t, root, "caller.go", "func CallAlpha", "Do")
	locs, err := c.Definition(ctx, callerPath, callPos)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	got := locKeys(t, root, locs)
	if !contains(got, "alpha.go:5") {
		t.Errorf("definition of the a.Do() call = %v, want alpha.go:5 (Alpha.Do)", got)
	}
	if contains(got, "beta.go:5") {
		t.Errorf("definition of the a.Do() call = %v, must not be beta.go:5", got)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func relURI(t *testing.T, root, uri string) string {
	t.Helper()
	rel, err := filepath.Rel(root, URItoPath(uri))
	if err != nil {
		return URItoPath(uri)
	}
	return filepath.ToSlash(rel)
}
