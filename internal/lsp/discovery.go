package lsp

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"homelab/fastexec/internal/config"
)

// This file only looks for a language server that is already on this machine;
// it never downloads anything. Installing is a separate, explicit step with
// consent (see install.go): the setup and `fastexec lsp install <language>` show
// what would be downloaded and ask first. Discovery never triggers it — a server
// that is not present is reported as "not installed" plus the exact command that
// would install it.
//
// Discovery order, first hit wins:
//
//  1. FASTEXEC_LSP_PATH — extra directories, highest priority, for a server
//     that is installed somewhere unusual.
//  2. A language-server home: one directory per server. <data_dir>/lsp for the
//     servers that belong to FastExec, and any extra home from
//     FASTEXEC_LSP_HOME. See lspHomes.
//  3. language.Dirs — directories that hold this one server.
//  4. PATH.
//  5. Well-known per-ecosystem directories that installers do not always add to
//     PATH (GOPATH/bin, GOROOT/bin, ~/.cargo/bin, ~/.dotnet/tools, ...).

// Placeholders usable in a server's Args, Env and Requires. They are expanded
// when the launch configuration is built, so the catalogue stays free of any
// path that belongs to one machine:
//
//	{root}       the repository root the server is started for
//	{home}       the user's home directory
//	{data}       <data_dir>/lsp/<server>, where a user drops that server
//	{workspace}  <data>/workspace/<root slug>, a per-project scratch directory
//
// {workspace} is what makes jdtls possible: it refuses to start without a
// private workspace directory per project, and that directory must not live
// inside the repository being edited.
const (
	phRoot      = "{root}"
	phHome      = "{home}"
	phData      = "{data}"
	phWorkspace = "{workspace}"
)

// Server is one installable language server for a language. A language lists its
// servers in preference order: the first one this machine can run is the one
// FastExec starts. The others exist so `lsp doctor` can offer the alternative
// instead of a dead end.
type Server struct {
	// Name is the executable name, e.g. "pyright-langserver".
	Name string
	// Dir is the directory a user drops this server into under <data_dir>/lsp,
	// when it is not the executable name. The approved TypeScript install lives
	// in lsp/ts, not lsp/typescript-language-server, and both spellings are
	// accepted by discovery.
	Dir string
	// Args are always passed to the server. Placeholders allowed.
	Args []string
	// VersionArgs is how this server prints its version; nil means the
	// conventional "--version".
	VersionArgs []string
	// NoVersion marks a launcher with no version flag at all (jdtls,
	// PowerShellEditorServices). `lsp doctor` then reports the server as found
	// instead of inventing a probe whose failure looks like a broken install.
	NoVersion bool
	// Env are extra environment entries ("K=V") for the process. Placeholders
	// allowed.
	Env []string
	// InitOptions is sent as initializationOptions in the initialize request.
	// nil sends nothing, which is what most servers want: they are configured
	// later through workspace/didChangeConfiguration, and a server that reads
	// initializationOptions ignores unknown keys.
	InitOptions map[string]any
	// InitOptionsFor builds the initializationOptions of one launch, when they
	// depend on what was resolved: the executable path and the repository root.
	// It wins over InitOptions when both are set. The TypeScript entry is the
	// one that needs it — the server does not run at all unless it is told where
	// its tsserver is, and that path is only known at launch time. Nil means
	// "the static InitOptions are the whole story".
	InitOptionsFor func(bin, root string) map[string]any
	// Requires are files that must exist for this server to be usable at all:
	// a server that ships as a bundle needs its launcher on disk. A server whose
	// Requires are missing is treated as not installed. Placeholders allowed,
	// except {root} and {workspace} — discovery does not always know a root.
	Requires []string
	// Install is the exact command that installs this server. Text only:
	// FastExec never runs it.
	Install string
	// Note is a short honesty line shown by `lsp doctor` when this server is
	// missing: what it needs, or why no single command installs it.
	Note string

	// Dirs are extra directories that hold this server's executable under its
	// plain name. They are searched before PATH, so a server unpacked in a
	// directory of its own is found without editing PATH. Entries are expanded
	// against the environment; one that does not exist is skipped.
	Dirs []string
	// Customize, when set, completes a launch from the project root. It adds
	// arguments that depend on what is there, the environment the server needs
	// to run, and — with a short problem phrase — refuses a project the server
	// could not answer for anyway. A non-empty problem means "do not start":
	// the manager degrades with that phrase instead of wasting a process.
	Customize func(root string) (args, env []string, problem string)
	// BlockingProgress lists the $/progress title prefixes that mean "the
	// project is still loading". While one is in flight an empty answer means
	// "not yet", not "nothing", so the client waits and then degrades with
	// LoadingNote. Servers that load instantly (gopls) leave it nil.
	BlockingProgress []string
	// LoadingNote is the short phrase reported while a blocking load is in
	// flight, e.g. "loading C# solution".
	LoadingNote string
	// RenameByRefs says the server's rename is not per-symbol (csharp-ls
	// renames every overload of the name), so the client must build the edit
	// from the reference list instead. See Launch.RenameByRefs.
	RenameByRefs bool
}

