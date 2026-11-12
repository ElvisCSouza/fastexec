package lsp

// install.go: o instalador dos servidores de linguagem. É a única parte do
// FastExec que baixa e executa alguma coisa, e só faz isso quando o usuário
// pede: o catálogo tem versões FIXAS, cada item mostra de onde vem e quanto
// pesa, e nada é instalado sem confirmação daquela linguagem.
//
// Regras que este arquivo garante:
//
//   - Nada é baixado para descobrir a versão: as versões estão fixadas aqui,
//     com o SHA-256 que a origem publica (release do GitHub) ou o mecanismo de
//     integridade da própria origem (GOSUMDB, NuGet, npm).
//   - Pré-requisito que falta não é instalado: o item fica de fora com o
//     comando que o usuário deve rodar.
//   - Nada vai para o PATH e nada pede administrador: tudo fica em
//     <data_dir>/lsp/<servidor>/.
//   - Instalação é atômica: baixa e monta numa pasta de passagem, confere, e só
//     então troca a pasta final por rename. Falha de rede deixa o que existia
//     intacto e nada pela metade.
//   - `remove` só apaga pasta que este instalador criou (tem installed.json).

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"homelab/fastexec/internal/graph"
)

// stateFile marca uma pasta como instalada por este instalador.
const stateFile = "installed.json"

// maxDownload bounds one artifact. A wrong URL that streams forever must not
// fill the disk; every real artifact here is under 30 MB.
const maxDownload = 400 << 20

// Kind is how a pinned server is obtained.
type Kind string

const (
	KindGo     Kind = "go install"   // go install <pkg>@<version> with GOBIN
	KindDotnet Kind = "dotnet tool"  // dotnet tool install --tool-path
	KindNpm    Kind = "npm install"  // npm install --prefix
	KindZip    Kind = "release .zip" // GitHub release asset, zip
	KindGz     Kind = "release .gz"  // GitHub release asset, a single gzipped binary
)

// Asset is one downloadable file of a release, for one OS/architecture.
type Asset struct {
	OS     string  `json:"os"`
	Arch   string  `json:"arch"`
	URL    string  `json:"url"`
	SHA256 string  `json:"sha256"`
	MB     float64 `json:"mb"`
}

// Pin is a fixed, verified version of one language server.
type Pin struct {
	// Lang is the catalogue id this pin serves ("go", "csharp", ...).
	Lang string
	// Server is the executable name; it is also the folder name under
	// <data_dir>/lsp/.
	Server string
	// Version is the exact version installed. Never "latest".
	Version string
	Kind    Kind
	// Pkg is the module path, NuGet id or npm package.
	Pkg string
	// Pkgs are extra packages installed in the same step (npm: the server and
	// its typescript peer).
	Pkgs []string
	// Assets are the release files, one per OS/arch.
	Assets []Asset
	// Size is the approximate download size of an ecosystem install.
	Size string
	// Origin is where the bytes come from, for the confirmation screen.
	Origin string
	// License is the licence of the third-party component.
	License string
	// Home is the project page, shown by `lsp doctor`.
	Home string
	// Requires are the tools that must already exist on this machine.
	Requires []string
	// Note is an honesty line for what the installer cannot promise.
	Note string
}

