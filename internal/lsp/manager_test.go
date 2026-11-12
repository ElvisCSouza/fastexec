package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestManager builds a manager that is not the process-wide singleton, so a
// test that reaps or shuts down servers cannot disturb another test.
//
// The caller must `defer m.Shutdown()`: a deferred call runs before the
// t.Cleanup that removes the fixture directory, which matters on Windows —
// a live server holds its repository root as its working directory.
func newTestManager(t *testing.T, idle time.Duration) *manager {
	t.Helper()
	return &manager{
		entries: make(map[string]*entry),
		idle:    idle,
		max:     DefaultMaxServers,
		stop:    make(chan struct{}),
	}
}

// fakeLaunchEnv varies how the fake server behaves; tests in a package run
// sequentially, so one package-level slice is enough.
var fakeLaunchEnv []string

// useFakeServer points the manager at this test binary, which TestMain turns
// into a language server. Nothing real is installed or downloaded.
func useFakeServer(t *testing.T, env ...string) {
	t.Helper()
	prev := launchFor
	fakeLaunchEnv = env
	launchFor = func(lang Language, root string) (Launch, bool) {
		return Launch{
			Name:       lang.Primary().Name,
			LanguageID: lang.ID,
			Command:    os.Args[0],
			Env:        append([]string{"FAKELSP_HELPER=1"}, fakeLaunchEnv...),
			Root:       root,
		}, true
	}
	t.Cleanup(func() {
		launchFor = prev
		fakeLaunchEnv = nil
	})
}

// goModule makes a directory that looks loadable to the Go server.
func goModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	return root
}

func goLanguage(t *testing.T) Language {
	t.Helper()
	lang, ok := LanguageByID("go")
	if !ok {
		t.Fatal("the catalogue has no Go entry")
	}
	return lang
}

func TestManagerStartsOnDemandAndReusesWarmServer(t *testing.T) {
	useFakeServer(t)
	root := goModule(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	if len(m.Servers()) != 0 {
		t.Fatal("a fresh manager already has a server; it must start on demand")
	}

	c1, release, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if c1 == nil || c1.PID() <= 0 {
		t.Fatal("Acquire returned no live server")
	}
	servers := m.Servers()
	if len(servers) != 1 {
		t.Fatalf("Servers() = %d entries, want 1", len(servers))
	}
	if servers[0].Root != root || servers[0].Language != "go" {
		t.Errorf("Servers()[0] = %+v, want root %q and language go", servers[0], root)
	}
	release()

	// The second call is served by the same process: that is the whole point of
	// keeping a server warm.
	c2, release2, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer release2()
	if c2 != c1 {
		t.Fatal("the second query started a new server instead of reusing the warm one")
	}
	if len(m.Servers()) != 1 {
		t.Fatalf("Servers() = %d entries after reuse, want 1", len(m.Servers()))
	}
}

func TestManagerReapsIdleServers(t *testing.T) {
	useFakeServer(t)
	root := goModule(t)
	lang := goLanguage(t)
	m := newTestManager(t, 40*time.Millisecond)
	defer m.Shutdown()
	ctx := context.Background()

	client, release, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	// A server in use is never reaped, however idle it looks.
	_, releaseBusy, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	m.sweep()
	if len(m.Servers()) != 1 {
		t.Fatal("sweep reaped a server that was in use")
	}
	releaseBusy()

	time.Sleep(80 * time.Millisecond)
	m.sweep()
	if got := m.Servers(); len(got) != 0 {
		t.Fatalf("Servers() = %+v after the idle window, want none", got)
	}
	if client.PID() > 0 {
		t.Error("the reaped server process is still alive")
	}
}

func TestManagerCapsSimultaneousServers(t *testing.T) {
	useFakeServer(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	m.SetMaxServers(1)
	ctx := context.Background()
	rootA, rootB := goModule(t), goModule(t)

	_, releaseA, err := m.Acquire(ctx, rootA, lang)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}

	// A is busy, so B cannot take its slot: refusing is better than stealing a
	// server from a call that is using it.
	_, _, err = m.Acquire(ctx, rootB, lang)
	if err == nil {
		t.Fatal("Acquire over the cap succeeded while every server was busy")
	}
	var ue *UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("cap refusal = %v, want an *UnavailableError", err)
	}
	if len(m.Servers()) != 1 {
		t.Fatalf("Servers() = %d, want the cap respected", len(m.Servers()))
	}
	releaseA()

	// With A idle, B takes the slot and the cap still holds.
	_, releaseB, err := m.Acquire(ctx, rootB, lang)
	if err != nil {
		t.Fatalf("Acquire B after release: %v", err)
	}
	defer releaseB()
	servers := m.Servers()
	if len(servers) != 1 {
		t.Fatalf("Servers() = %d after eviction, want 1", len(servers))
	}
	if servers[0].Root != rootB {
		t.Errorf("the surviving server is %q, want the newest root %q", servers[0].Root, rootB)
	}
}

