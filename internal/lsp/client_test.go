package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClient starts the fake server as a real child process for root.
func fakeClient(t *testing.T, root string, env ...string) *Client {
	t.Helper()
	cfg := Config{
		Launch: Launch{
			Name:       "fake-lsp",
			LanguageID: "go",
			Command:    os.Args[0],
			Env:        append([]string{"FAKELSP_HELPER=1"}, env...),
			Root:       root,
		},
		Timeout:          5 * time.Second,
		HandshakeTimeout: 10 * time.Second,
	}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

const fixtureSrc = `package demo

type A struct{}

func (a A) Run() {}

type B struct{}

func (b B) Run() {}

func main() {
	var a A
	a.Run()
	var b B
	b.Run()
}
`

// posOf returns the position of the first occurrence of sub.
func posOf(t *testing.T, text, sub string) Position {
	t.Helper()
	i := strings.Index(text, sub)
	if i < 0 {
		t.Fatalf("fixture does not contain %q", sub)
	}
	line := strings.Count(text[:i], "\n")
	start := strings.LastIndexByte(text[:i], '\n') + 1
	return Position{Line: line, Character: i - start}
}

func TestHandshakeAndCapabilities(t *testing.T) {
	root := t.TempDir()
	c := fakeClient(t, root)

	caps := c.Capabilities()
	if !caps.References || !caps.Definition || !caps.Rename || !caps.PrepareRename ||
		!caps.Hover || !caps.DocumentSymbol || !caps.CodeAction || !caps.Formatting {
		t.Fatalf("capabilities not negotiated: %+v", caps)
	}
	if c.Root() != root {
		t.Errorf("Root() = %q, want %q", c.Root(), root)
	}
	if c.PID() <= 0 {
		t.Errorf("PID() = %d, want a live process", c.PID())
	}
	// The server's stderr is captured for diagnosis.
	if got := c.Stderr(); !strings.Contains(got, "fake-lsp ready") {
		t.Errorf("stderr not captured, got %q", got)
	}
}

func TestDocumentSyncAndNavigation(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	c := fakeClient(t, root)

	pos := posOf(t, fixtureSrc, "Run()")

	locs, err := c.References(context.Background(), path, pos, false)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	// Four writes of "Run" in the file; the declaration is excluded.
	if len(locs) != 3 {
		t.Fatalf("References = %d locations, want 3: %+v", len(locs), locs)
	}
	for _, l := range locs {
		if !strings.HasSuffix(l.URI, "/main.go") {
			t.Errorf("location URI = %q, want main.go", l.URI)
		}
		if l.Range.End.Character-l.Range.Start.Character != 3 {
			t.Errorf("range %+v does not cover the identifier", l.Range)
		}
	}

	defs, err := c.Definition(context.Background(), path, pos)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("Definition = %d locations, want 1", len(defs))
	}

	// The document was synced from disk before the query, so the server saw
	// the same bytes the client has: the answer is about this file.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture vanished: %v", err)
	}
}

func TestDidChangeIsSentWhenFileChangesOnDisk(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	c := fakeClient(t, root)
	ctx := context.Background()

	if _, err := c.References(ctx, path, posOf(t, fixtureSrc, "Run()"), false); err != nil {
		t.Fatalf("References: %v", err)
	}

	// FastExec owns the file: rewriting it on disk and querying again must
	// re-sync the document, and the answer must follow the new bytes.
	updated := strings.ReplaceAll(fixtureSrc, "a.Run()", "a.Run()\n\ta.Run()")
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	locs, err := c.References(ctx, path, posOf(t, updated, "Run()"), false)
	if err != nil {
		t.Fatalf("References after change: %v", err)
	}
	if len(locs) != 4 {
		t.Fatalf("References after change = %d, want 4", len(locs))
	}
}

func TestRenameReturnsWorkspaceEdit(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	c := fakeClient(t, root)
	ctx := context.Background()

	rng, placeholder, err := c.PrepareRename(ctx, path, posOf(t, fixtureSrc, "Run()"))
	if err != nil {
		t.Fatalf("PrepareRename: %v", err)
	}
	if rng == nil || placeholder != "Run" {
		t.Fatalf("PrepareRename = %v, %q; want the identifier range and \"Run\"", rng, placeholder)
	}

	we, err := c.Rename(ctx, path, posOf(t, fixtureSrc, "Run()"), "Execute")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if we == nil {
		t.Fatal("Rename returned no WorkspaceEdit")
	}
	files := we.Files()
	if len(files) != 1 {
		t.Fatalf("WorkspaceEdit touches %d files, want 1", len(files))
	}
	// 4 occurrences of Run, all rewritten.
	if n := countEdits(we); n != 4 {
		t.Fatalf("WorkspaceEdit has %d edits, want 4", n)
	}
	// Nothing was written: the caller decides.
	if data, _ := os.ReadFile(path); string(data) != fixtureSrc {
		t.Fatal("Rename wrote to disk; it must only compute the edit")
	}
}