// Pins is the catalogue: one entry per language that has a reproducible,
// non-interactive install. Languages without an entry (clangd, jdtls,
// kotlin-language-server) are reported as "manual" with the catalogue's own
// install command — an installer that pretends to install them would either
// download a multi-hundred-MB SDK or lie.
var Pins = []Pin{
	{
		Lang: "go", Server: "gopls", Version: "v0.24.0", Kind: KindGo,
		Pkg: "golang.org/x/tools/gopls", Size: "~15 MB of source, compiled here",
		Origin: "proxy.golang.org (checksummed by GOSUMDB)", License: "BSD-3-Clause",
		Home: "https://github.com/golang/tools", Requires: []string{"go"},
	},
	{
		Lang: "csharp", Server: "csharp-ls", Version: "0.28.0", Kind: KindDotnet,
		Pkg: "csharp-ls", Size: "~4 MB",
		Origin: "nuget.org (package hash verified by the client)", License: "MIT",
		Home: "https://github.com/razzmatazz/csharp-language-server", Requires: []string{"dotnet"},
		Note: "the .NET SDK must be installed: csharp-ls is a .NET tool, a runtime alone cannot install it",
	},
	{
		Lang: "typescript", Server: "typescript-language-server", Version: "6.0.3", Kind: KindNpm,
		Pkg: "typescript-language-server", Pkgs: []string{"typescript@7.0.2"}, Size: "~25 MB with typescript",
		Origin: "registry.npmjs.org (integrity checked by npm)", License: "MIT (typescript: Apache-2.0)",
		Home:     "https://github.com/typescript-language-server/typescript-language-server",
		Requires: []string{"node", "npm"},
	},
	{
		Lang: "python", Server: "pyright-langserver", Version: "1.1.414", Kind: KindNpm,
		Pkg: "pyright", Size: "~20 MB",
		Origin: "registry.npmjs.org (integrity checked by npm)", License: "MIT",
		Home: "https://github.com/microsoft/pyright", Requires: []string{"node", "npm"},
		Note: "the pinned server for Python is pyright (npm); an existing pylsp keeps working and is used if it is the one installed",
	},
	{
		Lang: "rust", Server: "rust-analyzer", Version: "2026-11-09", Kind: KindZip,
		Origin: "github.com/rust-lang/rust-analyzer releases", License: "MIT or Apache-2.0",
		Home: "https://rust-analyzer.github.io",
		Assets: []Asset{
			{OS: "windows", Arch: "amd64", MB: 17.2, SHA256: "973ab11e84955a7f8e3cf01ccd98a33f3ecbd6d4a14bf59bb164aa9e831c6fef",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-x86_64-pc-windows-msvc.zip"},
			{OS: "windows", Arch: "arm64", MB: 15.4, SHA256: "ca0770b76d3881642221d0437f7535e324f22a9499bcd00bbebeb0228617249e",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-aarch64-pc-windows-msvc.zip"},
			{OS: "linux", Arch: "amd64", MB: 14.9, SHA256: "8c039b25cb338b3970c731646677a4c9d4e9dd7a92c9ab9ae459c71467fbf4a9",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-x86_64-unknown-linux-gnu.gz"},
			{OS: "linux", Arch: "arm64", MB: 14.4, SHA256: "813ca3cec78ac6dab99c7bc9d51e63578f6d401b994ecf29cc0dcad86f9b2cd4",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-aarch64-unknown-linux-gnu.gz"},
			{OS: "darwin", Arch: "amd64", MB: 14.4, SHA256: "abf410a54e33e9f8b2d37f76dde8884db86a030d6a9cc70c0e3f87a77c084c5c",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-x86_64-apple-darwin.gz"},
			{OS: "darwin", Arch: "arm64", MB: 13.7, SHA256: "e43a65008f8f4291320f33c0545dd577ad7623a7407b584a27155833d3722dfb",
				URL: "https://github.com/rust-lang/rust-analyzer/releases/download/2026-11-09/rust-analyzer-aarch64-apple-darwin.gz"},
		},
		Note: "the .gz assets are used on Linux/macOS: the .tar.xz ones would need an xz decoder FastExec does not carry",
	},
	{
		Lang: "zig", Server: "zls", Version: "0.17.0", Kind: KindZip,
		Origin: "github.com/zigtools/zls releases", License: "MIT",
		Home: "https://github.com/zigtools/zls",
		Assets: []Asset{
			{OS: "windows", Arch: "amd64", MB: 4.6, SHA256: "b0e557b13032322f530a818ea4eb899a51a2e738808397a8cf3868fd56b73972",
				URL: "https://github.com/zigtools/zls/releases/download/0.17.0/zls-x86_64-windows.zip"},
			{OS: "windows", Arch: "arm64", MB: 4.4, SHA256: "98869d1d2e9532044a3bbb25be16a932041bdc2f422fe074cc499fd20d1b6f53",
				URL: "https://github.com/zigtools/zls/releases/download/0.17.0/zls-aarch64-windows.zip"},
		},
		Note: "only the Windows zip is pinned: the Linux/macOS assets are .tar.xz, which needs an xz decoder",
	},
}

// PinFor returns the pinned version for a language, if it has one.
func PinFor(lang Language) (Pin, bool) {
	return PinByServer(lang.ServerNames()...)
}

// PinByServer looks a pin up by executable name, including the alternative
// names a language accepts (python: pyright-langserver or pylsp).
func PinByServer(servers ...string) (Pin, bool) {
	for _, s := range servers {
		for _, p := range Pins {
			if p.Server == s {
				return p, true
			}
		}
	}
	return Pin{}, false
}

// Asset returns the release file for this OS/arch.
func (p Pin) Asset(goos, goarch string) (Asset, bool) {
	for _, a := range p.Assets {
		if a.OS == goos && a.Arch == goarch {
			return a, true
		}
	}
	return Asset{}, false
}

// SizeFor is the size to show before downloading: the asset's when the pin is
// a release file, the estimate otherwise.
func (p Pin) SizeFor(goos, goarch string) string {
	if a, ok := p.Asset(goos, goarch); ok {
		return fmt.Sprintf("~%.1f MB", a.MB)
	}
	return p.Size
}

// --- pré-requisitos ---

// Prereq is one tool a pin needs, and whether this machine has it.
type Prereq struct {
	Tool    string
	Why     string
	Found   bool
	Version string
	Install string
}

// prerequisite describes how to detect and how to install one tool. FastExec
// never installs any of them: it prints the command and moves on.
type prerequisite struct {
	tool    string
	probe   []string // prints the version; exit code decides
	why     string
	install string
}

var prerequisites = map[string]prerequisite{
	"go": {tool: "go", probe: []string{"go", "version"}, why: "compiles gopls",
		install: "winget install GoLang.Go   (Linux: apt install golang-go; macOS: brew install go)"},
	"node": {tool: "node", probe: []string{"node", "--version"}, why: "runs the npm-based servers",
		install: "winget install OpenJS.NodeJS.LTS   (Linux: apt install nodejs; macOS: brew install node)"},
	"npm": {tool: "npm", probe: []string{"npm", "--version"}, why: "installs the server package",
		install: "comes with Node.js: install Node.js LTS and reopen the terminal"},
	"dotnet": {tool: "dotnet", probe: []string{"dotnet", "--list-sdks"}, why: "installs csharp-ls as a .NET tool",
		install: "winget install Microsoft.DotNet.SDK.8   (Linux: apt install dotnet-sdk-8.0; macOS: brew install dotnet)"},
}

// lookPath and runProbe are seams: the tests replace them to describe a machine
// without Node, or with a dotnet that has no SDK, without touching this one.
var (
	lookPath = exec.LookPath
	runProbe = probe
)

// Prereqs checks, one by one, the tools a pin needs. A tool that is present but
// answers with an error (dotnet with only a runtime) counts as missing.
func Prereqs(p Pin) []Prereq {
	out := make([]Prereq, 0, len(p.Requires))
	for _, name := range p.Requires {
		spec, ok := prerequisites[name]
		if !ok {
			continue
		}
		pr := Prereq{Tool: name, Why: spec.why, Install: spec.install}
		if _, err := lookPath(name); err != nil {
			pr.Install = spec.install + " (not found on PATH)"
			out = append(out, pr)
			continue
		}
		pr.Found = true
		if v, err := runProbe(spec.probe); err == nil {
			pr.Version = v
		} else {
			pr.Found = false
			pr.Install = spec.install + " (found, but `" + name + " " + strings.Join(spec.probe, " ") + "` failed: " + err.Error() + ")"
		}
		out = append(out, pr)
	}
	return out
}

// probe runs a version flag with a short timeout and returns its first line.
func probe(args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if r := []rune(line); len(r) > 60 {
		line = string(r[:60])
	}
	return line, nil
}

// --- plano ---

// Item is one line of the install plan shown before anything is downloaded.
type Item struct {
	Lang Language
	Pin  Pin
	// Pinned is false for a language the installer cannot install reproducibly;
	// the item then only carries Manual.
	Pinned bool
	Size   string
	// Origin, Version, License are what the confirmation screen shows.
	Version, Origin, License string
	// Requires are the prerequisites, already checked.
	Requires []Prereq
	// Missing names the prerequisites that are not on this machine.
	Missing []string
	// Detected is whether the language was found in the scanned folders.
	Detected bool
	// Files is how many source files of that language were seen.
	Files int
	// Installed is the state written by a previous install, if any.
	Installed *ServerState
	// UpToDate is true when the installed version equals the pin.
	UpToDate bool
	// Found is where a server for this language already is (PATH, ~/go/bin...).
	Found string
	// Manual is the command to install it by hand when Pinned is false.
	Manual string
	// Ok is whether this item can be installed right now.
	Ok bool
}

// PlanItem builds the install plan for one language. roots are the per-language
// file counts from DetectCounts, used only for pre-selection.
func PlanItem(lang Language, roots map[string]int, goos, goarch string) Item {
	it := Item{Lang: lang, Files: roots[lang.ID], Detected: roots[lang.ID] > 0}
	if p, ok := PinFor(lang); ok {
		it.Pin, it.Pinned = p, true
		it.Version, it.Origin, it.License = p.Version, p.Origin, p.License
		it.Size = p.SizeFor(goos, goarch)
		if p.Kind == KindZip || p.Kind == KindGz {
			if _, ok := p.Asset(goos, goarch); !ok {
				it.Pinned, it.Manual = false, lang.ManualInstall()
				it.Size, it.Version, it.Origin = "", "", ""
			}
		}
		it.Requires = Prereqs(p)
		for _, r := range it.Requires {
			if !r.Found {
				it.Missing = append(it.Missing, r.Tool)
			}
		}
	} else {
		it.Manual = lang.ManualInstall()
	}
	if st, ok := installedAt(dataLSPDir(), serverDirName(it)); ok {
		it.Installed = &st
		it.UpToDate = it.Pinned && st.Version == it.Pin.Version
	}
	if path, _, ok := Find(lang); ok {
		it.Found = path
	}
	it.Ok = it.Pinned && len(it.Missing) == 0
	return it
}

// serverDirName is the folder under <data_dir>/lsp/ this item installs into.
func serverDirName(it Item) string {
	if it.Pinned {
		return it.Pin.Server
	}
	return it.Lang.Primary().Name
}

// PlanAll is PlanItem for every language in the catalogue, in catalogue order.
func PlanAll(roots map[string]int) []Item {
	out := make([]Item, 0, len(Languages))
	for _, l := range Languages {
		out = append(out, PlanItem(l, roots, runtime.GOOS, runtime.GOARCH))
	}
	return out
}

// DetectCounts counts, per language id, the source files seen under the given
// folders. It is the same bounded walk discovery uses: directories only, no
// file contents, and it never leaves the folder it was given.
func DetectCounts(roots ...string) map[string]int {
	counts := map[string]int{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		for id, n := range countIn(root) {
			counts[id] += n
		}
	}
	return counts
}

// DetectRoots are the folders scanned to pre-select languages: the indexed
// repositories plus whatever the caller passes (a work folder the user gave).
func DetectRoots(extra ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	for _, p := range extra {
		add(p)
	}
	if repos, err := graph.RepoRoots(); err == nil {
		for _, r := range repos {
			add(r)
		}
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return out
}

func countIn(root string) map[string]int {
	counts := map[string]int{}
	pending := []string{root}
	for len(pending) > 0 {
		dir := pending[0]
		pending = pending[1:]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				if !skipDir(e.Name()) {
					pending = append(pending, filepath.Join(dir, e.Name()))
				}
				continue
			}
			if lang, ok := LanguageByExt(filepath.Ext(e.Name())); ok {
				counts[lang.ID]++
			}
		}
	}
	return counts
}

// Selection is what the user chose to install.
type Selection struct {
	// Items are the languages that will be installed, in catalogue order.
	Items []Item
	// Blocked are languages the user asked for that cannot be installed right
	// now: no pinned version, or a missing prerequisite. Each one carries the
	// command that would fix it; the rest of the selection proceeds.
	Blocked []Item
	// Unknown are ids that are not in the catalogue at all.
	Unknown []string
}

// langAliases are the short spellings accepted on the command line and in the
// setup screen, so `--lang go,cs,ts` works as well as the catalogue ids. They
// are aliases for what the user types, never a second identity: everything
// downstream still uses the catalogue id.
var langAliases = map[string]string{
	"golang": "go",
	"cs":     "csharp", "c#": "csharp", "dotnet": "csharp",
	"ts": "typescript", "js": "typescript", "javascript": "typescript",
	"py": "python", "python3": "python",
	"rs": "rust",
	"c":  "cpp", "c++": "cpp", "clang": "cpp",
}

// NormalizeLangID turns a typed language name into the catalogue id. Unknown
// names are returned as they came, so the caller's error message quotes what the
// user typed.
func NormalizeLangID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if canon, ok := langAliases[id]; ok {
		return canon
	}
	return id
}

// Choose turns the requested language ids into a selection.
//
// An empty langs means "the detected set": the languages found in the scanned
// folders that can actually be installed. That is what `lsp install` with no
// argument does, and what the setup step pre-selects.
func Choose(items []Item, langs []string) (Selection, error) {
	var sel Selection
	if len(langs) == 0 {
		for _, it := range items {
			if !it.Detected {
				continue
			}
			if it.Ok {
				sel.Items = append(sel.Items, it)
			} else {
				sel.Blocked = append(sel.Blocked, it)
			}
		}
		return sel, nil
	}
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.Lang.ID] = it
	}
	seen := map[string]bool{}
	for _, raw := range langs {
		for _, id := range strings.Split(raw, ",") {
			id = NormalizeLangID(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			it, ok := byID[id]
			if !ok {
				sel.Unknown = append(sel.Unknown, id)
				continue
			}
			if it.Ok {
				sel.Items = append(sel.Items, it)
			} else {
				sel.Blocked = append(sel.Blocked, it)
			}
		}
	}
	if len(sel.Unknown) > 0 {
		ids := make([]string, 0, len(byID))
		for _, it := range items {
			ids = append(ids, it.Lang.ID)
		}
		return sel, fmt.Errorf("unknown language %s; the catalogue has: %s",
			strings.Join(sel.Unknown, ", "), strings.Join(ids, ", "))
	}
	return sel, nil
}

// --- downloader e runner ---

// Downloader fetches one URL into a file. It is an interface so tests can run
// the whole installer without touching the network.
type Downloader interface {
	Download(ctx context.Context, url, dest string) error
}

// Runner runs one external installer command (go install, npm, dotnet). Also an
// interface, for the same reason.
type Runner interface {
	Run(ctx context.Context, name string, args []string, dir string, extraEnv []string) error
}

// HTTPDownloader is the real one: no cookies, no credentials, one GET.
type HTTPDownloader struct{ Client *http.Client }

func (d HTTPDownloader) Download(ctx context.Context, url, dest string) error {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s answered %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return fmt.Errorf("download interrupted after %d bytes: %w", n, err)
	}
	if n > maxDownload {
		return fmt.Errorf("download exceeded %d MB, aborted", maxDownload>>20)
	}
	return f.Close()
}

// ExecRunner runs the real commands.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, dir string, extraEnv []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, tail(string(out), 3))
	}
	return nil
}

