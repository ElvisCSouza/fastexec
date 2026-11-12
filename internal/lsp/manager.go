package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Defaults for the resident manager. They are deliberately generous: a
// language server is expensive to start (gopls indexes the whole module) and
// cheap to keep, so the manager prefers a warm server for the window a working
// session actually lasts.
const (
	// DefaultIdleTimeout is how long a server may sit unused before it is shut
	// down. Fifteen minutes outlives a burst of edits and does not outlive the
	// coffee break.
	DefaultIdleTimeout = 15 * time.Minute
	// DefaultMaxServers caps how many server processes FastExec keeps alive at
	// once across every repository and language.
	DefaultMaxServers = 4
	// failCooldown is how long a failed start is remembered. Without it, a
	// repository whose server crashes on load would spawn a doomed process on
	// every single query.
	failCooldown = 2 * time.Minute
	// sweepInterval is how often idle servers are reaped.
	sweepInterval = time.Minute
	// maxRootDepth bounds the walk up from a file looking for a project marker.
	maxRootDepth = 64
)

// UnavailableError reports that FastExec could not get a type-exact answer.
// The caller degrades to the name-based graph and says so; it is never an
// error the user sees as a failure.
type UnavailableError struct {
	// Reason is a short, lowercase phrase: "server not installed",
	// "TIMEOUT", "project did not load", ...
	Reason string
}

func (e *UnavailableError) Error() string { return "lsp unavailable: " + e.Reason }

// Degraded renders the one-line honesty note that replaces the exact answer.
// It is the only place that wording lives, so every tool says it the same way.
//
// A project that is still loading is not "unavailable": the server is there and
// working, and the next query is exact. Saying so — "lsp loading C# solution…;
// by name" — is the difference between a user who retries and a user who goes
// looking for a broken install.
func Degraded(err error) string {
	var le *LoadingError
	if errors.As(err, &le) {
		return "lsp " + le.Note + "…; by name"
	}
	var ue *UnavailableError
	if errors.As(err, &ue) && ue.Reason != "" {
		return "lsp unavailable (" + ue.Reason + "); by name"
	}
	return "lsp unavailable; by name"
}

func unavailable(reason string, args ...any) error {
	if len(args) > 0 {
		reason = fmt.Sprintf(reason, args...)
	}
	return &UnavailableError{Reason: reason}
}

// Unavailable builds the same degradation error for callers outside this package: a tool that could not get
// an exact answer withholds it the same way the manager does, and the wording of the note stays in one place.
func Unavailable(reason string, args ...any) error { return unavailable(reason, args...) }

// ServerInfo describes a live server, for `lsp doctor` and the daemon status.
type ServerInfo struct {
	Root     string
	Language string
	Server   string
	PID      int
	Idle     time.Duration
	Usage    int
}

// entry is one (repository root, language) slot.
type entry struct {
	root   string
	lang   Language
	client *Client

	// inflight counts callers currently using the client. A slot is never
	// evicted while inflight > 0.
	inflight int

	// fail records the last start failure and when it happened, so a doomed
	// server is not respawned on every query.
	fail   error
	failAt time.Time
}

type manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	idle    time.Duration
	max     int
	stopped bool
	started bool
	stop    chan struct{}
}

var (
	globalOnce sync.Once
	global     *manager
)

// Manager returns the process-wide manager.
//
// It lives in the resident daemon: tools run inside the daemon, so one
// singleton means one warm gopls per repository for every tool call, instead of
// a fresh process per query.
func Manager() *manager {
	globalOnce.Do(func() {
		global = &manager{
			entries: make(map[string]*entry),
			idle:    DefaultIdleTimeout,
			max:     DefaultMaxServers,
			stop:    make(chan struct{}),
		}
	})
	return global
}

// launchFor resolves the executable that serves a language in a root. It is a
// variable so tests can point the manager at a fake server without installing
// a real one anywhere on the machine.
var launchFor = LaunchFor

// SetIdleTimeout overrides the idle window (0 restores the default). Used by
// tests and by configuration; it does not disturb servers already running
// beyond applying to the next sweep.
func (m *manager) SetIdleTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d <= 0 {
		d = DefaultIdleTimeout
	}
	m.idle = d
}