// Language describes one ecosystem FastExec can drive through its own server.
type Language struct {
	// ID is the languageId sent to the server, e.g. "go".
	ID string
	// Name is for humans, e.g. "Go".
	Name string
	// Servers are the servers that can serve this language, best first. Every
	// one of them is searched by discovery, so a language installed under a
	// secondary name (Python: pyright-langserver or pylsp) is found and used as
	// is; the alternative names are the other entries of this list.
	Servers []Server
	// Exts are the file extensions that make this language worth starting.
	Exts []string
	// Markers are files whose presence at the repository root means the project
	// can actually be loaded by the server (go.mod, package.json, ...). An empty
	// list means the language has no project concept: any directory holding one
	// of its files is loadable, and the root is the file's own directory.
	Markers []string
	// StrictMarkers are the markers a server needs for an exact cross-file
	// answer, when it also runs without them (tsconfig.json, jsconfig.json).
	// RootFor prefers the nearest one; without it the server works on an
	// implicit project and ProjectNote says so.
	StrictMarkers []string
	// ProjectFiles, when set, is how the root is picked instead of Markers:
	// the nearest ancestor holding a file that matches the FIRST pattern wins,
	// and the next pattern is only tried when no ancestor has the first. An
	// ecosystem whose server loads one artifact best (C#: the .sln, not the
	// nearest .csproj) needs this order; Markers cannot express it.
	ProjectFiles []string
	// Details adds honesty lines to `lsp doctor` for a server that WAS found,
	// when the install can still be incomplete in a way the version string does
	// not show. The TypeScript entry is the one that needs it. Nil means "found
	// is the whole story".
	Details func(bin string) []string
	// Optional marks a language whose exact answer is a bonus rather than the
	// point: Markdown, JSON, YAML, shell and PowerShell are indexed by the
	// name-based graph already, so a missing server is not a degradation and
	// produces no note. For every other language a missing server is reported.
	Optional bool
}

// Primary is the server a fresh install would provide, used in messages and as
// the default when nothing was found.
func (l Language) Primary() Server {
	if len(l.Servers) == 0 {
		return Server{}
	}
	return l.Servers[0]
}

