package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file tests the catalogue itself: for every language, that the launch
// configuration FastExec would use is the one it says it is — the executable it
// resolves, the arguments it passes, the extensions and markers that select it,
// and the clean degradation when the server is not on this machine.
//
// No real server is installed by these tests and none needs to be: each case
// plants a fake executable where discovery looks for a server, so the test is
// about the configuration, not about this PC's PATH.

// plantServer drops a runnable stub for one server into the FastExec data
// directory, which is where discovery looks first.
func plantServer(t *testing.T, home string, srv Server) string {
	t.Helper()
	dir := filepath.Join(home, "lsp", srv.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	bin := filepath.Join(dir, execName(srv.Name))
	if err := os.WriteFile(bin, []byte("stub\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", bin, err)
	}
	return bin
}

// isolatedHome points the data directory (and therefore {data} and
// {workspace}) at a temp dir, so a planted stub cannot be confused with
// something already installed on this machine.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FASTEXEC_HOME", home)
	return home
}

// argsFor is the argv every language passes today, after the executable. It is
// the expectation the test holds the catalogue to; a language that gains an
// argument has to change this line too.
var argsFor = map[string][]string{
	"go":         nil,
	"csharp":     nil,
	"typescript": {"--stdio"},
	"python":     {"--stdio"}, // pyright is preferred over pylsp
	"cpp":        nil,
	"rust":       nil,
	"java":       {"-data"}, // the workspace directory follows; checked below
	"kotlin":     nil,
	"zig":        nil,
	"powershell": nil, // checked by TestPowerShellLaunchesThroughItsBundle
	"shell":      {"start"},
	"json":       {"--stdio"},
	"yaml":       {"--stdio"},
	"markdown":   nil,
}

// TestCatalogueIsWellFormed holds the whole table to the invariants everything
// downstream relies on: an id, a name, at least one server with an install
// command, and extensions that are unambiguous.
func TestCatalogueIsWellFormed(t *testing.T) {
	if len(Languages) < 14 {
		t.Fatalf("catalogue has %d languages, expected the full set", len(Languages))
	}
	seenID := map[string]bool{}
	seenExt := map[string]string{}
	for _, lang := range Languages {
		if lang.ID == "" || lang.Name == "" {
			t.Errorf("%q: missing id or name", lang.ID)
		}
		if seenID[lang.ID] {
			t.Errorf("duplicate language id %q", lang.ID)
		}
		seenID[lang.ID] = true
		if len(lang.Servers) == 0 {
			t.Errorf("%s: no server at all", lang.ID)
			continue
		}
		if lang.Primary().Name != lang.Servers[0].Name {
			t.Errorf("%s: Primary is not the first server", lang.ID)
		}
		for _, srv := range lang.Servers {
			if srv.Name == "" {
				t.Errorf("%s: a server has no executable name", lang.ID)
			}
			// `lsp doctor` promises a command for every missing server.
			if srv.Install == "" {
				t.Errorf("%s/%s: no install command, so doctor would be a dead end", lang.ID, srv.Name)
			}
		}
		if len(lang.Exts) == 0 {
			t.Errorf("%s: no extension, so it can never be detected", lang.ID)
		}
		for _, ext := range lang.Exts {
			if !strings.HasPrefix(ext, ".") || ext != strings.ToLower(ext) {
				t.Errorf("%s: extension %q is not a lowercase \".ext\"", lang.ID, ext)
			}
			if owner, dup := seenExt[ext]; dup {
				t.Errorf("extension %q claimed by both %s and %s", ext, owner, lang.ID)
			}
			seenExt[ext] = lang.ID
		}
		if argsFor[lang.ID] == nil && !languageHasNoArgs(lang.ID) {
			t.Errorf("%s: not covered by the args table", lang.ID)
		}
	}
	for id := range argsFor {
		if _, ok := LanguageByID(id); !ok {
			t.Errorf("args table names unknown language %q", id)
		}
	}
}

// languageHasNoArgs says which catalogue ids genuinely take no argument, so the
// args table uses nil for "none" and the check above still catches a language
// that was added without updating the table.
func languageHasNoArgs(id string) bool {
	switch id {
	case "go", "csharp", "cpp", "rust", "kotlin", "zig", "markdown", "powershell":
		return true
	}
	return false
}