// SetMaxServers overrides the simultaneous-server cap (minimum 1).
func (m *manager) SetMaxServers(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 1 {
		n = 1
	}
	m.max = n
}

func (m *manager) key(root string, lang Language) string {
	return strings.ToLower(filepath.Clean(root)) + "\x00" + lang.ID
}

// startSweeper launches the idle reaper once.
func (m *manager) startSweeper() {
	m.mu.Lock()
	already := m.started
	m.started = true
	m.mu.Unlock()
	if already {
		return
	}
	go func() {
		t := time.NewTicker(sweepInterval)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.sweep()
			}
		}
	}()
}

// sweep shuts down servers that have been idle past the window.
func (m *manager) sweep() {
	m.mu.Lock()
	var doomed []*entry
	for key, e := range m.entries {
		if e.inflight > 0 {
			continue
		}
		if e.client == nil {
			// A slot that only holds a failure record: keep it while the
			// cooldown still applies, forget it afterwards.
			if time.Since(e.failAt) >= failCooldown {
				delete(m.entries, key)
			}
			continue
		}
		if e.client.Idle() >= m.idle {
			doomed = append(doomed, e)
			delete(m.entries, key)
		}
	}
	m.mu.Unlock()
	for _, e := range doomed {
		e.client.Close()
	}
}

// Acquire returns a warm client for (root, language), starting one on demand.
//
// The returned release function must be called when the work is done; it keeps
// the server from being reaped while a request is in flight. On failure the
// error is always an *UnavailableError: the caller degrades to the name-based
// graph instead of reporting a failure.
func (m *manager) Acquire(ctx context.Context, root string, lang Language) (*Client, func(), error) {
	if Disabled() {
		return nil, nil, unavailable("disabled by FASTEXEC_LSP")
	}
	if root == "" {
		return nil, nil, unavailable("no repository root")
	}
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return nil, nil, unavailable("shutting down")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, unavailable("bad root: %v", err)
	}
	// A directory that cannot be loaded is not worth a process: gopls on a
	// tree without go.mod builds an empty view and answers nothing.
	if !HasMarker(lang, abs) {
		return nil, nil, unavailable("no %s project marker in %s", lang.Name, filepath.Base(abs))
	}

	m.startSweeper()
	key := m.key(abs, lang)

	m.mu.Lock()
	e := m.entries[key]
	if e != nil && e.client != nil {
		e.inflight++
		m.mu.Unlock()
		e.client.Touch()
		return e.client, m.releaseFunc(key), nil
	}
	if e != nil && e.fail != nil && time.Since(e.failAt) < failCooldown {
		fail := e.fail
		m.mu.Unlock()
		return nil, nil, fail
	}
	// Reserve the slot before spawning so two callers cannot start the same
	// server twice, and so the cap counts the server being started.
	if e == nil {
		e = &entry{root: abs, lang: lang}
		m.entries[key] = e
	}
	e.inflight++
	if err := m.evictForCapLocked(key); err != nil {
		e.inflight--
		if e.client == nil {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return nil, nil, err
	}
	m.mu.Unlock()

	launch, ok := launchFor(lang, abs)
	if !ok {
		m.recordFail(key, unavailable("%s not installed", lang.Primary().Name))
		return nil, nil, unavailable("%s not installed", lang.Primary().Name)
	}
	if launch.Problem != "" {
		// The server exists but cannot work here. This is deliberately not
		// remembered as a failure: the cause is on the user's side and is cheap
		// to check again (a missing SDK, a project not restored yet), and a
		// cooldown would keep answering "restore needed" for two minutes after
		// they ran the restore.
		m.mu.Lock()
		e.inflight--
		if e.client == nil {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return nil, nil, unavailable("%s", launch.Problem)
	}

	// Starting a server outlives the query that triggered it: a caller whose
	// context is cancelled mid-handshake must not leave a half-started process
	// behind, and the next caller should find it warm. The handshake timeout in
	// Config is what bounds the wait.
	client, err := New(context.WithoutCancel(ctx), Config{Launch: launch})
	if err != nil {
		reason := "server did not start"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			reason = "timed out starting " + launch.Name
		}
		m.recordFail(key, unavailable("%s", reason))
		return nil, nil, unavailable("%s", reason)
	}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		client.Close()
		m.dropEntry(key)
		return nil, nil, unavailable("shutting down")
	}
	e.client = client
	e.fail = nil
	m.mu.Unlock()
	return client, m.releaseFunc(key), nil
}