func TestManagerDegradesWhenDisabled(t *testing.T) {
	useFakeServer(t)
	t.Setenv("FASTEXEC_LSP", "off")
	root := goModule(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()

	_, _, err := m.Acquire(context.Background(), root, lang)
	if err == nil {
		t.Fatal("Acquire succeeded with FASTEXEC_LSP=off")
	}
	var ue *UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("Acquire = %v, want an *UnavailableError", err)
	}
	if ue.Reason != "disabled by FASTEXEC_LSP" {
		t.Errorf("reason = %q", ue.Reason)
	}
	// The one line every tool shows instead of an exact answer.
	if got, want := Degraded(err), "lsp unavailable (disabled by FASTEXEC_LSP); by name"; got != want {
		t.Errorf("Degraded = %q, want %q", got, want)
	}
	if len(m.Servers()) != 0 {
		t.Error("a server was started even though LSP is disabled")
	}
}

func TestManagerDegradesWithoutAProjectMarker(t *testing.T) {
	useFakeServer(t)
	// A directory gopls cannot load: no go.mod anywhere above it. Starting a
	// server here would only waste a process and answer nothing.
	root := t.TempDir()
	useTempRootAbove(t, root)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()

	_, _, err := m.Acquire(context.Background(), root, lang)
	var ue *UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("Acquire = %v, want an *UnavailableError", err)
	}
	if !strings.Contains(ue.Reason, "project marker") {
		t.Errorf("reason = %q, want it to name the missing marker", ue.Reason)
	}
	if !strings.Contains(Degraded(err), "by name") {
		t.Errorf("Degraded = %q, want the by-name fallback line", Degraded(err))
	}
}

// useTempRootAbove makes sure no project marker sits above a temp directory.
// Some CI systems put the temp dir inside a Go module; if that happens the walk
// up would legitimately find a marker, and the assertion would be about the CI
// layout rather than about FastExec.
func useTempRootAbove(t *testing.T, dir string) {
	t.Helper()
	for d := filepath.Dir(dir); d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
		if HasMarker(goLanguage(t), d) {
			t.Skipf("temp dir sits inside a Go module (%s); cannot test a marker-less tree here", d)
		}
	}
}

func TestManagerRemembersAFailedStart(t *testing.T) {
	prev := launchFor
	var mu sync.Mutex
	calls := 0
	launchFor = func(Language, string) (Launch, bool) {
		mu.Lock()
		calls++
		mu.Unlock()
		return Launch{}, false // not installed
	}
	t.Cleanup(func() { launchFor = prev })

	root := goModule(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _, err := m.Acquire(ctx, root, lang)
		var ue *UnavailableError
		if !errors.As(err, &ue) {
			t.Fatalf("Acquire = %v, want an *UnavailableError", err)
		}
		if !strings.Contains(ue.Reason, "not installed") {
			t.Errorf("reason = %q, want a not-installed line", ue.Reason)
		}
	}
	// The cooldown keeps a doomed server from being respawned on every query.
	if calls != 1 {
		t.Errorf("the executable was looked for %d times, want 1 (failure cooldown)", calls)
	}
}

