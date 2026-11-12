package lsp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// C# is the one language in the table whose server cannot run on its own: it is
// a .NET program that loads a .NET project, so it needs a .NET SDK on the
// machine and a project whose packages were already restored. Both are things
// FastExec reports and never does for the user. Running `dotnet restore` would
// download packages from the internet, and downloading on someone's behalf,
// silently, is not a decision this tool gets to make.

// csharpCustomize completes a csharp-ls launch for one repository root.
//
// It decides four things in order, and the first one that fails ends the
// launch with a phrase the user can act on:
//
//  1. Is there a .NET SDK? Without one csharp-ls starts and then cannot load
//     anything, which is worse than not starting.
//  2. Does a global.json pin an SDK this machine does not have? The SDK
//     resolver refuses before MSBuild ever sees the project, so reporting
//     "restore needed" — or letting the server start and answer nothing —
//     sends the user to fix the wrong thing.
//  3. Was the project restored? A project without obj/project.assets.json
//     answers nothing until `dotnet restore` runs.
//  4. Which solution to load. csharp-ls auto-discovers .csproj under its
//     working directory, but a .sln is what makes references exact across
//     projects, so it is passed explicitly when there is one.
func csharpCustomize(root string) (args, env []string, problem string) {
	sdk, portable := dotnetSDK()
	if sdk == "" {
		return nil, nil, "no .NET SDK found (install one, or set FASTEXEC_DOTNET_SDK to its folder)"
	}
	// The server is a .NET apphost: it finds its runtime through DOTNET_ROOT,
	// and MSBuildLocator refuses to start without it. Setting it for the child
	// only — never for FastExec's own process — is what makes a portable SDK
	// usable without touching the machine.
	env = append(env, "DOTNET_ROOT="+sdk)
	if portable {
		env = append(env, "PATH="+sdk+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	// Checked before the restore, and reported instead of it: a pinned SDK that
	// is not here breaks the launch itself, and the user who reads "restore
	// needed" runs restore, watches it succeed, and gets the same empty answers.
	// The pin belongs to the repository, so FastExec names it and never edits it.
	if problem := pinnedSDKProblem(root, sdk); problem != "" {
		return nil, env, problem
	}

	if project, ok := unrestored(root); ok {
		return nil, env, "restore needed: run dotnet restore (" + project + ")"
	}

	if sln := firstMatch(root, "*.sln"); sln != "" {
		args = append(args, "--solution", sln)
	}
	return args, env, ""
}

// unrestored reports the first project under root that has no restore output.
// A project with no PackageReference still gets an assets file, so its absence
// means "not restored", not "nothing to restore".
//
// The list of projects to look at is the one the server would load, not the one
// next to the file: a solution keeps its projects wherever it likes
// (src/App/App.csproj), and a project the server loads but cannot read is the
// case this check exists to catch — csharp-ls answers empty lists for a project
// whose assets file is missing, and an empty list reads as "no uses".
func unrestored(root string) (string, bool) {
	for _, p := range loadableProjects(root) {
		assets := filepath.Join(filepath.Dir(p), "obj", "project.assets.json")
		if fi, err := os.Stat(assets); err != nil || fi.IsDir() {
			return relTo(root, p), true
		}
	}
	return "", false
}

// maxProjectDepth bounds the search for project files when a solution does not
// name them. Four levels below the root is deeper than any layout a person
// writes by hand (src/<Area>/<App>/App.csproj) and still shallow enough that a
// monorepo is scanned in one directory read rather than a walk of everything.
const maxProjectDepth = 4

// maxGlobalJSONDepth bounds the walk up from the root looking for global.json.
// The SDK resolver walks to the drive root; eight levels is already past any
// repository layout and keeps a query on W:\ from reading the whole tree.
const maxGlobalJSONDepth = 8

// maxPinnedListed caps the installed versions named in the note. A machine with
// a dozen SDKs would otherwise turn one line of degradation into a paragraph.
const maxPinnedListed = 4

// pinnedSDKProblem reports a global.json that pins an SDK this machine cannot
// satisfy, in the words of the pin itself: which version was asked for, which
// ones are here, and the two ways out. It is the difference between a user
// running `dotnet restore` over and over and a user adding one line to a file
// they own.
//
// It only reads. global.json belongs to the repository, and a tool that edited
// it to make itself work would be changing the project's build for a query.
func pinnedSDKProblem(root, sdkRoot string) string {
	pinned, roll := pinnedSDK(root)
	if pinned == "" {
		return ""
	}
	installed := installedSDKs(sdkRoot)
	for _, v := range installed {
		if sdkSatisfies(pinned, v, roll) {
			return ""
		}
	}
	if len(installed) == 0 {
		// Nothing to roll forward to: naming rollForward here would be advice
		// that cannot work.
		return "global.json pins SDK " + pinned + "; installed: none (install " + pinned + ")"
	}
	shown := installed
	if len(shown) > maxPinnedListed {
		shown = shown[:maxPinnedListed]
	}
	list := strings.Join(shown, ", ")
	if len(shown) < len(installed) {
		list += ", ..."
	}
	return "global.json pins SDK " + pinned + "; installed: " + list +
		" (set rollForward or install " + pinned + ")"
}

// pinnedSDK reads the version and rollForward a global.json pins, walking up
// from root the way the SDK resolver does. An unreadable or malformed file
// answers nothing: a guess about which SDK was meant is worse than silence.
func pinnedSDK(root string) (version, roll string) {
	dir := root
	for i := 0; i < maxGlobalJSONDepth; i++ {
		body, err := os.ReadFile(filepath.Join(dir, "global.json"))
		if err == nil {
			var doc struct {
				SDK struct {
					Version     string `json:"version"`
					RollForward string `json:"rollForward"`
				} `json:"sdk"`
			}
			if json.Unmarshal(body, &doc) != nil {
				return "", ""
			}
			if _, ok := sdkVersion(doc.SDK.Version); !ok {
				return "", ""
			}
			return doc.SDK.Version, doc.SDK.RollForward
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

// installedSDKs lists the SDK versions under an SDK root, newest last. Both
// shapes are accepted: an install root (…\dotnet\sdk\8.0.404) and a folder that
// is already the sdk directory, which is how a portable SDK is sometimes
// pointed at through FASTEXEC_DOTNET_SDK.
func installedSDKs(sdkRoot string) []string {
	dir := filepath.Join(sdkRoot, "sdk")
	if filepath.Base(filepath.Clean(sdkRoot)) == "sdk" {
		dir = sdkRoot
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, ok := sdkVersion(e.Name()); ok {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// sdkSatisfies reports whether an installed version answers a pinned one under
// the global.json rollForward policy. The policies are approximated by the part
// of the version they hold fixed — major.minor.featureband.patch — which is
// what decides whether the resolver accepts a version at all.
func sdkSatisfies(pinned, installed, roll string) bool {
	p, ok := sdkVersion(pinned)
	if !ok {
		return false
	}
	v, ok := sdkVersion(installed)
	if !ok {
		return false
	}
	for i := range p {
		if v[i] < p[i] {
			return false
		}
		if v[i] > p[i] {
			break
		}
	}
	switch strings.ToLower(strings.TrimSpace(roll)) {
	case "disable":
		// The pin is exact: the one case where a newer SDK is not an answer.
		return v == p
	case "feature", "latestfeature":
		return v[0] == p[0] && v[1] == p[1]
	case "minor", "latestminor":
		return v[0] == p[0]
	case "major", "latestmajor":
		return true
	default:
		// "patch" (the default) and "latestPatch": the same feature band. A
		// value nobody recognises is read as the default rather than as "no
		// roll", because claiming a pin cannot be met is the worse mistake.
		return v[0] == p[0] && v[1] == p[1] && v[2] == p[2]
	}
}

// sdkVersion parses a dotted numeric SDK version into
// major.minor.featureband.patch. A missing or non-numeric component is zero,
// and a name that is not a version at all answers false.
func sdkVersion(s string) ([4]int, bool) {
	var out [4]int
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if s == "" {
		return out, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > len(out) {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	// "8.0.404" is major 8, minor 0, feature band 4, patch 4. Holding the band
	// apart from the patch is what makes the default policy mean something.
	out[3] += out[2] % 100
	out[2] /= 100
	return out, true
}

// projectNoise are directories that never hold a project the server loads:
// build output (bin, obj), package caches, and version control. Pruning them is
// what makes the walk cheap, and it also keeps a generated copy of a project
// under obj/ from being reported as a second, unrestored one.
var projectNoise = map[string]bool{
	"bin": true, "obj": true, "node_modules": true, "packages": true,
	".git": true, ".vs": true, ".idea": true, "TestResults": true,
}

// loadableProjects lists the project files a server started at root would load:
// the ones a solution names, or — when there is no solution, or it names none —
// the ones lying under root.
func loadableProjects(root string) []string {
	if sln := firstMatch(root, "*.sln"); sln != "" {
		if projects := solutionProjects(sln); len(projects) > 0 {
			return projects
		}
	}
	return walkProjects(root)
}

// solutionProjects reads the project list out of a .sln. The format is a
// Microsoft format, not XML, and the only lines that matter are the Project
// entries; the quoted path inside is relative to the solution and uses
// backslashes even on Linux, because the file was written by Visual Studio.
//
// Entries that do not end in a project extension are solution folders, and one
// that names a file which is not there is a project the user has not checked
// out: neither is something the server can fail to read, so both are skipped.
func solutionProjects(sln string) []string {
	data, err := os.ReadFile(sln)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(sln)
	var out []string
	for _, m := range slnProject.FindAllStringSubmatch(string(data), -1) {
		p := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(m[1], `\`, "/")))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// slnProject matches the path field of a "Project(...) = name, path, guid" line.
var slnProject = regexp.MustCompile(`(?m)^Project\("[^"]*"\)\s*=\s*"[^"]*",\s*"([^"]+\.(?:cs|fs|vb)proj)"`)

// walkProjects lists project files under root, up to maxProjectDepth, skipping
// the directories no project lives in. The result is sorted so the project
// reported to the user does not depend on the order the filesystem returns.
func walkProjects(root string) []string {
	root = filepath.Clean(root)
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read is not a reason to stop: the projects
			// found so far are still worth checking.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && projectNoise[d.Name()] {
				return fs.SkipDir
			}
			if depth(root, path) > maxProjectDepth {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".csproj") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// depth is how many levels path sits below root.
func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// dotnetSDK finds a .NET SDK and reports whether it is a portable one that has
// to be pointed at through DOTNET_ROOT and PATH.
//
// The result is computed once: a machine's SDK layout does not change while
// FastExec runs, and probing it starts a `dotnet` process. It is a variable so a
// test can describe a machine without an SDK — the one case that has to degrade,
// and the one a machine that has an SDK cannot be made to be.
var dotnetSDK = sync.OnceValues(findDotnetSDK)

func findDotnetSDK() (string, bool) {
	// 1. An explicitly named SDK root wins: it is the user pointing at one.
	if d := expandPath(os.Getenv("FASTEXEC_DOTNET_SDK")); d != "" && hasSDK(d) {
		return d, true
	}
	// 2. An SDK root already exported for this session.
	if d := expandPath(os.Getenv("DOTNET_ROOT")); d != "" && hasSDK(d) {
		return d, false
	}
	// 3. `dotnet` on PATH, but only when it really carries an SDK. The
	//    Windows-wide install of the runtime alone prints nothing for
	//    --list-sdks and still exits 0, so the output is the discriminator.
	if exe, err := exec.LookPath("dotnet"); err == nil && dotnetHasSDK(exe) {
		return filepath.Dir(exe), false
	}
	// 4. A portable SDK unpacked in a directory of its own.
	for _, d := range portableSDKDirs() {
		if hasSDK(d) {
			return d, true
		}
	}
	return "", false
}

// portableSDKDirs are where a self-contained SDK is expected to sit: the
// FastExec data directory, and this lab's shared location. Both are guesses
// that have to exist to count.
func portableSDKDirs() []string {
	dirs := []string{
		filepath.Join(dataLSPDir(), "dotnet-sdk"),
		filepath.Join(dataLSPDir(), "dotnet"),
	}
	if runtime.GOOS == "windows" {
		dirs = append(dirs, `W:\Data\dotnet-sdk\sdk`)
	}
	return dirs
}

// hasSDK reports whether dir looks like a .NET install root that carries an
// SDK: the host executable plus at least one version directory under sdk/.
// This is a filesystem check on purpose — it costs no process.
func hasSDK(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(dotnetExe(dir)); err != nil {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(dir, "sdk"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// dotnetExe is the host executable inside a .NET install root.
func dotnetExe(dir string) string {
	name := "dotnet"
	if runtime.GOOS == "windows" {
		name = "dotnet.exe"
	}
	return filepath.Join(dir, name)
}

// dotnetHasSDK asks a dotnet executable what SDKs it has. An empty answer means
// the install is a runtime-only one, which cannot build or load a project.
func dotnetHasSDK(exe string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--list-sdks")
	cmd.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// glob lists matches of pattern inside dir, sorted, as absolute paths.
func glob(dir, pattern string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			out = append(out, m)
		}
	}
	return out
}

// firstMatch is the first file in dir matching pattern, or "".
func firstMatch(dir, pattern string) string {
	if m := glob(dir, pattern); len(m) > 0 {
		return m[0]
	}
	return ""
}