// evictForCapLocked frees a slot if the cap is reached, by closing the
// least-recently-used server that is not in use. The caller holds m.mu.
//
// The slot being started counts: once this start succeeds there is one more
// live server, so the cap is enforced against live+1. Counting only the
// already-started servers would let the steady state sit one above the cap.
func (m *manager) evictForCapLocked(keep string) error {
	live := m.liveCountLocked() + 1
	for live > m.max {
		var victim *entry
		var victimKey string
		for key, e := range m.entries {
			if key == keep || e.client == nil || e.inflight > 0 {
				continue
			}
			if victim == nil || e.client.Idle() > victim.client.Idle() {
				victim, victimKey = e, key
			}
		}
		if victim == nil {
			// Everything live is busy: the cap is a hard limit, and refusing is
			// better than stealing a server from a call that is using it.
			return unavailable("too many language servers running (max %d)", m.max)
		}
		delete(m.entries, victimKey)
		victim.client.Close()
		live--
	}
	return nil
}

func (m *manager) liveCountLocked() int {
	n := 0
	for _, e := range m.entries {
		if e.client != nil {
			n++
		}
	}
	return n
}

func (m *manager) releaseFunc(key string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if e := m.entries[key]; e != nil && e.inflight > 0 {
				e.inflight--
			}
			m.mu.Unlock()
		})
	}
}

// recordFail remembers a failed start against the slot. The entry is kept even
// though it has no client: deleting it here would throw away the record, and
// the cooldown that keeps a doomed server from being respawned on every query
// would never apply. sweep retires the record once it has expired.
func (m *manager) recordFail(key string, err error) {
	m.mu.Lock()
	if e := m.entries[key]; e != nil {
		if e.inflight > 0 {
			e.inflight--
		}
		e.fail = err
		e.failAt = time.Now()
	}
	m.mu.Unlock()
}

