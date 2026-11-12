package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"homelab/fastexec/internal/config"
)

// FastExec never downloads a language server. A server is an optional,
// user-installed program: if it is not already on this machine, the answer is
// "not installed" plus the exact command that would install it. Nothing in this
// file executes that command.
//
// Discovery order, first hit wins:
//
//  1. FASTEXEC_LSP_PATH — extra directories, highest priority, for a server
//     that is installed somewhere unusual.
//  2. <data_dir>/lsp/<server>/ — where a user can drop a server for FastExec
//     alone, without touching PATH.
//  3. PATH.
//  4. Well-known per-ecosystem directories that installers do not always add to
//     PATH (GOPATH/bin, GOROOT/bin, ~/.cargo/bin, ~/.dotnet/tools, ...).

// Language describes one ecosystem FastExec can drive through its own server.
type Language struct {
	// ID is the languageId sent to the server, e.g. "go".
	ID string
	// Name is for humans, e.g. "Go".
	Name string
	// Server is the conventional executable name, e.g. "gopls".
	Server string
	// Dir is the directory a user drops this server into under
	// <data_dir>/lsp, when it is not the executable name. The approved
	// TypeScript install lives in lsp/ts, not lsp/typescript-language-server.
	Dir string
	// Exts are the file extensions that make this language worth starting.
	Exts []string
	// Markers are files whose presence at the repository root means the project
	// can actually be loaded by the server (go.mod, package.json, ...).
	Markers []string
	// StrictMarkers are the markers a server needs for an exact cross-file
	// answer, when it also runs without them (tsconfig.json, jsconfig.json).
	// RootFor prefers the nearest one; without it the server works on an
	// implicit project and ProjectNote says so.
	StrictMarkers []string
	// Args are always passed to the server.
	Args []string
	// InitOptions builds the initializationOptions of one launch. It receives
	// the resolved executable and the repository root, because some servers
	// need to be told where their own backend is. Nil means "send none".
	InitOptions func(bin, root string) map[string]any
	// VersionArgs is how this server prints its version. The default is
	// "--version"; gopls spells it differently.
	VersionArgs []string
	// Install is the exact command that installs this server. Text only:
	// FastExec never runs it.
	Install string
	// Note is a short honesty line shown by `lsp doctor` when the server is
	// missing and no single command installs it.
	Note string
	// Details adds honesty lines to `lsp doctor` for a server that WAS found,
	// when the install can still be incomplete in a way the version string does
	// not show. The TypeScript entry is the one that needs it. Nil means "found
	// is the whole story".
	Details func(bin string) []string
}