func TestManagerAcquireFileResolvesTheRepositoryRoot(t *testing.T) {
	useFakeServer(t)
	root := goModule(t)
	path := writeFixture(t, root, filepath.Join("internal", "deep", "main.go"), "package main\n")

	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	client, release, err := m.AcquireFile(context.Background(), path)
	if err != nil {
		t.Fatalf("AcquireFile: %v", err)
	}
	defer release()
	if client.Root() != root {
		t.Fatalf("client root = %q, want the module root %q", client.Root(), root)
	}
}

func TestManagerShutdownClosesEveryServer(t *testing.T) {
	useFakeServer(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	client, release, err := m.Acquire(ctx, goModule(t), lang)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	m.Shutdown()

	if got := m.Servers(); len(got) != 0 {
		t.Fatalf("Servers() = %+v after Shutdown, want none", got)
	}
	if client.PID() > 0 {
		t.Error("Shutdown left the server process running")
	}
	// A shutdown manager is not reusable; it says so instead of starting a
	// process it would immediately have to kill.
	_, _, err = m.Acquire(ctx, goModule(t), lang)
	var ue *UnavailableError
	if !errors.As(err, &ue) || ue.Reason != "shutting down" {
		t.Fatalf("Acquire after Shutdown = %v, want a shutting-down UnavailableError", err)
	}
}

// TestManagerStartsOneServerForConcurrentCallers: two queries arriving at a
// cold root share one start instead of racing to spawn a server each.
//
// The race is not cosmetic. Two gopls processes indexing the same module contend
// for the module cache and the CPU, so both settle late — the one that loses is
// left orphaned, and the query that asked first is the one that reads
// "project did not load".
func TestManagerStartsOneServerForConcurrentCallers(t *testing.T) {
	root := goModule(t)
	lang := goLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()

	// The stub holds the first handshake open so the second caller is certain to
	// arrive while a start is in flight: a test that let the start finish first
	// would pass with or without the latch.
	inside := make(chan struct{})
	proceed := make(chan struct{})
	var calls int64
	prev := launchFor
	launchFor = func(l Language, r string) (Launch, bool) {
		if atomic.AddInt64(&calls, 1) == 1 {
			close(inside)
			<-proceed
		}
		return Launch{
			Name:       l.Primary().Name,
			LanguageID: l.ID,
			Command:    os.Args[0],
			Env:        []string{"FAKELSP_HELPER=1"},
			Root:       r,
		}, true
	}
	t.Cleanup(func() { launchFor = prev })

	type result struct {
		client  *Client
		release func()
		err     error
	}
	acquire := func() <-chan result {
		ch := make(chan result, 1)
		go func() {
			c, release, err := m.Acquire(context.Background(), root, lang)
			ch <- result{c, release, err}
		}()
		return ch
	}

	first := acquire()
	<-inside
	second := acquire()
	// Long enough that a rival start would already have happened: without the
	// latch the second caller reaches the spawn in microseconds.
	time.Sleep(100 * time.Millisecond)
	close(proceed)

	a, b := <-first, <-second
	for i, r := range []result{a, b} {
		if r.err != nil {
			t.Fatalf("concurrent Acquire #%d: %v", i+1, r.err)
		}
		if r.release != nil {
			defer r.release()
		}
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("launch called %d times for two concurrent callers, want 1", got)
	}
	if a.client != b.client {
		t.Error("the two callers got different servers for the same root")
	}
	if got := m.Servers(); len(got) != 1 {
		t.Errorf("Servers() = %+v, want the single warm server", got)
	}
}