func (m *manager) dropEntry(key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

// Warm returns a client for (root, language) only if one is already running, and never starts one.
//
// It exists for the callers that cannot pay for a start. gopls on a cold module takes seconds; the
// edit-impact note is appended to an edit that has already been written and must not delay it. Such a
// caller uses the warm server when a previous query left one, and falls back to the name-based graph
// immediately when there is none — the same honest degradation, decided in microseconds instead of seconds.
func (m *manager) Warm(root string, lang Language) (*Client, func(), error) {
	if Disabled() {
		return nil, nil, unavailable("disabled by FASTEXEC_LSP")
	}
	if root == "" {
		return nil, nil, unavailable("no repository root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, unavailable("bad root: %v", err)
	}
	key := m.key(abs, lang)
	m.mu.Lock()
	e := m.entries[key]
	if e == nil || e.client == nil {
		// The failure record is read under the same lock that sweep uses to retire it: the entry stays
		// alive while this call runs, but its fields are not ours to read outside the lock.
		var fail error
		if e != nil && e.fail != nil && time.Since(e.failAt) < failCooldown {
			fail = e.fail
		}
		m.mu.Unlock()
		if fail != nil {
			return nil, nil, fail
		}
		return nil, nil, unavailable("no warm %s for %s", lang.Primary().Name, filepath.Base(abs))
	}
	e.inflight++
	m.mu.Unlock()
	e.client.Touch()
	return e.client, m.releaseFunc(key), nil
}

// WarmFile is AcquireFile without the spawning: the language server that owns path, if it happens to be up.
func (m *manager) WarmFile(path string) (*Client, func(), error) {
	abs, err := absPath(path)
	if err != nil {
		return nil, nil, unavailable("bad path: %v", err)
	}
	lang, ok := LanguageForPath(abs)
	if !ok {
		return nil, nil, unavailable("no language server for %s", filepath.Ext(abs))
	}
	return m.Warm(RootFor(lang, abs), lang)
}

// AcquireFile returns a warm client for the language that owns path, resolving
// the repository root by walking up to the nearest project marker.
func (m *manager) AcquireFile(ctx context.Context, path string) (*Client, func(), error) {
	abs, err := absPath(path)
	if err != nil {
		return nil, nil, unavailable("bad path: %v", err)
	}
	lang, ok := LanguageForPath(abs)
	if !ok {
		return nil, nil, unavailable("no language server for %s", filepath.Ext(abs))
	}
	return m.Acquire(ctx, RootFor(lang, abs), lang)
}

// RootFor is the repository root a server should be started for: the nearest
// ancestor of path that carries a project marker for the language. Without a
// marker, the file's own directory is the best we can honestly claim.
//
// When the language declares ProjectFiles, those patterns are tried in order
// and each one is searched all the way up before the next is considered. That
// order is the language's, not the tree's: C# loads a .sln best, so a solution
// at the top of the repository wins over the .csproj right next to the file.
// A language with no ProjectFiles match falls through to the marker walk below
// instead of giving up, so a language that declares both kinds keeps both.
//
// A language may also declare StrictMarkers — the config file (tsconfig.json)
// without which the server runs on a guessed project. Those win over the plain
// markers at any distance: a monorepo with a package.json at the top and a
// tsconfig.json three levels down belongs to the tsconfig, and starting tsserver
// at the package.json would throw away the compiler options the answer depends
// on. The nearest plain marker is remembered and returned only when no strict
// marker exists on the way up.
func RootFor(lang Language, path string) string {
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	if len(lang.ProjectFiles) > 0 {
		for _, pattern := range lang.ProjectFiles {
			if d, ok := nearestWith(dir, pattern); ok {
				return d
			}
		}
	}
	fallback := ""
	for i := 0; i < maxRootDepth; i++ {
		if HasStrictMarker(lang, dir) {
			return dir
		}
		if fallback == "" && HasMarker(lang, dir) {
			fallback = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}
	if fallback != "" {
		return fallback
	}
	return filepath.Dir(path)
}

// nearestWith walks up from dir looking for a directory that holds a file
// matching pattern.
func nearestWith(dir, pattern string) (string, bool) {
	for i := 0; i < maxRootDepth; i++ {
		if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) > 0 {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}
	return "", false
}

// Servers lists the live servers, newest use last.
func (m *manager) Servers() []ServerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerInfo, 0, len(m.entries))
	for _, e := range m.entries {
		if e.client == nil {
			continue
		}
		out = append(out, ServerInfo{
			Root:     e.root,
			Language: e.lang.ID,
			Server:   e.client.Name(),
			PID:      e.client.PID(),
			Idle:     e.client.Idle(),
			Usage:    e.inflight,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].Language < out[j].Language
	})
	return out
}

// StopRoot closes and forgets every server of one repository root.
//
// It is the narrow counterpart of Shutdown: the manager stays usable and the
// reaper keeps running, so a root that goes away (repository deleted, moved, or
// a test's temporary directory) can give its processes back without taking the
// warm servers of every other root down with it. On Windows this is what
// actually releases the directory: a live server has it as its working
// directory and a removable directory cannot be removed from under it.
func (m *manager) StopRoot(root string) {
	want := strings.ToLower(filepath.Clean(root))
	m.mu.Lock()
	var clients []*Client
	for key, e := range m.entries {
		if e.client == nil || strings.ToLower(filepath.Clean(e.root)) != want {
			continue
		}
		clients = append(clients, e.client)
		delete(m.entries, key)
	}
	m.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

// Shutdown closes every server and stops the reaper. Called when the daemon
// exits; the manager is not reusable afterwards.
func (m *manager) Shutdown() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stop)
	clients := make([]*Client, 0, len(m.entries))
	for key, e := range m.entries {
		if e.client != nil {
			clients = append(clients, e.client)
		}
		delete(m.entries, key)
	}
	m.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}
