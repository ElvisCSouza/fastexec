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
