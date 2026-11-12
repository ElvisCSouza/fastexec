package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/config"
)

// stubSDK answers the SDK probe for one test. The stub is what makes "there is
// no .NET SDK on this machine" testable from a machine that has one.
func stubSDK(t *testing.T, root string, portable bool) {
	t.Helper()
	prev := dotnetSDK
	dotnetSDK = func() (string, bool) { return root, portable }
	t.Cleanup(func() { dotnetSDK = prev })
}

// csproj writes a project file at dir/name. An empty body is enough: the
// customizer reads the filesystem, never the project's contents.
func csproj(t *testing.T, dir, name string) string {
	t.Helper()
	return writeFixture(t, dir, name, "<Project Sdk=\"Microsoft.NET.Sdk\"></Project>\n")
}

// restored fakes the output `dotnet restore` leaves behind. Its presence is the
// whole test of "was this project restored": FastExec must never run restore
// itself, so it can only look.
func restored(t *testing.T, projectDir string) {
	t.Helper()
	writeFixture(t, filepath.Join(projectDir, "obj"), "project.assets.json", "{}\n")
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

func TestCSharpCustomizeWithoutAnSDKReportsIt(t *testing.T) {
	stubSDK(t, "", false)
	root := t.TempDir()
	csproj(t, root, "demo.csproj")

	args, env, problem := csharpCustomize(root)
	if problem == "" {
		t.Fatal("a machine without an SDK must not launch a server that cannot load anything")
	}
	if !strings.Contains(problem, "no .NET SDK found") || !strings.Contains(problem, "FASTEXEC_DOTNET_SDK") {
		t.Errorf("problem = %q, want the SDK wording and the way to fix it", problem)
	}
	if len(args) != 0 || len(env) != 0 {
		t.Errorf("args = %v, env = %v; nothing may be prepared for a launch that is refused", args, env)
	}
}

func TestCSharpCustomizeRefusesToLaunchUnrestoredProjects(t *testing.T) {
	stubSDK(t, `W:\sdk`, true)
	root := t.TempDir()
	project := csproj(t, root, "demo.csproj")

	_, env, problem := csharpCustomize(root)
	if !strings.HasPrefix(problem, "restore needed: run dotnet restore (") || !strings.Contains(problem, "demo.csproj") {
		t.Fatalf("problem = %q, want 'restore needed' naming the project", problem)
	}
	// The environment still carries DOTNET_ROOT: the phrase is for the user, and
	// it must be usable without a second, differently-configured run.
	if sdk, _ := envValue(env, "DOTNET_ROOT"); sdk != `W:\sdk` {
		t.Errorf("DOTNET_ROOT = %q, want the SDK root", sdk)
	}
	_ = project

	// After the user restores, the same call must proceed: nothing here is
	// remembered, because the user can fix it at any moment.
	restored(t, root)
	if _, _, problem := csharpCustomize(root); problem != "" {
		t.Errorf("problem = %q after the restore output appeared, want none", problem)
	}
}

func TestCSharpCustomizePassesTheSolutionAndTheSDKEnv(t *testing.T) {
	stubSDK(t, `W:\sdk`, true)
	root := t.TempDir()
	writeFixture(t, root, "demo.sln", "Microsoft Visual Studio Solution File\n")
	restored(t, root)
	csproj(t, root, "demo.csproj")

	args, env, problem := csharpCustomize(root)
	if problem != "" {
		t.Fatalf("problem = %q, want none", problem)
	}
	want := filepath.Join(root, "demo.sln")
	if len(args) != 2 || args[0] != "--solution" || args[1] != want {
		t.Fatalf("args = %v, want --solution %s", args, want)
	}
	// A portable SDK is invisible to the server unless the child is told: the
	// machine's own DOTNET_ROOT points at a runtime with no SDK.
	if sdk, _ := envValue(env, "DOTNET_ROOT"); sdk != `W:\sdk` {
		t.Errorf("DOTNET_ROOT = %q, want the SDK root", sdk)
	}
	path, ok := envValue(env, "PATH")
	if !ok || !strings.HasPrefix(path, `W:\sdk`+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want it to start with the portable SDK", path)
	}
}

func TestCSharpCustomizeLeavesThePathAloneForAnInstalledSDK(t *testing.T) {
	stubSDK(t, `C:\Program Files\dotnet`, false)
	root := t.TempDir()
	csproj(t, root, "demo.csproj")
	restored(t, root)

	args, env, problem := csharpCustomize(root)
	if problem != "" {
		t.Fatalf("problem = %q, want none", problem)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want no --solution for a project with no solution", args)
	}
	if _, ok := envValue(env, "PATH"); ok {
		t.Error("PATH was rewritten for an SDK that is already on it")
	}
}

func TestCSharpCustomizeFindsProjectsOneLevelDown(t *testing.T) {
	stubSDK(t, "/sdk", false)
	root := t.TempDir()
	// The layout a solution usually has: projects in their own directories.
	nested := filepath.Join(root, "src", "App")
	csproj(t, nested, "App.csproj")

	if _, _, problem := csharpCustomize(root); !strings.Contains(problem, "App.csproj") {
		t.Errorf("problem = %q, want the nested project named", problem)
	}
	restored(t, nested)
	if _, _, problem := csharpCustomize(root); problem != "" {
		t.Errorf("problem = %q after the nested project was restored, want none", problem)
	}
}

// TestRootForPrefersTheSolution: the root decides which server instance loads
// what. C# loads a .sln best — references are only exact across projects when
// the whole solution is loaded — so a solution at the top wins over a .csproj
// sitting right next to the file.
func TestRootForPrefersTheSolution(t *testing.T) {
	lang := csharpLanguage(t)
	root := t.TempDir()
	writeFixture(t, root, "demo.sln", "Microsoft Visual Studio Solution File\n")
	nested := filepath.Join(root, "src", "App")
	file := writeFixture(t, nested, "App.cs", "class App {}\n")
	csproj(t, nested, "App.csproj")

	if got := RootFor(lang, file); got != root {
		t.Errorf("RootFor = %q, want the solution root %q", got, root)
	}

	// Without a solution anywhere above, the project next to the file is the
	// root, and it is found from any depth of the source tree.
	plain := t.TempDir()
	deep := writeFixture(t, filepath.Join(plain, "src", "a", "b"), "Deep.cs", "class Deep {}\n")
	csproj(t, plain, "Plain.csproj")
	if got := RootFor(lang, deep); got != plain {
		t.Errorf("RootFor = %q, want %q", got, plain)
	}
}

// TestManagerDegradesOnLaunchProblemWithoutRemembering it: a launch problem is
// something the user fixes (install the SDK, run restore). Remembering it as a
// failure would keep answering the same phrase for the whole cooldown after they
// fixed it.
func TestManagerDegradesOnLaunchProblemWithoutRemembering(t *testing.T) {
	root := csharpProject(t)
	lang := csharpLanguage(t)
	m := newTestManager(t, time.Minute)
	defer m.Shutdown()
	ctx := context.Background()

	calls := 0
	prev := launchFor
	launchFor = func(l Language, r string) (Launch, bool) {
		calls++
		if calls == 1 {
			return Launch{Name: l.Primary().Name, Problem: "restore needed: run dotnet restore (demo.csproj)"}, true
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

	_, _, err := m.Acquire(ctx, root, lang)
	var ue *UnavailableError
	if !errors.As(err, &ue) || !strings.Contains(ue.Reason, "restore needed") {
		t.Fatalf("Acquire with a launch problem = %v, want the problem as an UnavailableError", err)
	}
	if got := Degraded(err); !strings.Contains(got, "restore needed") || !strings.HasSuffix(got, "; by name") {
		t.Errorf("Degraded(%v) = %q, want the reason and the fallback", err, got)
	}

	// The very next query tries again and gets a server: the failure was not
	// cached.
	c, release, err := m.Acquire(ctx, root, lang)
	if err != nil {
		t.Fatalf("Acquire after the problem was fixed: %v", err)
	}
	defer release()
	if c == nil || calls != 2 {
		t.Fatalf("Acquire retried %d time(s); a launch problem must not be remembered", calls)
	}
	if len(m.Servers()) != 1 {
		t.Errorf("Servers() = %d, want 1", len(m.Servers()))
	}
}

// TestCSharpLanguageEntry locks the contract the rest of this package reads off
// the table: the gate is the "loading" progress title, the note names the
// solution load, and the root prefers a solution over a project file.
func TestCSharpLanguageEntry(t *testing.T) {
	lang := csharpLanguage(t)
	if len(lang.ProjectFiles) < 2 || lang.ProjectFiles[0] != "*.sln" {
		t.Errorf("ProjectFiles = %v, want *.sln before *.csproj", lang.ProjectFiles)
	}
	srv := lang.Primary()
	if len(srv.BlockingProgress) == 0 || srv.BlockingProgress[0] != "loading" {
		t.Errorf("BlockingProgress = %v, want the 'loading' prefix", srv.BlockingProgress)
	}
	if srv.LoadingNote != "loading C# solution" {
		t.Errorf("LoadingNote = %q", srv.LoadingNote)
	}
	if srv.Customize == nil {
		t.Error("the C# entry has no customizer; nothing would provide DOTNET_ROOT or refuse an unrestored project")
	}
	for _, want := range []string{"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1"} {
		if _, ok := envValue(srv.Env, strings.SplitN(want, "=", 2)[0]); !ok {
			t.Errorf("Env is missing %s: %v", want, srv.Env)
		}
	}
	if got, ok := LanguageByExt(".cs"); !ok || got.ID != "csharp" {
		t.Errorf("Exts = %v, want .cs to resolve back to C#", lang.Exts)
	}
}

// TestSolutionProjectsReadsTheMicrosoftFormat: a .sln names its projects in a
// format that is neither XML nor a list — the file is read by hand. Only the
// entries that point at a project which is really there count: a solution folder
// has no project extension, and a project not checked out is nothing the server
// could fail to load.
func TestSolutionProjectsReadsTheMicrosoftFormat(t *testing.T) {
	root := t.TempDir()
	app := csproj(t, filepath.Join(root, "src", "App"), "App.csproj")
	tests := csproj(t, filepath.Join(root, "tests"), "App.Tests.csproj")
	writeFixture(t, root, "demo.sln", `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{9A19103F-16F7-4668-BE54-9A1E7A4F7556}") = "App", "src\App\App.csproj", "{11111111-1111-1111-1111-111111111111}"
Project("{9A19103F-16F7-4668-BE54-9A1E7A4F7556}") = "App.Tests", "tests\App.Tests.csproj", "{22222222-2222-2222-2222-222222222222}"
Project("{2150E333-8FDC-42A3-9474-1A3956D46DE8}") = "Solution Items", "Solution Items", "{33333333-3333-3333-3333-333333333333}"
Project("{9A19103F-16F7-4668-BE54-9A1E7A4F7556}") = "Gone", "src\Gone\Gone.csproj", "{44444444-4444-4444-4444-444444444444}"
EndProject
`)

	got := solutionProjects(filepath.Join(root, "demo.sln"))
	want := []string{app, tests}
	if len(got) != len(want) {
		t.Fatalf("solutionProjects = %v, want the two projects on disk %v", got, want)
	}
	for i := range want {
		if !sameFile(got[i], want[i]) {
			t.Errorf("solutionProjects[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestWalkProjectsStopsAtTheNoise: the walk exists so a repository with no
// solution still gets its projects checked. It has to skip build output — a
// project copied under obj/ is not a second project — and stop at a sane depth,
// or a monorepo turns every query into a walk of the whole tree.
func TestWalkProjectsStopsAtTheNoise(t *testing.T) {
	root := t.TempDir()
	top := csproj(t, root, "Top.csproj")
	deepest := csproj(t, filepath.Join(root, "a", "b", "c", "d"), "Deepest.csproj")
	// One level past the limit, and two copies of a project that is build
	// output rather than source.
	csproj(t, filepath.Join(root, "a", "b", "c", "d", "e"), "TooDeep.csproj")
	csproj(t, filepath.Join(root, "bin"), "Copied.csproj")
	csproj(t, filepath.Join(root, "src", "obj"), "Generated.csproj")

	got := walkProjects(root)
	// Sorted by path: the project named in an error message must not depend on
	// the order the filesystem happens to return.
	want := []string{top, deepest}
	if len(got) != len(want) {
		t.Fatalf("walkProjects = %v, want exactly %v", got, want)
	}
	for i := range want {
		if !sameFile(got[i], want[i]) {
			t.Errorf("walkProjects[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestLoadableProjectsPrefersTheSolution: the projects the server loads are the
// ones the solution names, not the ones lying around. A stray .csproj next to
// the solution must not be reported as a project needing restore.
func TestLoadableProjectsPrefersTheSolution(t *testing.T) {
	root := t.TempDir()
	named := csproj(t, filepath.Join(root, "src", "App"), "App.csproj")
	csproj(t, filepath.Join(root, "stray"), "Stray.csproj")
	writeFixture(t, root, "demo.sln",
		`Project("{9A19103F-16F7-4668-BE54-9A1E7A4F7556}") = "App", "src\App\App.csproj", "{11111111-1111-1111-1111-111111111111}"`+"\n")

	got := loadableProjects(root)
	if len(got) != 1 || !sameFile(got[0], named) {
		t.Fatalf("loadableProjects = %v, want just the project the solution names (%q)", got, named)
	}

	// A solution that names nothing is no better than none: fall back to the
	// walk rather than reporting zero projects, or a project needing restore
	// would go unnoticed and answer empty lists.
	empty := t.TempDir()
	want := csproj(t, empty, "Only.csproj")
	writeFixture(t, empty, "empty.sln", "Microsoft Visual Studio Solution File\n")
	got = loadableProjects(empty)
	if len(got) != 1 || !sameFile(got[0], want) {
		t.Fatalf("loadableProjects with a solution naming nothing = %v, want the project under the root (%q)", got, want)
	}
}

// sdkTree builds an install root shaped like a real one. <root>/sdk/<version>
// directories are exactly what the resolver reads before it decides whether a
// global.json pin can be met, so a fake here tests the real decision.
func sdkTree(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		if err := os.MkdirAll(filepath.Join(root, "sdk", v), 0o755); err != nil {
			t.Fatalf("sdkTree: %v", err)
		}
	}
	return root
}

// globalJSON writes the pin the repository owns. FastExec only ever reads it.
func globalJSON(t *testing.T, dir, version, roll string) {
	t.Helper()
	body := `{"sdk":{"version":"` + version + `"`
	if roll != "" {
		body += `,"rollForward":"` + roll + `"`
	}
	writeFixture(t, dir, "global.json", body+"}}\n")
}

// TestCSharpCustomizeNamesThePinnedSDKItCannotSatisfy: when global.json pins an
// SDK this machine does not have, the launch is the thing that is broken — and
// the note must say so, because "restore needed" sends the user to run a
// command that will succeed and change nothing.
func TestCSharpCustomizeNamesThePinnedSDKItCannotSatisfy(t *testing.T) {
	tests := []struct {
		name string
		pin  string
		roll string
		sdks []string
		want string // "" means the pin is satisfiable and the launch goes ahead
	}{
		{
			// The reported case: 7.0.102 pinned, only 8.x installed, and the
			// default policy holds the feature band.
			name: "a pin the default policy cannot reach",
			pin:  "7.0.102",
			sdks: []string{"8.0.100", "8.0.404"},
			want: "global.json pins SDK 7.0.102; installed: 8.0.100, 8.0.404 (set rollForward or install 7.0.102)",
		},
		{
			// Roll forward far enough and the pin stops being a problem: the
			// answer must not nag about a file that is already correct.
			name: "latestFeature reaches a newer band in the same minor",
			pin:  "7.0.102",
			roll: "latestFeature",
			sdks: []string{"7.0.404"},
		},
		{
			// latestFeature holds major.minor, so a newer major is still out of
			// reach — this is the case a hand-written note gets wrong.
			name: "latestFeature does not cross a minor",
			pin:  "7.0.102",
			roll: "latestFeature",
			sdks: []string{"8.0.404"},
			want: "global.json pins SDK 7.0.102; installed: 8.0.404 (set rollForward or install 7.0.102)",
		},
		{
			name: "disable makes the pin exact",
			pin:  "8.0.100",
			roll: "disable",
			sdks: []string{"8.0.404"},
			want: "global.json pins SDK 8.0.100; installed: 8.0.404 (set rollForward or install 8.0.100)",
		},
		{
			name: "an installed exact match satisfies disable",
			pin:  "8.0.404",
			roll: "disable",
			sdks: []string{"8.0.404", "9.0.100"},
		},
		{
			name: "latestMinor reaches a newer minor",
			pin:  "8.0.100",
			roll: "latestMinor",
			sdks: []string{"8.0.404"},
		},
		{
			name: "latestMajor crosses as far as it has to",
			pin:  "7.0.102",
			roll: "latestMajor",
			sdks: []string{"8.0.404"},
		},
		{
			// A policy nobody recognises is read as the default, not as "no
			// roll": claiming a pin cannot be met is the worse mistake.
			name: "an unknown policy is read as the default",
			pin:  "8.0.100",
			roll: "sideways",
			sdks: []string{"8.0.404"},
			want: "global.json pins SDK 8.0.100; installed: 8.0.404 (set rollForward or install 8.0.100)",
		},
		{
			// With nothing here, naming rollForward would be advice that cannot
			// work; installing the pinned version is the only way out.
			name: "nothing installed at all",
			pin:  "7.0.102",
			want: "global.json pins SDK 7.0.102; installed: none (install 7.0.102)",
		},
		{
			// The default policy accepts a newer patch inside the band.
			name: "same band, newer patch",
			pin:  "8.0.404",
			sdks: []string{"8.0.410"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			globalJSON(t, root, tc.pin, tc.roll)
			csproj(t, root, "demo.csproj")
			sdk := sdkTree(t, tc.sdks...)
			stubSDK(t, sdk, false)
			if tc.want == "" {
				// Restored, so a satisfiable pin has to leave the launch with
				// nothing at all to complain about.
				restored(t, root)
			}

			args, env, problem := csharpCustomize(root)
			if tc.want == "" {
				if problem != "" {
					t.Fatalf("problem = %q, want the pin treated as satisfiable", problem)
				}
				// No .sln in the fixture, so a launch that goes ahead carries no
				// arguments — but it does have to happen.
				if len(args) != 0 {
					t.Errorf("args = %v, want the launch to go ahead", args)
				}
				if got, ok := envValue(env, "DOTNET_ROOT"); !ok || got != sdk {
					t.Errorf("DOTNET_ROOT = %q (present %v), want %q", got, ok, sdk)
				}
				return
			}
			if problem != tc.want {
				t.Fatalf("problem = %q, want %q", problem, tc.want)
			}
			if strings.Contains(problem, "restore needed") {
				t.Error("a pinned SDK was reported as a restore problem")
			}
			if len(args) != 0 {
				t.Errorf("args = %v; nothing may be prepared for a launch that is refused", args)
			}
			// DOTNET_ROOT stays: the note is about which SDK was asked for, and
			// the env is the other half of what the caller may want to see.
			if got, ok := envValue(env, "DOTNET_ROOT"); !ok || got != sdk {
				t.Errorf("DOTNET_ROOT = %q (present %v), want %q", got, ok, sdk)
			}
		})
	}
}

// TestPinnedSDKWalkStopsAtTheNearestFile: the resolver walks up, so the pin
// closest to the project is the one that counts — a global.json at the drive
// root must not override the repository's own.
func TestPinnedSDKWalkStopsAtTheNearestFile(t *testing.T) {
	outer := t.TempDir()
	globalJSON(t, outer, "9.0.100", "")
	inner := filepath.Join(outer, "src", "App")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	globalJSON(t, inner, "7.0.102", "latestMajor")

	version, roll := pinnedSDK(inner)
	if version != "7.0.102" || roll != "latestMajor" {
		t.Errorf("pinnedSDK = (%q, %q), want the nearest global.json", version, roll)
	}

	// Malformed or absent: silence, because a guess about which SDK was meant is
	// worse than saying nothing.
	broken := t.TempDir()
	writeFixture(t, broken, "global.json", "{ this is not json\n")
	if v, r := pinnedSDK(broken); v != "" || r != "" {
		t.Errorf("pinnedSDK on a malformed file = (%q, %q), want silence", v, r)
	}
	if v, _ := pinnedSDK(t.TempDir()); v != "" {
		t.Errorf("pinnedSDK without a file = %q, want silence", v)
	}
}

// TestInstalledSDKsReadsBothRootShapes: FASTEXEC_DOTNET_SDK may point at an
// install root (...\dotnet) or straight at its sdk folder, and the note must
// list the same versions either way.
func TestInstalledSDKsReadsBothRootShapes(t *testing.T) {
	root := sdkTree(t, "8.0.404", "9.0.100", "not-a-version")
	if got := installedSDKs(root); len(got) != 2 || got[0] != "8.0.404" || got[1] != "9.0.100" {
		t.Errorf("installedSDKs(install root) = %v, want the two versions and no junk", got)
	}
	if got := installedSDKs(filepath.Join(root, "sdk")); len(got) != 2 {
		t.Errorf("installedSDKs(sdk dir) = %v, want the same two versions", got)
	}
	if got := installedSDKs(filepath.Join(root, "missing")); got != nil {
		t.Errorf("installedSDKs on a missing dir = %v, want none", got)
	}
}

// TestInstalledSDKListIsCapped: one line of degradation must stay one line even
// on a machine with a dozen SDKs.
func TestInstalledSDKListIsCapped(t *testing.T) {
	root := t.TempDir()
	globalJSON(t, root, "7.0.102", "")
	csproj(t, root, "demo.csproj")
	sdk := sdkTree(t, "8.0.100", "8.0.200", "8.0.300", "8.0.400", "8.0.500", "8.0.600")
	stubSDK(t, sdk, false)

	_, _, problem := csharpCustomize(root)
	if !strings.Contains(problem, "...") {
		t.Errorf("problem = %q, want the list visibly truncated", problem)
	}
	for _, v := range []string{"8.0.500", "8.0.600"} {
		if strings.Contains(problem, v) {
			t.Errorf("problem = %q, want at most %d versions named", problem, maxPinnedListed)
		}
	}
	if got := installedSDKs(sdk); len(got) != 6 {
		t.Fatalf("fixture built %d SDKs, want 6", len(got))
	}
}

// --- the doctor's C# checks: the launch's own list, without starting anything ---

// TestCSharpReadinessIsSilentForAProjectThatWouldLoad: the doctor must be able to
// say "ready" — a check that always finds something is a check nobody reads.
func TestCSharpReadinessIsSilentForAProjectThatWouldLoad(t *testing.T) {
	sdk := sdkTree(t, "8.0.428")
	stubSDK(t, sdk, false)
	root := t.TempDir()
	csproj(t, root, "demo.csproj")
	restored(t, root)

	if problems := CSharpReadiness(root); len(problems) != 0 {
		t.Errorf("CSharpReadiness = %q, want none for a restored project with no pin", problems)
	}
}

// TestCSharpReadinessSaysWhatTheLaunchWouldRefuse: every line the doctor prints
// for C# is the line the launch would have refused with, in the same order. This
// is what keeps the two from drifting: a user who reads "restore needed" in the
// doctor sees exactly what the model would have been told.
func TestCSharpReadinessSaysWhatTheLaunchWouldRefuse(t *testing.T) {
	tests := []struct {
		name     string
		sdk      string
		pin      string
		restored bool
		want     []string
	}{
		{
			name:     "the pin the machine cannot satisfy",
			sdk:      "8.0.428",
			pin:      "7.0.102",
			restored: true,
			want:     []string{"global.json pins SDK 7.0.102; installed: 8.0.428 (set rollForward or install 7.0.102)"},
		},
		{
			name: "the restore that never ran",
			sdk:  "8.0.428",
			want: []string{"restore needed: run dotnet restore (demo.csproj)"},
		},
		{
			// Both problems, in launch order: the SDK first, then the pin that
			// cannot be met without one. Fixing the pin alone would not help.
			name: "no SDK at all plus a pin",
			pin:  "7.0.102",
			want: []string{
				"no .NET SDK found",
				"global.json pins SDK 7.0.102; installed: none (install 7.0.102)",
				"restore needed: run dotnet restore (demo.csproj)",
			},
		},
		{
			name:     "a pin this machine can meet stays quiet",
			sdk:      "8.0.428",
			pin:      "8.0.400",
			restored: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sdkRoot := ""
			if tc.sdk != "" {
				sdkRoot = sdkTree(t, tc.sdk)
			}
			stubSDK(t, sdkRoot, false)
			csproj(t, root, "demo.csproj")
			if tc.pin != "" {
				globalJSON(t, root, tc.pin, "")
			}
			if tc.restored {
				restored(t, root)
			}

			problems := CSharpReadiness(root)
			if len(problems) != len(tc.want) {
				t.Fatalf("CSharpReadiness = %q, want %d line(s): %q", problems, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(problems[i], want) {
					t.Errorf("line %d = %q, want it to contain %q", i, problems[i], want)
				}
			}
			// The doctor and the launch are one list: the first problem the
			// doctor prints is the one the launch refuses with.
			if len(tc.want) > 0 {
				if _, _, launch := csharpCustomize(root); launch != problems[0] {
					t.Errorf("launch problem = %q, doctor line = %q; they must be the same sentence", launch, problems[0])
				}
			}
		})
	}
}

// TestNoSDKProblemNamesTheConfiguredFolderThatIsNotAnSDK: the most confusing
// state is a config that points at a folder which is not an SDK — the user
// believes one is configured, and the tool must say which folder it looked at.
func TestNoSDKProblemNamesTheConfiguredFolderThatIsNotAnSDK(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "not-an-sdk")
	restore := config.Set(&config.Config{DotnetSDK: bogus})
	t.Cleanup(restore)
	root := t.TempDir()
	csproj(t, root, "demo.csproj")

	problems := csharpProblems(root, "")
	if len(problems) == 0 || !strings.Contains(problems[0], "no .NET SDK found") {
		t.Fatalf("problems = %q, want the missing SDK first", problems)
	}
	for _, want := range []string{bogus, "fastexec config set dotnet_sdk"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("line = %q, want it to name %q", problems[0], want)
		}
	}
}

// sdkInstall is sdkTree plus the host executable: what hasSDK looks for when the
// lookup decides on its own, rather than being told by a stub. The file is never
// run, so an empty one is enough.
func sdkInstall(t *testing.T, versions ...string) string {
	t.Helper()
	root := sdkTree(t, versions...)
	if err := os.WriteFile(dotnetExe(root), nil, 0o755); err != nil {
		t.Fatalf("sdkInstall: %v", err)
	}
	return root
}

// TestFindDotnetSDKOrder: env > config > PATH/known folders. Each step is a
// different promise — "for this command", "for every command", "whatever the
// machine has" — and a portable SDK is invisible to the machine, so the config
// one has to win over the folders FastExec guesses at.
func TestFindDotnetSDKOrder(t *testing.T) {
	t.Setenv("FASTEXEC_DOTNET_SDK", "")
	t.Setenv("DOTNET_ROOT", "")
	envSDK := sdkInstall(t, "8.0.100")
	configSDK := sdkInstall(t, "8.0.404")
	rootSDK := sdkInstall(t, "9.0.100")

	t.Run("the environment beats the config", func(t *testing.T) {
		t.Setenv("FASTEXEC_DOTNET_SDK", envSDK)
		restore := config.Set(&config.Config{DotnetSDK: configSDK})
		t.Cleanup(restore)
		got, portable := findDotnetSDK()
		if got != envSDK || !portable {
			t.Errorf("findDotnetSDK = (%q, %v), want the environment's %q as portable", got, portable, envSDK)
		}
	})

	t.Run("DOTNET_ROOT beats the config", func(t *testing.T) {
		t.Setenv("DOTNET_ROOT", rootSDK)
		restore := config.Set(&config.Config{DotnetSDK: configSDK})
		t.Cleanup(restore)
		got, portable := findDotnetSDK()
		if got != rootSDK || portable {
			t.Errorf("findDotnetSDK = (%q, %v), want DOTNET_ROOT %q", got, portable, rootSDK)
		}
	})

	t.Run("the config beats PATH and the known folders", func(t *testing.T) {
		restore := config.Set(&config.Config{DotnetSDK: configSDK})
		t.Cleanup(restore)
		got, portable := findDotnetSDK()
		if got != configSDK || !portable {
			t.Errorf("findDotnetSDK = (%q, %v), want the config's %q as portable", got, portable, configSDK)
		}
	})

	t.Run("a configured folder that is not an SDK is skipped", func(t *testing.T) {
		bogus := filepath.Join(t.TempDir(), "not-an-sdk")
		restore := config.Set(&config.Config{DotnetSDK: bogus})
		t.Cleanup(restore)
		if got, _ := findDotnetSDK(); got == bogus {
			t.Error("a folder with no SDK was accepted just because it was configured")
		}
	})
}

// TestLoadAction: the note the model sees for a C# project that did not load
// carries the action, and no other language pays for a generic sentence.
func TestLoadAction(t *testing.T) {
	stubSDK(t, sdkTree(t, "8.0.428"), false)
	root := t.TempDir()
	csproj(t, root, "demo.csproj")

	if act := LoadAction("csharp", root); !strings.HasPrefix(act, "restore needed: run dotnet restore (") {
		t.Errorf("LoadAction = %q, want the restore command", act)
	}
	restored(t, root)
	if act := LoadAction("csharp", root); !strings.Contains(act, "fastexec lsp doctor") {
		t.Errorf("LoadAction = %q, want the doctor named when nothing is wrong on disk", act)
	}
	if act := LoadAction("go", root); act != "" {
		t.Errorf("LoadAction(go) = %q, want nothing", act)
	}
	if act := LoadAction("csharp", ""); act != "" {
		t.Errorf("LoadAction with no root = %q, want nothing", act)
	}
}