// Languages is the full catalogue, in the order `lsp doctor` prints it.
//
// Adding an ecosystem means adding one entry here: everything downstream
// (discovery, manager, doctor) is driven by this table.
var Languages = []Language{
	{
		ID: "go", Name: "Go", Server: "gopls",
		Exts:        []string{".go"},
		Markers:     []string{"go.mod", "go.work"},
		VersionArgs: []string{"version"},
		Install:     "go install golang.org/x/tools/gopls@latest",
	},
	{
		ID: "csharp", Name: "C#", Server: "csharp-ls",
		Exts:    []string{".cs", ".csx"},
		Markers: []string{"*.csproj", "*.sln", "*.fsproj"},
		Install: "dotnet tool install --global csharp-ls",
		Note:    "the project must restore first (dotnet restore); a solution that does not load answers nothing",
	},
	{
		ID: "typescript", Name: "TypeScript / JavaScript", Server: "typescript-language-server",
		Dir:           "ts",
		Exts:          []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
		Markers:       []string{"package.json", "tsconfig.json", "jsconfig.json"},
		StrictMarkers: []string{"tsconfig.json", "jsconfig.json"},
		Args:          []string{"--stdio"},
		InitOptions:   tsInitOptions,
		Details:       tsDoctorDetails,
		Install:       "npm install -g typescript typescript-language-server",
		Note:          "the server drives a tsserver: without typescript in the project (or next to the server) it starts and exits immediately",
	},
	{
		ID: "python", Name: "Python", Server: "pylsp",
		Exts:    []string{".py", ".pyi"},
		Markers: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"},
		Install: "pip install python-lsp-server",
	},
	{
		ID: "cpp", Name: "C / C++", Server: "clangd",
		Exts:    []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"},
		Markers: []string{"CMakeLists.txt", "compile_commands.json", "Makefile", "*.vcxproj"},
		Install: "winget install LLVM.LLVM   (Linux: apt install clangd; macOS: brew install llvm)",
		Note:    "without compile_commands.json clangd guesses the flags and the answer is approximate",
	},
	{
		ID: "rust", Name: "Rust", Server: "rust-analyzer",
		Exts:    []string{".rs"},
		Markers: []string{"Cargo.toml"},
		Install: "rustup component add rust-analyzer",
	},
	{
		ID: "java", Name: "Java", Server: "jdtls",
		Exts:    []string{".java"},
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", ".classpath"},
		Install: "no single command: download eclipse.jdt.ls and put the launcher on PATH",
		Note:    "jdtls needs a JDK 21+ and a per-project workspace directory",
	},
	{
		ID: "kotlin", Name: "Kotlin", Server: "kotlin-language-server",
		Exts:    []string{".kt", ".kts"},
		Markers: []string{"build.gradle.kts", "settings.gradle.kts"},
		Install: "scoop install kotlin-language-server   (or download from fwcd/kotlin-language-server)",
	},
	{
		ID: "zig", Name: "Zig", Server: "zls",
		Exts:    []string{".zig"},
		Markers: []string{"build.zig", "build.zig.zon"},
		Install: "winget install zls   (Linux/macOS: download from zigtools/zls)",
	},
}

// LanguageByExt returns the language that owns an extension (".go"), and
// whether one was found.
func LanguageByExt(ext string) (Language, bool) {
	ext = strings.ToLower(ext)
	for _, l := range Languages {
		for _, e := range l.Exts {
			if e == ext {
				return l, true
			}
		}
	}
	return Language{}, false
}

// LanguageByID returns the catalogue entry with this id.
func LanguageByID(id string) (Language, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, l := range Languages {
		if l.ID == id {
			return l, true
		}
	}
	return Language{}, false
}

// LanguageForPath returns the language for a file path, if any.
func LanguageForPath(path string) (Language, bool) {
	return LanguageByExt(filepath.Ext(path))
}

// Disabled reports whether the user turned the LSP layer off. FASTEXEC_LSP=off
// (or 0/false/no) is the documented switch; it is also what tests use to prove
// FastExec still answers without any server.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FASTEXEC_LSP"))) {
	case "off", "0", "false", "no", "disable", "disabled":
		return true
	}
	return false
}

// execNames lists the file names a server may have on this platform, in the
// order they are looked for.
//
// On Windows the interesting one is not .exe: a server installed with npm is a
// .cmd shim that runs node, and the name without extension is a shell script.
// Both are runnable by exec.Command, so discovery must not stop at the .exe.
func execNames(server string) []string {
	if runtime.GOOS != "windows" {
		return []string{server}
	}
	if strings.HasSuffix(strings.ToLower(server), ".exe") {
		return []string{server}
	}
	return []string{server + ".exe", server + ".cmd", server + ".bat", server}
}