// TestEveryLanguageLaunchesItsOwnConfiguration is the per-language case: with a
// stub planted where discovery looks, the language must resolve that stub and
// pass exactly the arguments the catalogue documents.
func TestEveryLanguageLaunchesItsOwnConfiguration(t *testing.T) {
	for _, lang := range Languages {
		t.Run(lang.ID, func(t *testing.T) {
			home := isolatedHome(t)
			srv := lang.Primary()
			bin := plantServer(t, home, srv)
			// A bundle-backed server needs its launcher on disk to count as
			// installed; plant it so this case tests the launch, not Requires.
			for _, req := range srv.Requires {
				p := expand(req, srv, "")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("stub\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			root := t.TempDir()
			got, ok := LaunchFor(lang, root)
			if !ok {
				t.Fatalf("%s: not launched although %s was planted in the data dir", lang.Name, bin)
			}
			if got.Command != bin {
				t.Errorf("command = %q, want the planted %q", got.Command, bin)
			}
			if got.LanguageID != lang.ID {
				t.Errorf("languageId = %q, want %q", got.LanguageID, lang.ID)
			}
			want := argsFor[lang.ID]
			if lang.ID == "java" {
				// {workspace} must have become a real per-project directory,
				// and it must not be inside the repository being edited.
				if len(got.Args) != 2 || got.Args[0] != "-data" {
					t.Fatalf("jdtls args = %q, want -data <workspace>", got.Args)
				}
				ws := got.Args[1]
				if !filepath.IsAbs(ws) {
					t.Errorf("jdtls workspace %q is not absolute", ws)
				}
				if strings.Contains(ws, root) {
					t.Errorf("jdtls workspace %q lives inside the repository", ws)
				}
				if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
					t.Errorf("jdtls workspace %q was not created: %v", ws, err)
				}
				return
			}
			if lang.ID == "powershell" {
				// The one launch built by string surgery; its own test below
				// checks the command line in full.
				if len(got.Args) != 5 || got.Args[0] != "-NoLogo" || got.Args[3] != "-Command" {
					t.Fatalf("powershell args = %q, want the pwsh -Command launcher", got.Args)
				}
				return
			}
			if len(got.Args) != len(want) {
				t.Fatalf("args = %q, want %q", got.Args, want)
			}
			for i := range want {
				if got.Args[i] != want[i] {
					t.Fatalf("args = %q, want %q", got.Args, want)
				}
			}
		})
	}
}

// TestPowerShellLaunchesThroughItsBundle pins the one launch that has to be
// built by string surgery: the server is a .ps1 inside the EditorServices
// bundle, started by the pwsh launcher, and both paths must be real.
func TestPowerShellLaunchesThroughItsBundle(t *testing.T) {
	home := isolatedHome(t)
	lang, ok := LanguageByID("powershell")
	if !ok {
		t.Fatal("powershell is not in the catalogue")
	}
	srv := lang.Primary()
	plantServer(t, home, srv)
	for _, req := range srv.Requires {
		p := expand(req, srv, "")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("stub\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	launch, ok := LaunchFor(lang, root)
	if !ok {
		t.Fatal("powershell did not launch")
	}
	joined := strings.Join(launch.Args, " ")
	data := filepath.Join(home, "lsp", "pwsh")
	for _, want := range []string{"-NoLogo", "-Stdio", "Start-EditorServices.ps1", "-LogPath"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q do not carry %q", joined, want)
		}
	}
	if !strings.Contains(joined, data) {
		t.Errorf("args %q do not point at the bundle %q", joined, data)
	}
	if strings.Contains(joined, "{") {
		t.Errorf("args %q still hold an unexpanded placeholder", joined)
	}
	if _, err := os.Stat(filepath.Join(data, "workspace")); err != nil {
		t.Errorf("the log directory was not laid out: %v", err)
	}
}

// TestServerOrderIsPreference: pyright is preferred, and pylsp is what a
// machine without npm gets — with its documented initializationOptions, which
// is the only configuration either server needs.
func TestServerOrderIsPreference(t *testing.T) {
	home := isolatedHome(t)
	lang, _ := LanguageByID("python")

	// Neither installed: nothing to launch.
	if _, ok := LaunchFor(lang, t.TempDir()); ok {
		t.Fatal("python launched with no server installed")
	}

	// Only the fallback: pylsp answers, with follow_imports on.
	pylsp := lang.Servers[1]
	plantServer(t, home, pylsp)
	launch, ok := LaunchFor(lang, t.TempDir())
	if !ok {
		t.Fatal("pylsp was planted but python did not launch")
	}
	if launch.Name != "pylsp" {
		t.Fatalf("server = %q, want pylsp", launch.Name)
	}
	followImports(t, launch.InitOptions)

	// Both: pyright wins, and carries no initializationOptions of its own.
	plantServer(t, home, lang.Primary())
	launch, ok = LaunchFor(lang, t.TempDir())
	if !ok {
		t.Fatal("python did not launch")
	}
	if launch.Name != "pyright-langserver" {
		t.Errorf("server = %q, want pyright-langserver to be preferred", launch.Name)
	}
	if len(launch.InitOptions) != 0 {
		t.Errorf("pyright got initializationOptions %v, want none", launch.InitOptions)
	}
}

func followImports(t *testing.T, opts map[string]any) {
	t.Helper()
	plugins, _ := opts["plugins"].(map[string]any)
	jedi, _ := plugins["jedi_definition"].(map[string]any)
	if jedi["follow_imports"] != true {
		t.Errorf("initializationOptions = %v, want plugins.jedi_definition.follow_imports = true", opts)
	}
}

// TestBundleServerIsAbsentWithoutItsLauncher: PowerShellEditorServices has no
// installer of its own, so a stray pwsh.exe must not make FastExec claim it can
// serve PowerShell — it would start and die.
func TestBundleServerIsAbsentWithoutItsLauncher(t *testing.T) {
	home := isolatedHome(t)
	lang, _ := LanguageByID("powershell")
	srv := lang.Primary()
	plantServer(t, home, srv) // the launcher is there, the bundle is not

	if Installed(lang) {
		t.Fatal("powershell reported as installed without Start-EditorServices.ps1")
	}
	_, _, tried, ok := FindServer(lang, "")
	if ok {
		t.Fatal("FindServer found a bundle-less server")
	}
	joined := strings.Join(tried, "\n")
	if !strings.Contains(joined, "required but missing") {
		t.Errorf("nothing in the search log explains the failure:\n%s", joined)
	}
}

// TestExtensionAndMarkerSelectTheLanguage walks the mapping in both
// directions, including the glob markers (a folder of loose .py files is a
// Python project, a build.gradle.kts is a Kotlin one).
func TestExtensionAndMarkerSelectTheLanguage(t *testing.T) {
	for _, lang := range Languages {
		for _, ext := range lang.Exts {
			got, ok := LanguageForPath("src/file" + ext)
			if !ok || got.ID != lang.ID {
				t.Errorf("LanguageForPath(%q) = %q/%v, want %s", ext, got.ID, ok, lang.ID)
			}
			if upper, ok := LanguageByExt(strings.ToUpper(ext)); !ok || upper.ID != lang.ID {
				t.Errorf("LanguageByExt(%q) did not match case-insensitively", strings.ToUpper(ext))
			}
		}
		if len(lang.Markers) == 0 {
			// No project concept: every directory is loadable.
			if !HasMarker(lang, t.TempDir()) {
				t.Errorf("%s: reported unloadable in an empty directory", lang.ID)
			}
			continue
		}
		// Empty directory: not this language's project.
		if HasMarker(lang, t.TempDir()) {
			t.Errorf("%s: markers %v matched an empty directory", lang.ID, lang.Markers)
		}
		// Each marker on its own must be enough.
		for _, m := range lang.Markers {
			root := t.TempDir()
			name := m
			if strings.ContainsAny(m, "*?") {
				name = "sample" + strings.TrimPrefix(m, "*")
			}
			if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !HasMarker(lang, root) {
				t.Errorf("%s: marker %s did not mark the directory loadable", lang.ID, m)
			}
		}
	}
}

// TestUnknownExtensionHasNoLanguage: an unconfigured language must degrade to
// "no language", which is what sends the caller to the graph by name.
func TestUnknownExtensionHasNoLanguage(t *testing.T) {
	for _, path := range []string{"a.rb", "a.pl", "a.lua", "a.hs", "Makefile", "a"} {
		if lang, ok := LanguageForPath(path); ok {
			t.Errorf("%s was claimed by %s", path, lang.ID)
		}
	}
}

// TestDetectInRanksByFileCount: the manager starts servers for what the
// repository is actually made of.
func TestDetectInRanksByFileCount(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	writeFixture(t, root, "a.py", "x = 1\n")
	writeFixture(t, root, "b.py", "y = 1\n")
	writeFixture(t, root, "c.py", "z = 1\n")
	writeFixture(t, root, "notes.md", "# hi\n")
	// A vendored tree must not sway the ranking.
	writeFixture(t, filepath.Join(root, "node_modules", "dep"), "index.js", "1\n")

	got := DetectIn(root)
	if len(got) < 3 {
		t.Fatalf("DetectIn = %v, want python, go and markdown", got)
	}
	if got[0].ID != "python" {
		t.Errorf("first = %s, want python (3 files)", got[0].ID)
	}
	for _, l := range got {
		if l.ID == "typescript" {
			t.Error("node_modules was scanned")
		}
	}
}

// launchRecord is what the fake server saw: its own command line and
// environment, and the initialize request it received.
type launchRecord struct {
	Argv        []string       `json:"argv"`
	RootURI     string         `json:"rootUri"`
	InitOptions map[string]any `json:"initializationOptions"`
	Env         []string       `json:"env"`
}

// runEchoed starts one server with FAKELSP_ECHO set, so the fake writes down
// what actually arrived instead of the test trusting the Config it built. The
// synthetic LSP_PROBE entry is added to the environment to prove the extra
// environment of a launch survives the trip.
func runEchoed(t *testing.T, lang Language, srv Server, root string) launchRecord {
	t.Helper()
	echo := filepath.Join(t.TempDir(), "echo.json")
	probe := filepath.Join(t.TempDir(), "probe")
	cfg := Config{
		Launch: Launch{
			Name:       srv.Name,
			LanguageID: lang.ID,
			Command:    os.Args[0],
			Args:       srv.Args,
			// FAKELSP_* turn this binary into the fake server; FAKELSP_ECHO
			// makes it record what it received.
			Env:         []string{"FAKELSP_HELPER=1", "FAKELSP_ECHO=" + echo, "LSP_PROBE=" + probe},
			InitOptions: srv.InitOptions,
			Root:        root,
		},
		Timeout:          5 * time.Second,
		HandshakeTimeout: 10 * time.Second,
	}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	raw, err := os.ReadFile(echo)
	if err != nil {
		t.Fatalf("the server recorded nothing: %v", err)
	}
	var rec launchRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("echo is not JSON: %v", err)
	}
	var sawProbe bool
	for _, e := range rec.Env {
		if e == "LSP_PROBE="+probe {
			sawProbe = true
		}
	}
	if !sawProbe {
		t.Errorf("the child did not receive LSP_PROBE=%s", probe)
	}
	return rec
}

// TestLaunchArgsReachTheProcess: the arguments a language configures are what
// the process is started with, not merely what the Config struct holds. A
// server started without its --stdio flag would sit silent forever.
func TestLaunchArgsReachTheProcess(t *testing.T) {
	lang, _ := LanguageByID("typescript")
	srv := lang.Primary()
	root := t.TempDir()
	rec := runEchoed(t, lang, srv, root)

	want := append([]string{os.Args[0]}, srv.Args...)
	if strings.Join(rec.Argv, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %q, want %q", rec.Argv, want)
	}
	if !strings.HasPrefix(rec.RootURI, "file://") {
		t.Errorf("rootUri = %q, want a file:// URI", rec.RootURI)
	}
}

// TestInitializationOptionsReachTheProcess: pyright is configured later, pylsp
// is configured here. When a language does send initializationOptions, it is
// the documented ones that arrive inside the initialize request.
func TestInitializationOptionsReachTheProcess(t *testing.T) {
	lang, _ := LanguageByID("python")
	rec := runEchoed(t, lang, lang.Servers[1], t.TempDir())
	followImports(t, rec.InitOptions)
}

// TestNoInitializationOptionsIsTheDefault: a server that is configured later
// must not receive an empty initializationOptions, which some servers read as
// "the client has opinions".
func TestNoInitializationOptionsIsTheDefault(t *testing.T) {
	for _, lang := range Languages {
		for _, srv := range lang.Servers {
			if srv.InitOptions == nil {
				continue
			}
			if len(srv.InitOptions) == 0 {
				t.Errorf("%s/%s: empty but non-nil initializationOptions", lang.ID, srv.Name)
			}
		}
	}
}