// tail keeps the last n lines of an installer's output: enough to explain the
// failure, short enough to stay readable.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// --- estado instalado ---

// ServerState is what an install writes to <data_dir>/lsp/<server>/installed.json.
type ServerState struct {
	Server  string `json:"server"`
	Lang    string `json:"language"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	Origin  string `json:"origin"`
	License string `json:"license"`
	Bin     string `json:"bin"`
	SHA256  string `json:"sha256,omitempty"`
	At      string `json:"installedAt"`
	By      string `json:"installedBy"`
}

// InstalledFor reads the state of one server folder under <data_dir>/lsp.
func InstalledFor(server string) (ServerState, bool) { return installedAt(dataLSPDir(), server) }

// installedAt reads the state of one server folder inside base.
func installedAt(base, server string) (ServerState, bool) {
	if server == "" || strings.ContainsAny(server, `/\`) {
		return ServerState{}, false
	}
	data, err := os.ReadFile(filepath.Join(base, server, stateFile))
	if err != nil {
		return ServerState{}, false
	}
	var st ServerState
	if json.Unmarshal(data, &st) != nil {
		return ServerState{}, false
	}
	return st, true
}

// InstalledList lists every server this installer put under <data_dir>/lsp/,
// sorted by name. Servers the user installed elsewhere are not in here: they
// are not ours to report as installed.
func InstalledList() []ServerState { return installedIn(dataLSPDir()) }

// InstallDir is where this installer puts servers: <data_dir>/lsp. It is the
// only place FastExec writes a language server, and the only place `lsp remove`
// deletes from — a server the user installed on PATH is never touched.
func InstallDir() string { return dataLSPDir() }

// installedIn is InstalledList for an arbitrary base folder.
func installedIn(base string) []ServerState {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []ServerState
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if st, ok := installedAt(base, e.Name()); ok {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// --- instalador ---

// State reads the install state of one server in this installer's folder.
func (in *Installer) State(server string) (ServerState, bool) { return installedAt(in.dir(), server) }

// States lists everything installed in this installer's folder.
func (in *Installer) States() []ServerState { return installedIn(in.dir()) }

// Installer performs the install. Zero value is usable: NewInstaller fills in
// the real downloader and runner.
type Installer struct {
	// Dir is <data_dir>/lsp.
	Dir string
	// Downloader and Runner are the only things that touch the outside world.
	Downloader Downloader
	Runner     Runner
	// Log receives progress lines, if set.
	Log func(format string, args ...any)
}

// NewInstaller is the production installer.
func NewInstaller() *Installer {
	return &Installer{Dir: dataLSPDir(), Downloader: HTTPDownloader{}, Runner: ExecRunner{}}
}

func (in *Installer) logf(format string, args ...any) {
	if in.Log != nil {
		in.Log(format, args...)
	}
}

func (in *Installer) dir() string {
	if in.Dir != "" {
		return in.Dir
	}
	return dataLSPDir()
}

func (in *Installer) downloader() Downloader {
	if in.Downloader != nil {
		return in.Downloader
	}
	return HTTPDownloader{}
}

func (in *Installer) runner() Runner {
	if in.Runner != nil {
		return in.Runner
	}
	return ExecRunner{}
}

// Install fetches one pinned server and swaps it into place.
//
// Nothing is written outside the staging folder until the last rename: a
// network error, a bad checksum or a failed installer command leaves the
// previous installation exactly as it was.
func (in *Installer) Install(ctx context.Context, p Pin) (*ServerState, error) {
	if p.Server == "" || p.Version == "" {
		return nil, errors.New("install: the pin has no server or no fixed version")
	}
	base := in.dir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("install: cannot create %s: %w", base, err)
	}
	staging, err := os.MkdirTemp(base, ".staging-"+p.Server+"-")
	if err != nil {
		return nil, fmt.Errorf("install: cannot create a staging folder in %s: %w", base, err)
	}
	defer os.RemoveAll(staging) // no-op once the rename succeeded

	sum, err := in.fetch(ctx, p, staging, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	if err := in.verify(staging, p); err != nil {
		return nil, err
	}
	st := ServerState{
		Server: p.Server, Lang: p.Lang, Version: p.Version, Kind: string(p.Kind),
		Origin: p.Origin, License: p.License, SHA256: sum,
		Bin: filepath.Join(base, p.Server), At: time.Now().UTC().Format(time.RFC3339),
		By: "fastexec lsp install",
	}
	if err := writeState(staging, st); err != nil {
		return nil, err
	}
	if err := in.swap(staging, filepath.Join(base, p.Server)); err != nil {
		return nil, err
	}
	return &st, nil
}

// fetch obtains the artifact into staging, by the pin's kind, and returns the
// SHA-256 of what was downloaded (empty for ecosystem installs, which are
// verified by their own package manager).
func (in *Installer) fetch(ctx context.Context, p Pin, staging, goos, goarch string) (string, error) {
	switch p.Kind {
	case KindGo:
		in.logf("go install %s@%s", p.Pkg, p.Version)
		err := in.runner().Run(ctx, "go", []string{"install", p.Pkg + "@" + p.Version}, staging,
			[]string{"GOBIN=" + staging, "GOFLAGS=", "CGO_ENABLED=0"})
		if err != nil {
			return "", fmt.Errorf("%s %s: `go install %s@%s`: %w", p.Server, p.Version, p.Pkg, p.Version, err)
		}
		return "", nil
	case KindDotnet:
		in.logf("dotnet tool install %s %s", p.Pkg, p.Version)
		err := in.runner().Run(ctx, "dotnet",
			[]string{"tool", "install", "--tool-path", staging, "--version", p.Version, p.Pkg}, staging, nil)
		if err != nil {
			return "", fmt.Errorf("%s %s: `dotnet tool install %s --version %s`: %w", p.Server, p.Version, p.Pkg, p.Version, err)
		}
		return "", nil
	case KindNpm:
		pkgs := append([]string{p.Pkg + "@" + p.Version}, p.Pkgs...)
		in.logf("npm install --prefix <staging> %s", strings.Join(pkgs, " "))
		err := in.runner().Run(ctx, "npm",
			append([]string{"install", "--no-fund", "--no-audit", "--loglevel=error", "--prefix", staging}, pkgs...), staging, nil)
		if err != nil {
			return "", fmt.Errorf("%s %s: `npm install %s`: %w", p.Server, p.Version, strings.Join(pkgs, " "), err)
		}
		return "", nil
	case KindZip, KindGz:
		a, ok := p.Asset(goos, goarch)
		if !ok {
			return "", fmt.Errorf("%s %s has no pinned file for %s/%s", p.Server, p.Version, goos, goarch)
		}
		file := filepath.Join(staging, "artifact")
		in.logf("downloading %s", a.URL)
		if err := in.downloader().Download(ctx, a.URL, file); err != nil {
			return "", fmt.Errorf("%s %s: %w", p.Server, p.Version, err)
		}
		got, err := fileSHA256(file)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(got, a.SHA256) {
			return "", fmt.Errorf("%s %s: the downloaded file does not match the pinned SHA-256 (expected %s, got %s); nothing was installed",
				p.Server, p.Version, short(a.SHA256), short(got))
		}
		in.logf("SHA-256 matches the pinned value (%s)", short(a.SHA256))
		switch p.Kind {
		case KindZip:
			err = extractZip(file, staging)
		default:
			err = extractGz(file, staging, execName(p.Server))
		}
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", p.Server, p.Version, err)
		}
		_ = os.Remove(file)
		return strings.ToLower(got), nil
	}
	return "", fmt.Errorf("unknown install kind %q", p.Kind)
}

// verify checks that the executable really landed in staging, at the exact
// place discovery looks for it. A download that produced nothing usable must
// fail here, not at the first LSP request.
func (in *Installer) verify(staging string, p Pin) error {
	name := execName(p.Server)
	want := []string{
		filepath.Join(staging, name),
		filepath.Join(staging, "node_modules", ".bin", name),
		filepath.Join(staging, name+".cmd"),
	}
	for _, w := range want {
		if isExecutable(w) {
			return nil
		}
	}
	// An extracted archive may keep its own top-level folder: look for the
	// binary inside, bounded, and move it up.
	found, err := findExecutable(staging, p.Server, 4)
	if err != nil {
		return fmt.Errorf("%s %s: %w (nothing was installed)", p.Server, p.Version, err)
	}
	dest := filepath.Join(staging, name)
	if err := os.Rename(found, dest); err != nil {
		return fmt.Errorf("%s %s: cannot place the executable: %w", p.Server, p.Version, err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dest, 0o755)
	}
	return nil
}

// findExecutable looks for the server's binary inside an extracted tree.
func findExecutable(root, server string, depth int) (string, error) {
	if depth < 0 {
		return "", fmt.Errorf("no %s executable in the downloaded file", execName(server))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			if p, err := findExecutable(filepath.Join(root, e.Name()), server, depth-1); err == nil {
				return p, nil
			}
			continue
		}
		if strings.EqualFold(e.Name(), execName(server)) || strings.EqualFold(e.Name(), server) {
			p := filepath.Join(root, e.Name())
			if isExecutable(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("no %s executable in the downloaded file", execName(server))
}

// swap replaces <base>/<server> with the staging folder, atomically enough that
// no half-installed server is ever visible.
func (in *Installer) swap(staging, final string) error {
	old := final + ".old"
	_ = os.RemoveAll(old)
	had := false
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			return fmt.Errorf("cannot replace %s (a language server may be running: close it and try again): %w", final, err)
		}
		had = true
	}
	if err := os.Rename(staging, final); err != nil {
		if had {
			_ = os.Rename(old, final) // põe a instalação anterior de volta
		}
		return fmt.Errorf("cannot move the new installation into %s: %w", final, err)
	}
	_ = os.RemoveAll(old)
	return nil
}

// Remove deletes one server folder. It refuses anything this installer did not
// create: a folder without installed.json may be a server the user dropped
// there by hand.
func (in *Installer) Remove(server string) error {
	server = strings.TrimSpace(server)
	if server == "" || strings.ContainsAny(server, `/\`) {
		return fmt.Errorf("invalid server name %q", server)
	}
	dir := filepath.Join(in.dir(), server)
	if _, ok := installedAt(in.dir(), server); !ok {
		return fmt.Errorf("%s was not installed by FastExec (no %s); remove it by hand if you want it gone", dir, stateFile)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cannot remove %s (a language server may be running: close it and try again): %w", dir, err)
	}
	return nil
}

func writeState(dir string, st ServerState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stateFile), append(b, '\n'), 0o644)
}

// --- arquivos ---

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "…"
	}
	return sum
}

// extractZip unpacks an archive into dir. Entry names are cleaned and never
// allowed outside dir: a release file is untrusted input.
func extractZip(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("not a valid zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		dest, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode&0o111 == 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(out, io.LimitReader(rc, maxDownload))
		rc.Close()
		if cerr != nil {
			out.Close()
			return cerr
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

// extractGz expands a single gzipped binary (the shape of the rust-analyzer
// assets) into dir/name.
func extractGz(file, dir, name string) error {
	in, err := os.Open(file)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("not a valid gzip file: %w", err)
	}
	defer zr.Close()
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(zr, maxDownload)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// safeJoin joins an archive entry name onto dir, refusing anything that would
// escape it (../, absolute paths, drive letters).
func safeJoin(dir, name string) (string, error) {
	clean := filepath.Clean(strings.ReplaceAll(name, `\`, "/"))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("the archive has an unsafe entry (%q), nothing was installed", name)
	}
	return filepath.Join(dir, clean), nil
}

// --- teste do fluxo interativo ---

// Confirm is asked once per language before anything is downloaded. It exists
// so the CLI and the installer page share the same wording, and so a test can
// answer for the user.
type Confirm func(it Item) bool

// Ask reads a yes/no answer per language. Non-interactive callers pass their
// own Confirm (the CLI uses --yes); this one is for a terminal.
func Ask(in io.Reader, out io.Writer, question func(Item) string) Confirm {
	rd := newLineReader(in)
	return func(it Item) bool {
		fmt.Fprint(out, question(it))
		line, ok := rd()
		if !ok {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "s", "sim":
			return true
		}
		return false
	}
}

// newLineReader reads one line at a time, without a bufio.Scanner limit.
func newLineReader(r io.Reader) func() (string, bool) {
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	return func() (string, bool) {
		for {
			n, err := r.Read(one)
			if n > 0 {
				if one[0] == '\n' {
					line := strings.TrimRight(string(buf), "\r")
					buf = buf[:0]
					return line, true
				}
				buf = append(buf, one[0])
				continue
			}
			if err != nil {
				if len(buf) > 0 {
					s := string(buf)
					buf = buf[:0]
					return s, true
				}
				return "", false
			}
		}
	}
}

// randSuffix is used for staging folder names by callers that build their own.
func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