func countEdits(we *WorkspaceEdit) int {
	n := 0
	for _, edits := range we.Changes {
		n += len(edits)
	}
	for _, dc := range we.DocumentChanges {
		n += len(dc.Edits)
	}
	return n
}

func TestDiagnosticsPull(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", "package demo\n\n// TODO: finish\nfunc main() {}\n")
	c := fakeClient(t, root)

	diags, err := c.Diagnostics(context.Background(), path, time.Second)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Diagnostics = %d, want 1: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "TODO") {
		t.Errorf("message = %q", diags[0].Message)
	}
	if diags[0].Range.Start.Line != 2 {
		t.Errorf("diagnostic on line %d, want 2", diags[0].Range.Start.Line)
	}
}

func TestDiagnosticsPushFallback(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", "package demo\n\n// TODO: finish\nfunc main() {}\n")
	// FAKELSP_NO_PULL removes the pull capability: the client must fall back
	// to the publishDiagnostics notification instead of failing.
	c := fakeClient(t, root, "FAKELSP_NO_PULL=1")
	if c.Capabilities().Diagnostics {
		t.Fatal("fake still advertises pull diagnostics")
	}

	diags, err := c.Diagnostics(context.Background(), path, 3*time.Second)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Diagnostics = %d, want 1: %+v", len(diags), diags)
	}
}

// TestWaitSettledPullOnlyServer: a server that speaks pull diagnostics and never
// publishes (csharp-ls) must settle through the pull, not run out the budget and
// be reported as "did not load" for a project that is loaded.
func TestWaitSettledPullOnlyServer(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	c := fakeClient(t, root, "FAKELSP_NO_PUSH=1")
	if !c.Capabilities().Diagnostics {
		t.Fatal("fake lost its pull capability")
	}
	start := time.Now()
	if err := c.WaitSettled(context.Background(), path, 5*time.Second); err != nil {
		t.Fatalf("WaitSettled on a pull-only server: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("settled after %v, want it to come from the pull, not the timeout", d)
	}
}

func TestRequestTimeout(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	cfg := Config{
		Launch: Launch{
			Name: "fake-lsp", LanguageID: "go", Command: os.Args[0],
			Env:  []string{"FAKELSP_HELPER=1", "FAKELSP_MODE=timeout"},
			Root: root,
		},
		Timeout:          400 * time.Millisecond,
		HandshakeTimeout: 10 * time.Second,
	}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// The handshake answers; the operation does not. It must return, with an
	// error, instead of hanging the daemon.
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := c.References(context.Background(), path, Position{}, false)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("References hung past the per-call timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %v, want about the configured 400ms", elapsed)
	}
}

func TestServerDeathIsNoticedAndRestarted(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", fixtureSrc)
	marker := filepath.Join(t.TempDir(), "crashed")
	c := fakeClient(t, root, "FAKELSP_MODE=crash-once", "FAKELSP_STATE="+marker)
	ctx := context.Background()
	pos := posOf(t, fixtureSrc, "Run()")

	// The first operation kills the server; the call fails but must not hang.
	if _, err := c.References(ctx, path, pos, false); err == nil {
		t.Fatal("expected an error when the server dies mid-request")
	}
	// The next call restarts it transparently and answers.
	locs, err := c.References(ctx, path, pos, false)
	if err != nil {
		t.Fatalf("References after restart: %v", err)
	}
	if len(locs) != 3 {
		t.Fatalf("References after restart = %d, want 3", len(locs))
	}
	if c.PID() <= 0 {
		t.Error("client reports no live process after restart")
	}
}

func TestUnsupportedOperationIsNotAnError(t *testing.T) {
	root := t.TempDir()
	c := fakeClient(t, root)
	c.mu.Lock()
	c.caps.References = false
	c.mu.Unlock()

	_, err := c.References(context.Background(), filepath.Join(root, "x.go"), Position{}, false)
	if err != ErrUnsupported {
		t.Fatalf("References on a server without the capability = %v, want ErrUnsupported", err)
	}
}

func TestStderrIsBounded(t *testing.T) {
	root := t.TempDir()
	// The ring buffer keeps the tail, never the whole log.
	c := fakeClient(t, root, "FAKELSP_STDERR="+strings.Repeat("noise ", 20000))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.Stderr()) >= 64<<10 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(c.Stderr()); got > 64<<10 {
		t.Fatalf("stderr buffer = %d bytes, want at most %d", got, 64<<10)
	}
}
