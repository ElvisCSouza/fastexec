package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// C# is the language this gate exists for: csharp-ls answers the handshake and
// then spends tens of seconds loading a solution, and every query in that window
// would come back empty — which reads as "this symbol has no uses", the one
// answer that must never be given wrongly.
//
// The tests below run the real gate against the fake server (a real process
// speaking the real protocol), so what is exercised is the path a query takes,
// not a mock of it.

// useLoadingServer points the manager at the fake server in a mode that
// announces a project load, configured the way the C# entry configures
// csharp-ls: a "loading…" progress title is the gate, and the note names it.
func useLoadingServer(t *testing.T, mode string) {
	t.Helper()
	prev := launchFor
	launchFor = func(lang Language, root string) (Launch, bool) {
		return Launch{
			Name:             "csharp-ls",
			LanguageID:       "csharp",
			Command:          os.Args[0],
			Env:              []string{"FAKELSP_HELPER=1", "FAKELSP_MODE=" + mode},
			Root:             root,
			BlockingProgress: []string{"loading"},
			LoadingNote:      "loading C# solution",
		}, true
	}
	t.Cleanup(func() { launchFor = prev })
}

// csharpProject makes a directory the C# language accepts as a repository root.
func csharpProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "demo.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"></Project>\n")
	return root
}

func csharpLanguage(t *testing.T) Language {
	t.Helper()
	lang, ok := LanguageByID("csharp")
	if !ok {
		t.Fatal("the catalogue has no C# entry")
	}
	return lang
}