// Languages is the full catalogue, in the order `lsp doctor` prints it.
//
// Adding an ecosystem means adding one entry here: everything downstream
// (discovery, manager, doctor) is driven by this table.
var Languages = []Language{
	{
		ID: "go", Name: "Go",
		Exts:    []string{".go"},
		Markers: []string{"go.mod", "go.work"},
		Servers: []Server{{
			Name:        "gopls",
			VersionArgs: []string{"version"},
			Install:     "go install golang.org/x/tools/gopls@latest",
		}},
	},
	{
		ID: "csharp", Name: "C#",
		Exts:    []string{".cs", ".csx"},
		Markers: []string{"*.csproj", "*.sln", "*.fsproj"},
		// The solution is what csharp-ls loads best, so it wins over a .csproj
		// no matter how much deeper it sits in the tree. Without one, the
		// nearest .csproj is the project, and the server discovers it itself.
		ProjectFiles: []string{"*.sln", "*.csproj"},
		Servers: []Server{{
			Name: "csharp-ls",
			// A portable install lives in a directory of its own, which PATH
			// does not know about: the lab's portable csharp-ls is the one the
			// user approved. FASTEXEC_LSP_PATH (or FASTEXEC_LSP_HOME) is the
			// general way to say this on another machine.
			Dirs: []string{`W:\Data\fx-lsp\csharp-ls`},
			Env:  []string{"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1"},
			// No initializationOptions: csharp-ls 0.16 takes none, and passing
			// an object it does not know is how a server ends up failing to
			// start.
			Customize:        csharpCustomize,
			BlockingProgress: []string{"loading"},
			LoadingNote:      "loading C# solution",
			// csharp-ls 0.16 renames every overload that shares the name, so
			// its WorkspaceEdit would rewrite the wrong methods. Measured, not
			// guessed: see TestCSharpServerRenameIsNotPerSymbol.
			RenameByRefs: true,
			VersionArgs:  []string{"--version"},
			Install:      "dotnet tool install --global csharp-ls",
			Note: "needs a .NET SDK (FastExec never installs one) and a restored project (dotnet restore); " +
				"the first query in a big solution is answered from the name graph while it loads",
		}},
	},
	{
		ID: "typescript", Name: "TypeScript / JavaScript",
		Exts:          []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
		Markers:       []string{"package.json", "tsconfig.json", "jsconfig.json"},
		StrictMarkers: []string{"tsconfig.json", "jsconfig.json"},
		Details:       tsDoctorDetails,
		Servers: []Server{{
			Name: "typescript-language-server",
			Dir:  "ts",
			Args: []string{"--stdio"},
			// The tsserver path is only known once the executable and the root
			// are resolved, so this server builds its options per launch.
			InitOptionsFor: tsInitOptions,
			Install:        "npm install -g typescript typescript-language-server",
			Note:           "the server drives a tsserver: without typescript in the project (or next to the server) it starts and exits immediately",
		}},
	},
	{
		ID: "python", Name: "Python",
		Exts: []string{".py", ".pyi"},
		// The last marker is a glob: a directory holding any .py is loadable,
		// which is the honest answer for a folder of loose scripts.
		Markers: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile", "tox.ini", "*.py"},
		Servers: []Server{
			{
				Name: "pyright-langserver",
				Args: []string{"--stdio"},
				// pyright is configured through the client's settings, not here.
				Install: "npm install -g pyright",
				Note:    "pyright is the preferred server: it type-checks, pylsp mostly navigates",
			},
			{
				Name:    "pylsp",
				Install: "pip install python-lsp-server",
				Note:    "fallback for a machine without npm; it answers navigation and diagnostics, not types",
				// Documented python-lsp-server initializationOptions: follow an
				// import to the definition instead of stopping at the import line.
				InitOptions: map[string]any{
					"plugins": map[string]any{
						"jedi_definition": map[string]any{"follow_imports": true},
					},
				},
			},
		},
	},
	{
		ID: "cpp", Name: "C / C++",
		Exts:    []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"},
		Markers: []string{"CMakeLists.txt", "compile_commands.json", "Makefile", "meson.build", "*.vcxproj"},
		Servers: []Server{{
			Name:    "clangd",
			Install: "winget install LLVM.LLVM   (Linux: apt install clangd; macOS: brew install llvm)",
			Note: "clangd needs compile_commands.json to know the flags: generate it with " +
				"`cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON ..` (CMake, also copies it to build/), " +
				"`bear -- make` or `compiledb -n make`. Without it clangd guesses the flags and the answer is approximate",
		}},
	},
	{
		ID: "rust", Name: "Rust",
		Exts:    []string{".rs"},
		Markers: []string{"Cargo.toml", "rust-project.json"},
		Servers: []Server{{
			Name:    "rust-analyzer",
			Install: "rustup component add rust-analyzer",
			Note:    "a non-Cargo build (Bazel, a hand-written Makefile) needs a rust-project.json next to the sources",
		}},
	},
	{
		ID: "java", Name: "Java",
		Exts:    []string{".java"},
		Markers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", ".classpath"},
		Servers: []Server{{
			Name: "jdtls",
			// jdtls keeps its index in a workspace directory of its own and
			// refuses to run without one; it must not be the repository.
			Args:      []string{"-data", phWorkspace},
			NoVersion: true,
			Install: "no single command: download eclipse.jdt.ls (jdt-language-server-*.tar.gz) " +
				"and put its launcher (bin/jdtls) on PATH",
			Note: "a current jdtls needs a JDK 21+; it is a launcher script (Python), so it has no version flag to probe",
		}},
	},
	{
		ID: "kotlin", Name: "Kotlin",
		Exts:    []string{".kt", ".kts"},
		Markers: []string{"build.gradle.kts", "settings.gradle.kts", "build.gradle", "settings.gradle", "pom.xml"},
		Servers: []Server{{
			Name: "kotlin-language-server",
			Install: "scoop install kotlin-language-server   " +
				"(or unzip a release from fwcd/kotlin-language-server and put bin/ on PATH)",
		}},
	},
	{
		ID: "zig", Name: "Zig",
		Exts:    []string{".zig"},
		Markers: []string{"build.zig", "build.zig.zon"},
		Servers: []Server{{
			Name:    "zls",
			Install: "winget install zls   (Linux/macOS: download from zigtools/zls)",
		}},
	},
	{
		ID: "powershell", Name: "PowerShell", Optional: true,
		Exts: []string{".ps1", ".psm1", ".psd1", ".ps1xml"},
		Servers: []Server{{
			Name: "pwsh",
			// PowerShellEditorServices ships inside the VS Code extension and has
			// no installer of its own, so FastExec starts it through the pwsh
			// launcher it already needs. The bundle path is expanded at launch.
			Args: []string{
				"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				"& '" + phData + "/Start-EditorServices.ps1' " +
					"-HostName 'fastexec' -HostProfileId 'fastexec' -HostVersion '1.0.0' " +
					"-BundledModulesPath '" + phData + "' " +
					"-Stdio -LogPath '" + phWorkspace + "/pses.log' -LogLevel Warning",
			},
			NoVersion: true,
			Requires:  []string{phData + "/Start-EditorServices.ps1"},
			Install: "download PowerShellEditorServices from PowerShell/PowerShellEditorServices " +
				"(.zip release, or the copy inside the VS Code extension) and unzip it into " +
				"<data_dir>/lsp/pwsh/PowerShellEditorServices",
			Note: "no standalone installer: the server is the .ps1 launcher inside the bundle, " +
				"so without the bundle PowerShell stays by name",
		}},
	},
	{
		ID: "shell", Name: "Shell", Optional: true,
		Exts: []string{".sh", ".bash", ".zsh", ".ksh"},
		Servers: []Server{{
			Name:    "bash-language-server",
			Args:    []string{"start"},
			Install: "npm install -g bash-language-server",
			Note:    "diagnostics also want shellcheck on PATH; without it the server still navigates",
		}},
	},
	{
		ID: "json", Name: "JSON", Optional: true,
		Exts: []string{".json", ".jsonc"},
		Servers: []Server{{
			Name:    "vscode-json-language-server",
			Args:    []string{"--stdio"},
			Install: "npm install -g vscode-langservers-extracted",
		}},
	},
	{
		ID: "yaml", Name: "YAML", Optional: true,
		Exts: []string{".yaml", ".yml"},
		Servers: []Server{{
			Name:    "yaml-language-server",
			Args:    []string{"--stdio"},
			Install: "npm install -g yaml-language-server",
		}},
	},
	{
		ID: "markdown", Name: "Markdown", Optional: true,
		Exts: []string{".md", ".markdown"},
		Servers: []Server{{
			Name: "marksman",
			Install: "winget install marksman   " +
				"(Linux/macOS: download from artempyanykh/marksman)",
			Note: "optional: Markdown navigation and link lookup already come from the " +
				"documentation graph, so this only adds diagnostics and wiki-link awareness",
		}},
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

// execName is the name a server has on this platform when only one spelling is
// wanted: the .exe on Windows, the bare name elsewhere.
func execName(server string) string { return execNames(server)[0] }

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
	// FASTEXEC_LSP_PATH is not repeated here: Find has already searched it,
	// ahead of everything else.
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
func expandedLSPPath(srv Server) []string {
	var out []string
	for _, entry := range filepath.SplitList(os.Getenv("FASTEXEC_LSP_PATH")) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		for _, d := range []string{srv.Dir, srv.Name} {
			if d == "" {
				continue
			}
			out = append(out, filepath.Join(entry, d, "node_modules", ".bin"))
		}
		out = append(out, filepath.Join(entry, "node_modules", ".bin"))
	}
	return out
}

// lspHomes are the directories FastExec treats as a home for language servers:
// one subdirectory per server, named after the server, holding the executable.
// They are searched for every language, before PATH.
//
// The FastExec data directory comes first, so a server dropped there is the
// one FastExec uses. FASTEXEC_LSP_HOME replaces the machine-wide home below it
// — the layout is the same, so a user who keeps servers somewhere else only
// names the directory. The lab's shared location is only consulted when it
// exists: on any other machine the stat fails and nothing is reported as
// searched.
func lspHomes() []string {
	var homes []string
	add := func(p string) {
		p = strings.TrimSpace(expandPath(p))
		if p != "" && p != "." {
			homes = append(homes, filepath.Clean(p))
		}
	}
	explicit := filepath.SplitList(os.Getenv("FASTEXEC_LSP_HOME"))
	for _, d := range explicit {
		add(d)
	}
	add(dataLSPDir())
	if len(explicit) == 0 {
		add(labLSPHome())
	}

	seen := map[string]bool{}
	out := homes[:0]
	for _, h := range homes {
		if seen[strings.ToLower(h)] {
			continue
		}
		seen[strings.ToLower(h)] = true
		out = append(out, h)
	}
	return out
}

// serverDataDir is where a user drops one server for FastExec alone:
// <data_dir>/lsp/<server>. It is also the value of {data}.
func serverDataDir(srv Server) string {
	return filepath.Join(dataLSPDir(), srv.Name)
}

// workspaceDir is the per-project scratch directory of a server, and the value
// of {workspace}. It is derived from the root path and never lives inside the
// repository, because a server that keeps its index there would have it indexed
// by the name-based graph and edited by mistake.
func workspaceDir(srv Server, root string) string {
	return filepath.Join(serverDataDir(srv), "workspace", rootSlug(root))
}

// rootSlug turns a repository root into one readable directory name: the path
// with every separator folded to "-", truncated for sanity, plus a short hash so
// two long paths that share a prefix cannot collide.
func rootSlug(root string) string {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
	var b strings.Builder
	dash := false
	for _, r := range clean {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 48 {
		name = strings.Trim(name[:48], "-")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(clean))
	return fmt.Sprintf("%s-%08x", name, h.Sum32())
}

// expand replaces the placeholders of a server string. An unknown "{...}" is
// left alone: a server whose argument genuinely contains braces is not guessed
// at.
func expand(s string, srv Server, root string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	home, _ := os.UserHomeDir()
	return strings.NewReplacer(
		phRoot, root,
		phHome, home,
		phData, serverDataDir(srv),
		phWorkspace, workspaceDir(srv, root),
	).Replace(s)
}

// usesWorkspace reports whether a server needs its per-project directory, so it
// is only created for the servers that would otherwise fail to start.
func usesWorkspace(srv Server) bool {
	for _, s := range append(append([]string(nil), srv.Args...), srv.Env...) {
		if strings.Contains(s, phWorkspace) {
			return true
		}
	}
	return false
}

// missingRequires lists the Requires of a server that are not on disk. A server
// that ships as a bundle (PowerShellEditorServices) is unusable without them,
// and reporting it as "found" would produce an incomprehensible start error.
//
// root is only needed for placeholders that depend on it; discovery usually has
// none, which is why Requires must not use {root} or {workspace}.
func missingRequires(srv Server, root string) []string {
	var missing []string
	for _, r := range srv.Requires {
		p := expand(r, srv, root)
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	return missing
}

// labLSPHome is the shared language-server home of this lab's PC, with the
// same layout as the data directory. It is a convention of one machine, so it
// is a fallback that has to exist to count, and FASTEXEC_LSP_HOME overrides it.
func labLSPHome() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	const home = `W:\Data\fx-lsp`
	if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
		return ""
	}
	return home
}

// expandPath resolves environment references in a configured path. Both
// spellings are understood, $VAR/${VAR} and %VAR%, because a path written on
// Windows is written either way. A name that is not set makes the whole entry
// unusable: it is reported as empty instead of being searched as a literal
// "$VAR" directory, which would only add noise to what `lsp doctor` says it
// tried.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	var missing bool
	lookup := func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		missing = true
		return ""
	}
	out := os.Expand(p, lookup)
	// %VAR%: everything between two percent signs that is a valid name.
	out = percentVar.ReplaceAllStringFunc(out, func(m string) string {
		return lookup(m[1 : len(m)-1])
	})
	if missing {
		return ""
	}
	return out
}