// searchDirs is the fixed part of the search path, after the two FastExec-owned
// locations and before PATH is consulted.
//
// Every entry is derived from an environment variable or the user's home, so
// nothing here is specific to one machine.
func searchDirs() []string {
	var dirs []string
	add := func(p string) {
		if p == "" {
			return
		}
		dirs = append(dirs, filepath.Clean(p))
	}
	for _, d := range filepath.SplitList(os.Getenv("FASTEXEC_LSP_PATH")) {
		add(strings.TrimSpace(d))
	}
	home, _ := os.UserHomeDir()
	// Servers installers forget to expose.
	add(filepath.Join(os.Getenv("GOPATH"), "bin"))
	if home != "" {
		add(filepath.Join(home, "go", "bin"))
		add(filepath.Join(home, ".cargo", "bin"))
		add(filepath.Join(home, ".dotnet", "tools"))
		add(filepath.Join(home, ".local", "bin"))
		add(filepath.Join(home, ".npm-global", "bin"))
		add(filepath.Join(home, "node_modules", ".bin"))
	}
	add(filepath.Join(os.Getenv("GOROOT"), "bin"))
	add(filepath.Join(os.Getenv("APPDATA"), "npm"))
	add(filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "nodejs"))
	add(filepath.Join(os.Getenv("CARGO_HOME"), "bin"))
	// Unix conventions. Harmless on Windows: a missing directory is skipped.
	add("/usr/local/bin")
	add("/usr/bin")
	add(filepath.Join(home, ".local", "share", "npm", "bin"))

	seen := map[string]bool{}
	out := dirs[:0]
	for _, d := range dirs {
		if d == "" || d == "." || seen[strings.ToLower(d)] {
			continue
		}
		seen[strings.ToLower(d)] = true
		out = append(out, d)
	}
	return out
}

// dataLSPDir is <data_dir>/lsp.
func dataLSPDir() string { return filepath.Join(config.Home(), "lsp") }

// expandedLSPPath turns every FASTEXEC_LSP_PATH entry into the directories a
// node package could hide its executables in. The point is that
//
//	FASTEXEC_LSP_PATH=W:\Data\fx-lsp
//
// finds a server installed with
//
//	npm install --prefix W:\Data\fx-lsp\ts typescript-language-server
//
// without the user having to name the .bin directory itself. Entries that are
// already a .bin or a plain directory still work: they are kept as they are by
// searchDirs.
func expandedLSPPath(lang Language) []string {
	var out []string
	for _, entry := range filepath.SplitList(os.Getenv("FASTEXEC_LSP_PATH")) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		for _, d := range []string{lang.Dir, lang.Server} {
			if d == "" {
				continue
			}
			out = append(out, filepath.Join(entry, d, "node_modules", ".bin"))
		}
		out = append(out, filepath.Join(entry, "node_modules", ".bin"))
	}
	return out
}

// Find locates the executable for a language. The second return is the list of
// places that were searched, so a failure can be explained instead of guessed
// at.
func Find(lang Language) (string, []string, bool) {
	var tried []string
	names := execNames(lang.Server)

	// 1. FastExec's own drop-in directory, including the usual sub-layouts of
	// an extracted archive or a node package. lang.Dir is the short name a user
	// may have used instead of the executable's ("ts" for the TypeScript
	// server); both spellings are accepted.
	dirs := []string{lang.Server}
	if lang.Dir != "" && lang.Dir != lang.Server {
		dirs = append([]string{lang.Dir}, dirs...)
	}
	for _, d := range dirs {
		base := filepath.Join(dataLSPDir(), d)
		for _, name := range names {
			for _, cand := range []string{
				filepath.Join(base, name),
				filepath.Join(base, "bin", name),
				filepath.Join(base, lang.Server+"-"+runtime.GOOS+"-"+runtime.GOARCH, name),
				filepath.Join(base, "node_modules", ".bin", name),
				filepath.Join(base, "libexec", name),
			} {
				tried = append(tried, cand)
				if isExecutable(cand) {
					return cand, tried, true
				}
			}
		}
	}

	// 2. PATH. LookPath already honours PATHEXT, so the .cmd shim of an npm
	// install is found here on Windows.
	if p, err := exec.LookPath(lang.Server); err == nil {
		return p, tried, true
	}
	tried = append(tried, "PATH")

	// 3. Known per-ecosystem directories.
	for _, dir := range append(expandedLSPPath(lang), searchDirs()...) {
		for _, name := range names {
			cand := filepath.Join(dir, name)
			tried = append(tried, cand)
			if isExecutable(cand) {
				return cand, tried, true
			}
		}
	}
	return "", tried, false
}