// waitBusy polls until the server's load has been announced, so the assertions
// below are about the gate and not about how fast the fake starts.
func waitBusy(t *testing.T, c *Client, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if title, busy := c.Loading(); busy {
			return title
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the server announced no load; the gate cannot be tested")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLoadGateFallsBackWhileTheSolutionLoads is the directive's first-query
// behavior: a load in flight is reported as a load, the caller degrades to the
// name-based graph, and the note says what is happening instead of pretending
// the server is missing.
func TestLoadGateFallsBackWhileTheSolutionLoads(t *testing.T) {
	useLoadingServer(t, "loading")
	root := csharpProject(t)
	lang := csharpLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()

	c, release, err := m.Acquire(context.Background(), root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	waitBusy(t, c, 5*time.Second)

	err = c.WaitLoaded(context.Background(), 150*time.Millisecond)
	var le *LoadingError
	if !errors.As(err, &le) {
		t.Fatalf("WaitLoaded during a load = %v, want *LoadingError", err)
	}
	if le.Note != "loading C# solution" {
		t.Errorf("LoadingError.Note = %q, want the language's note", le.Note)
	}
	if !strings.Contains(le.Error(), "demo.sln") {
		t.Errorf("LoadingError.Error() = %q, want the announced title in it", le.Error())
	}
	// Callers that only care that nothing can be trusted ask this.
	if !errors.Is(err, ErrLoading) {
		t.Error("a LoadingError must still satisfy errors.Is(err, ErrLoading)")
	}
	if got := Degraded(err); got != "lsp loading C# solution…; by name" {
		t.Errorf("Degraded(loading) = %q, want the loading note", got)
	}

	// A query goes through WaitSettled first, and its budget bounds the wait:
	// the impact footer of an edit already on disk cannot pay five seconds.
	start := time.Now()
	err = c.WaitSettled(context.Background(), filepath.Join(root, "A.cs"), 120*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("WaitSettled spent %v on a 120ms budget; a busy load must not extend it", elapsed)
	}
	if !errors.As(err, &le) {
		t.Fatalf("WaitSettled during a load = %v, want *LoadingError", err)
	}
}

// TestLoadGateOpensWhenTheLoadEnds covers the other half of the directive: the
// first query degrades, the ones after the load are exact and pay nothing.
func TestLoadGateOpensWhenTheLoadEnds(t *testing.T) {
	useLoadingServer(t, "loading-once")
	root := csharpProject(t)
	lang := csharpLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	c, release, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	waitBusy(t, c, 5*time.Second)

	// The fake announces a solution and then its projects; only the end of the
	// last one may open the gate, so a short grace here is still a degradation.
	if err := c.WaitLoaded(ctx, 100*time.Millisecond); err == nil {
		t.Error("WaitLoaded returned nil while the solution was still being read")
	}

	if err := c.WaitLoaded(ctx, 10*time.Second); err != nil {
		t.Fatalf("WaitLoaded after the load ended: %v", err)
	}
	if title, busy := c.Loading(); busy {
		t.Errorf("Loading() = %q after the load ended, want not busy", title)
	}
	// The steady state is free: no polling, no grace spent.
	start := time.Now()
	if err := c.WaitLoaded(ctx, time.Minute); err != nil {
		t.Fatalf("WaitLoaded once loaded: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("a loaded project cost %v to re-check; it must be free", elapsed)
	}
}

// TestLoadGateRetiresForSilentServers: gopls announces no blocking progress. A
// client that waited forever for a title that never comes would tax every query
// of every Go repository, so the gate retires after the first grace and the
// document-settled wait stays as the gate.
func TestLoadGateRetiresForSilentServers(t *testing.T) {
	useLoadingServer(t, "") // the fake announces nothing
	root := csharpProject(t)
	lang := csharpLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	c, release, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if err := c.WaitLoaded(ctx, 150*time.Millisecond); err != nil {
		t.Fatalf("a server that announces nothing must not report a load: %v", err)
	}
	start := time.Now()
	if err := c.WaitLoaded(ctx, 5*time.Second); err != nil {
		t.Fatalf("second WaitLoaded: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("the retired gate still waited %v", elapsed)
	}
}

// TestProgressStateMachine pins the rules the gate is built on, without a
// process in the way: only titles the language declared as blocking count, and
// only the end of the LAST one means the project is loaded.
func TestProgressStateMachine(t *testing.T) {
	c := &Client{
		cfg:     Config{Launch: Launch{BlockingProgress: []string{"loading", "restoring"}}},
		loading: map[string]string{},
	}

	// A title the language did not declare is somebody else's progress (an
	// index, a format run) and must never gate a query.
	c.handleProgress([]byte(`{"token":"t1","value":{"kind":"begin","title":"Indexing files..."}}`))
	if _, busy := c.Loading(); busy {
		t.Fatal("an unrelated progress title closed the gate")
	}

	c.handleProgress([]byte(`{"token":"t1","value":{"kind":"begin","title":"Loading solution demo.sln..."}}`))
	if title, busy := c.Loading(); !busy || !strings.Contains(title, "demo.sln") {
		t.Fatalf("Loading() = %q, %v after a blocking begin", title, busy)
	}

	// A second load on top of the first: the first end is not the end.
	c.handleProgress([]byte(`{"token":"t2","value":{"kind":"begin","title":"Loading 4 project(s)..."}}`))
	c.handleProgress([]byte(`{"token":"t1","value":{"kind":"end"}}`))
	if _, busy := c.Loading(); !busy {
		t.Fatal("the gate opened at the end of the first of two loads")
	}
	c.handleProgress([]byte(`{"token":"t2","value":{"kind":"end"}}`))
	if title, busy := c.Loading(); busy {
		t.Fatalf("the gate stayed closed after the last load ended (%q)", title)
	}

	// An end for a token that never began is not an answer about anything.
	c.handleProgress([]byte(`{"token":"ghost","value":{"kind":"end"}}`))
	if title, busy := c.Loading(); busy {
		t.Fatalf("a stray end re-opened the gate (%q)", title)
	}

	// A report is not a title: "end" carries none, and a malformed frame must be
	// ignored rather than crash a query.
	c.handleProgress([]byte(`not json`))
	if _, busy := c.Loading(); busy {
		t.Fatal("a malformed progress notification closed the gate")
	}
}

// TestDegradedWording: one place decides how FastExec says what happened, and
// "still loading" must not be reported as "unavailable" — the user would go
// looking for a broken install instead of asking again in a moment.
func TestDegradedWording(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"loading", &LoadingError{Title: "Loading solution demo.sln...", Note: "loading C# solution"}, "lsp loading C# solution…; by name"},
		{"unavailable", Unavailable("restore needed: run dotnet restore (demo.csproj)"), "lsp unavailable (restore needed: run dotnet restore (demo.csproj)); by name"},
		{"bare", Unavailable(""), "lsp unavailable; by name"},
	}
	for _, tc := range cases {
		if got := Degraded(tc.err); got != tc.want {
			t.Errorf("Degraded(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := Degraded(nil); got != "lsp unavailable; by name" {
		t.Errorf("Degraded(nil) = %q", got)
	}
}

// TestLoadGraceIsBoundedByTheCallerBudget: the settle budget of a caller that
// cannot wait (the edit footer, 120 ms) is not silently raised to the default.
func TestLoadGraceIsBoundedByTheCallerBudget(t *testing.T) {
	if got := loadGrace(120 * time.Millisecond); got != 120*time.Millisecond {
		t.Errorf("loadGrace(120ms) = %v, want the caller's budget", got)
	}
	if got := loadGrace(0); got != DefaultLoadGrace {
		t.Errorf("loadGrace(0) = %v, want DefaultLoadGrace", got)
	}
	if got := loadGrace(time.Minute); got != DefaultLoadGrace {
		t.Errorf("loadGrace(1m) = %v, want DefaultLoadGrace", got)
	}
}