// percentVar matches the Windows spelling of an environment reference.
var percentVar = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%`)

// findExec locates one server's executable. The second return is the list of
// places that were searched, so a failure can be explained instead of guessed
// at.
func findExec(srv Server) (string, []string, bool) {
	var tried []string
	try := func(cand string) (string, bool) {
		tried = append(tried, cand)
		if isExecutable(cand) {
			return cand, true
		}
		return "", false
	}

	names := execNames(srv.Name)

	// 1. FASTEXEC_LSP_PATH: directories that hold the executable directly.
	//    Highest priority, because naming a directory there is the user saying
	//    "use this one". A node package installed with --prefix hides its
	//    executables one level down, so the layouts of the package are tried
	//    under each entry as well (see expandedLSPPath).
	for _, d := range filepath.SplitList(os.Getenv("FASTEXEC_LSP_PATH")) {
		if d = expandPath(d); d == "" {
			continue
		}
		for _, name := range names {
			if p, ok := try(filepath.Join(d, name)); ok {
				return p, tried, true
			}
		}
	}
	for _, dir := range expandedLSPPath(srv) {
		for _, name := range names {
			if p, ok := try(filepath.Join(dir, name)); ok {
				return p, tried, true
			}
		}
	}

	// 2. Language-server homes, laid out as <home>/<server>: the usual
	//    sub-layouts of an extracted archive or a node package are accepted
	//    under it. srv.Dir is the short name a user may have used instead of the
	//    executable's ("ts" for the TypeScript server); both spellings are
	//    accepted.
	dirs := []string{srv.Name}
	if srv.Dir != "" && srv.Dir != srv.Name {
		dirs = append([]string{srv.Dir}, dirs...)
	}
	for _, home := range lspHomes() {
		for _, d := range dirs {
			base := filepath.Join(home, d)
			for _, name := range names {
				for _, cand := range serverLayouts(base, srv, name) {
					if p, ok := try(cand); ok {
						return p, tried, true
					}
				}
			}
		}
	}

	// 3. Directories this server declared for itself.
	for _, d := range srv.Dirs {
		if d = expandPath(d); d == "" {
			continue
		}
		for _, name := range names {
			if p, ok := try(filepath.Join(d, name)); ok {
				return p, tried, true
			}
		}
	}

	// 4. PATH. Every spelling of the name is tried — the .cmd shim of an npm
	// install is in names on Windows — and LookPath already honours PATHEXT.
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, tried, true
		}
	}
	tried = append(tried, "PATH")

	// 5. Known per-ecosystem directories.
	for _, dir := range searchDirs() {
		for _, name := range names {
			if p, ok := try(filepath.Join(dir, name)); ok {
				return p, tried, true
			}
		}
	}
	return "", tried, false
}

// serverLayouts lists where an executable sits inside a per-server directory:
// at the top, or in the bin/, libexec/, node_modules/.bin/ or
// <server>-<os>-<arch>/ subdirectory that a release archive unpacks into.
func serverLayouts(base string, srv Server, name string) []string {
	return []string{
		filepath.Join(base, name),
		filepath.Join(base, "bin", name),
		filepath.Join(base, srv.Name+"-"+runtime.GOOS+"-"+runtime.GOARCH, name),
		filepath.Join(base, "node_modules", ".bin", name),
		filepath.Join(base, "libexec", name),
	}
}

// FindServer returns the first server of a language that this machine can
// actually run, with its executable path and every place that was searched.
//
// root may be empty when the caller only wants to know whether the language is
// available at all (`lsp doctor`); it is passed through to the Requires of a
// server that needs it.
func FindServer(lang Language, root string) (Server, string, []string, bool) {
	var tried []string
	for _, srv := range lang.Servers {
		if missing := missingRequires(srv, root); len(missing) > 0 {
			for _, m := range missing {
				tried = append(tried, "required but missing: "+m)
			}
			continue
		}
		bin, t, ok := findExec(srv)
		tried = append(tried, t...)
		if ok {
			return srv, bin, tried, true
		}
	}
	return Server{}, "", tried, false
}

// Installed reports whether any server of a language is available on this
// machine. It is what tells an optional language (Markdown, JSON) apart from one
// that failed: a missing optional server is not a degradation.
func Installed(lang Language) bool {
	_, _, _, ok := FindServer(lang, "")
	return ok
}

// Find is FindServer for a caller that only wants the executable: the installer
// asks this question while planning, and has no use for the server struct.
func Find(lang Language) (string, []string, bool) {
	_, path, tried, ok := FindServer(lang, "")
	return path, tried, ok
}

// ServerNames are the executable names a language may be served by, best first.
// It is how the installer asks the catalogue which pins apply to a language,
// without knowing how the servers are spelled.
func (l Language) ServerNames() []string {
	out := make([]string, 0, len(l.Servers))
	for _, s := range l.Servers {
		out = append(out, s.Name)
	}
	return out
}

// ManualInstall is the command line a human would run to get this language's
// preferred server, used for the languages with no pinned download. It is the
// first server that names one; empty when the catalogue knows of none, and
// FastExec never runs it either way.
func (l Language) ManualInstall() string {
	for _, s := range l.Servers {
		if s.Install != "" {
			return s.Install
		}
	}
	return ""
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
// root, or reports that no server is installed. Placeholders in the server's
// arguments and environment are resolved here, so the process gets real paths.
// A server that Customizes itself also gets the arguments and environment that
// depend on the root, and can refuse the project outright: the returned Launch
// then carries Problem instead of a command, and the manager degrades with that
// phrase.
func LaunchFor(lang Language, root string) (Launch, bool) {
	srv, bin, _, ok := FindServer(lang, root)
	if !ok {
		return Launch{}, false
	}
	// A per-launch builder wins over the static options: servers that need it
	// cannot be configured before their executable and root are known.
	initOptions := srv.InitOptions
	if srv.InitOptionsFor != nil {
		initOptions = srv.InitOptionsFor(bin, root)
	}
	args := make([]string, 0, len(srv.Args))
	for _, a := range srv.Args {
		args = append(args, expand(a, srv, root))
	}
	env := make([]string, 0, len(srv.Env))
	for _, e := range srv.Env {
		env = append(env, expand(e, srv, root))
	}
	if usesWorkspace(srv) {
		// jdtls exits immediately when its workspace directory is missing.
		_ = os.MkdirAll(workspaceDir(srv, root), 0o755)
	}
	l := Launch{
		Name:             srv.Name,
		LanguageID:       lang.ID,
		Command:          bin,
		Args:             args,
		Env:              env,
		InitOptions:      initOptions,
		Root:             root,
		BlockingProgress: append([]string(nil), srv.BlockingProgress...),
		LoadingNote:      srv.LoadingNote,
		RenameByRefs:     srv.RenameByRefs,
	}
	if srv.Customize != nil {
		cargs, cenv, problem := srv.Customize(root)
		for _, a := range cargs {
			l.Args = append(l.Args, expand(a, srv, root))
		}
		for _, e := range cenv {
			l.Env = append(l.Env, expand(e, srv, root))
		}
		l.Problem = problem
	}
	return l, true
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
//
// An empty marker list means the language has no project concept and every
// directory is loadable.
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