// isExecutable reports whether path is a regular, non-empty file we could run.
// An empty file is what a failed download leaves behind, and starting it would
// produce an incomprehensible error instead of a clean "not installed".
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if fi.Size() == 0 {
		return false
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}

// LaunchFor builds the launch configuration for a language in a repository
// root, or reports that no server is installed.
func LaunchFor(lang Language, root string) (Launch, bool) {
	bin, _, ok := Find(lang)
	if !ok {
		return Launch{}, false
	}
	var initOptions map[string]any
	if lang.InitOptions != nil {
		initOptions = lang.InitOptions(bin, root)
	}
	return Launch{
		Name:                  lang.Server,
		LanguageID:            lang.ID,
		Command:               bin,
		Args:                  append([]string(nil), lang.Args...),
		Root:                  root,
		InitializationOptions: initOptions,
	}, true
}

// DetectIn lists the languages that have at least one tracked extension under
// root, most files first. It powers `lsp doctor` and the manager's decision of
// which servers are worth starting for a repository.
//
// The scan is bounded: it walks directories, not file contents, and stops once
// every known language has been seen.
func DetectIn(root string) []Language {
	counts := map[string]int{}
	pending := []string{root}
	seenLang := 0
	for len(pending) > 0 && seenLang < len(Languages) {
		dir := pending[0]
		pending = pending[1:]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				if skipDir(name) {
					continue
				}
				pending = append(pending, filepath.Join(dir, name))
				continue
			}
			if lang, ok := LanguageByExt(filepath.Ext(name)); ok {
				if counts[lang.ID] == 0 {
					seenLang++
				}
				counts[lang.ID]++
			}
		}
	}
	out := make([]Language, 0, len(counts))
	for _, l := range Languages {
		if counts[l.ID] > 0 {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return counts[out[i].ID] > counts[out[j].ID] })
	return out
}

// skipDir keeps the scan out of directories that never hold the source being
// edited and can be enormous.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "target", "bin", "obj", "dist", "build",
		".git", ".svn", ".hg", ".idea", ".vs", ".venv", "venv", "__pycache__",
		".next", ".nuxt", ".gradle", ".mypy_cache", ".pytest_cache", "zig-cache",
		"zig-out", "packages", ".cache":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// HasMarker reports whether the repository looks like a loadable project for
// this language. Starting a server on a directory that cannot be loaded only
// wastes a process: gopls on a tree without go.mod answers nothing useful.
func HasMarker(lang Language, root string) bool {
	if len(lang.Markers) == 0 {
		return true
	}
	return hasAnyMarker(lang.Markers, root)
}

// HasStrictMarker reports whether the root carries the config file that makes
// the server answer exactly across files. It is a stricter question than
// HasMarker: for the TypeScript server a package.json makes the project loadable
// but leaves tsserver on a guessed project, which answers some cross-file
// questions incompletely.
func HasStrictMarker(lang Language, root string) bool {
	if len(lang.StrictMarkers) == 0 {
		return false
	}
	return hasAnyMarker(lang.StrictMarkers, root)
}

// hasAnyMarker is the shared body of HasMarker/HasStrictMarker. An empty list
// means "" — the caller decides what that means, because "no markers declared"
// is "always loads" for Markers and "no strict notion" for StrictMarkers.
func hasAnyMarker(markers []string, root string) bool {
	for _, m := range markers {
		if strings.ContainsAny(m, "*?") {
			if matches, _ := filepath.Glob(filepath.Join(root, m)); len(matches) > 0 {
				return true
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(root, m)); err == nil {
			return true
		}
	}
	return false
}
