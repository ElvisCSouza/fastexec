package lsp

// typescript_test.go: the TypeScript/JavaScript counterpart of gopls_test.go —
// the second real server this layer is exercised against, and the only one that
// brings a whole second install with it (the server is a front end for a
// tsserver, and without one it starts and exits).
//
// What only this test can prove:
//
//   - the handshake carries the tsserver path (without initializationOptions the
//     server does not run at all, and a fake server would happily accept the
//     missing field);
//   - the answer is exact across files in a TypeScript project: same-named
//     methods on different classes, a re-export barrel, a generic, and JSX;
//   - the same works in plain JavaScript with nothing but a jsconfig.json;
//   - a `typescript` package with no lib/tsserver.js (the 7.x native port) is
//     rejected instead of being handed to the server and killing it.
//
// Like the gopls test, every test here SKIPS when the pieces are not installed:
// a language server is an optional program on the user's machine.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- the TypeScript fixture ------------------------------------------------

// tsModels declares two classes that both have a `describe` method, plus a
// generic function: the two homonyms are what a name-based index cannot tell
// apart, and the generic is what makes the server's answer type-checked rather
// than syntactic.
const tsModels = `export class User {
  id = 0;
  describe(): string { return "user"; }
}

export class Group {
  id = 0;
  describe(): string { return "group"; }
}

export function first<T>(items: T[]): T | undefined { return items[0]; }
`

// tsStore calls both `describe`s and the generic. The two calls are on separate
// lines on purpose: it is what lets the test show that the server picked one
// method and not the other, instead of everything that matched the text.
const tsStore = `import { Group, User, first } from "./models";

export function loadUser(): User { return new User(); }
export function loadGroup(): Group { return new Group(); }

export function label(u: User): string {
  return u.describe();
}

export function tag(g: Group): string {
  return g.describe();
}

export function pick(users: User[]): User | undefined {
  return first(users);
}
`

// tsWidget is JSX: a component that receives a typed prop and calls the method
// on it. A file the other fixtures would never reach.
const tsWidget = `import { User } from "./models";

export function Widget({ user }: { user: User }) {
  return <div>{user.describe()}</div>;
}
`

// tsBarrel re-exports the module, so the consumer below reaches User through it
// and never mentions ./models: the answer has to follow the re-export.
const tsBarrel = `export * from "./models";
export { loadUser } from "./store";
`

const tsConsumer = `import { User } from "./index";

export function show(u: User): string {
  return u.describe();
}
`

const tsConfig = `{
  "compilerOptions": {
    "target": "es2020",
    "module": "esnext",
    "moduleResolution": "bundler",
    "strict": true,
    "jsx": "react-jsx",
    "noEmit": true
  },
  "include": ["*.ts", "*.tsx"]
}
`

// tsFixture writes the TypeScript project and returns its root.
func tsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "tsconfig.json", tsConfig)
	writeFixture(t, root, "models.ts", tsModels)
	writeFixture(t, root, "store.ts", tsStore)
	writeFixture(t, root, "widget.tsx", tsWidget)
	writeFixture(t, root, "index.ts", tsBarrel)
	writeFixture(t, root, "consumer.ts", tsConsumer)
	return root
}

// --- the JavaScript fixture ------------------------------------------------

const jsUtil = `export function format(value) { return String(value); }
`

const jsUse = `import { format } from "./util";

export function show(v) {
  return format(v);
}
`

const jsConfigFile = `{
  "compilerOptions": {
    "target": "es2020",
    "module": "esnext",
    "moduleResolution": "bundler",
    "allowJs": true
  },
  "include": ["*.js"]
}
`

func jsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "jsconfig.json", jsConfigFile)
	writeFixture(t, root, "util.js", jsUtil)
	writeFixture(t, root, "use.js", jsUse)
	return root
}

// --- driving the real server -----------------------------------------------

// tsLanguage returns the TypeScript table entry, failing the test if the table
// lost it.
func tsLanguage(t *testing.T) Language {
	t.Helper()
	lang, ok := LanguageByID("typescript")
	if !ok {
		t.Fatal("the typescript language is missing from the table")
	}
	return lang
}

// realTS starts a real typescript-language-server for root, or skips the test.
//
// It goes through LaunchFor, so this also proves that discovery finds the server
// and that tsInitOptions hands the server a usable tsserver — the two things
// that decide whether the process survives its first second.
func realTS(t *testing.T, root string) *Client {
	t.Helper()
	if Disabled() {
		t.Skip("FASTEXEC_LSP is off")
	}
	lang := tsLanguage(t)
	srv := lang.Primary()
	launch, ok := LaunchFor(lang, root)
	if !ok {
		t.Skipf("%s not installed: %s", srv.Name, srv.Install)
	}
	if _, ok := launch.InitOptions["tsserver"]; !ok {
		t.Skipf("no tsserver found for %s: %s", srv.Name, tsDoctorDetails(launch.Command)[0])
	}
	c, err := New(context.Background(), Config{
		Launch:           launch,
		HandshakeTimeout: 60 * time.Second,
		Timeout:          60 * time.Second,
	})
	if err != nil {
		t.Fatalf("starting the real %s: %v", srv.Name, err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestTypeScriptReferencesTellSameNamedMethodsApart is the TypeScript mirror of
// the gopls test: `User.describe` and `Group.describe` are the same text, and
// only one of them may come back.
//
// The gabarito is written out in full (three call sites across four files: a
// direct call, a JSX component, and a call reached through a re-export barrel)
// because the point of the layer is the exact list, not a non-empty one.
func TestTypeScriptReferencesTellSameNamedMethodsApart(t *testing.T) {
	root := tsFixture(t)
	c := realTS(t, root)
	ctx := context.Background()

	userPath, userPos := posInFile(t, root, "models.ts", `describe(): string { return "user"`, "describe")
	locs, err := c.References(ctx, userPath, userPos, false)
	if err != nil {
		t.Fatalf("References on User.describe: %v", err)
	}
	got := locKeys(t, root, locs)
	want := []string{"consumer.ts:4", "store.ts:7", "widget.tsx:4"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("User.describe references =\n  %v\nwant\n  %v", got, want)
	}
	if contains(got, "store.ts:11") {
		t.Errorf("User.describe references = %v, must NOT contain store.ts:11: that is Group.describe", got)
	}

	// The mirror image, so the test cannot pass by returning everything.
	groupPath, groupPos := posInFile(t, root, "models.ts", `describe(): string { return "group"`, "describe")
	locs, err = c.References(ctx, groupPath, groupPos, false)
	if err != nil {
		t.Fatalf("References on Group.describe: %v", err)
	}
	got = locKeys(t, root, locs)
	if strings.Join(got, ",") != "store.ts:11" {
		t.Errorf("Group.describe references = %v, want [store.ts:11]", got)
	}
}

// TestTypeScriptReferencesResolveGenericsAndJSX covers the two shapes a
// text-matching index has no answer for at all: a call to a generic function
// (the argument type is inferred, the name is never written next to the
// declaration) and a call inside JSX.
func TestTypeScriptReferencesResolveGenericsAndJSX(t *testing.T) {
	root := tsFixture(t)
	c := realTS(t, root)
	ctx := context.Background()

	genPath, genPos := posInFile(t, root, "models.ts", "export function first<T>", "first")
	locs, err := c.References(ctx, genPath, genPos, false)
	if err != nil {
		t.Fatalf("References on 'first': %v", err)
	}
	// Two references, and both are real: the call, and the import binding that
	// brings the name into the file. An import is a use — the same list VS Code
	// shows — so the gabarito counts it.
	if got := locKeys(t, root, locs); strings.Join(got, ",") != "store.ts:1,store.ts:15" {
		t.Errorf("'first' references = %v, want [store.ts:1 store.ts:15]", got)
	}
}

// TestTypeScriptRenameEditsExactlyOneType renames one of the two homonyms and
// checks, edit by edit, that the other one is untouched — including after the
// edit is actually written, which is what the applicator is for.
func TestTypeScriptRenameEditsExactlyOneType(t *testing.T) {
	root := tsFixture(t)
	c := realTS(t, root)
	ctx := context.Background()

	userPath, userPos := posInFile(t, root, "models.ts", `describe(): string { return "user"`, "describe")
	we, err := c.Rename(ctx, userPath, userPos, "describeUser")
	if err != nil {
		t.Fatalf("Rename on User.describe: %v", err)
	}
	if we == nil {
		t.Fatal("Rename returned no edit")
	}
	edits := editedFiles(t, root, we)
	files := make([]string, 0, len(edits))
	for f := range edits {
		files = append(files, f)
	}
	sort.Strings(files)
	want := []string{"consumer.ts", "models.ts", "store.ts", "widget.tsx"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("rename touched %v, want %v", files, want)
	}
	// The call in store.ts:7 must move, the one in store.ts:11 must not: the
	// rename is of one type's method, not of the word.
	for _, e := range edits["store.ts"] {
		if e.Range.Start.Line == 10 {
			t.Errorf("rename edited store.ts line 11 (Group.describe), which it must not touch")
		}
	}

	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root}); err != nil {
		t.Fatalf("applying the rename: %v", err)
	}
	store, err := os.ReadFile(filepath.Join(root, "store.ts"))
	if err != nil {
		t.Fatalf("reading the renamed store.ts: %v", err)
	}
	text := string(store)
	if !strings.Contains(text, "u.describeUser();") {
		t.Errorf("after the rename, store.ts has no renamed call:\n%s", text)
	}
	if !strings.Contains(text, "g.describe();") {
		t.Errorf("after the rename, Group.describe in store.ts was changed too:\n%s", text)
	}
}

// TestTypeScriptHoverAndDiagnostics prove the two other operations the lsp-tipos
// flow reads: a hover that carries the inferred type, and a diagnostic for a
// file that does not compile.
func TestTypeScriptHoverAndDiagnostics(t *testing.T) {
	root := tsFixture(t)
	// A type error the compiler must report: the argument does not match.
	writeFixture(t, root, "broken.ts", "import { first } from \"./models\";\n\nexport const n: string = first([1, 2]) as unknown as string;\nexport const bad: number = \"not a number\";\n")
	broken := filepath.Join(root, "broken.ts")
	c := realTS(t, root)
	ctx := context.Background()

	if err := c.WaitSettled(ctx, broken, 20*time.Second); err != nil {
		t.Fatalf("waiting for diagnostics on broken.ts: %v", err)
	}
	diags, err := c.Diagnostics(ctx, broken, 0)
	if err != nil {
		t.Fatalf("Diagnostics on broken.ts: %v", err)
	}
	if len(diags) == 0 {
		t.Errorf("no diagnostics for a file that assigns a string to a number")
	}

	// Hover on the declaration of the generic: the signature it prints carries
	// the type parameter, which is what `lsp-tipos` shows.
	genPath, genPos := posInFile(t, root, "models.ts", "export function first<T>", "first")
	hover, err := c.HoverAt(ctx, genPath, genPos)
	if err != nil {
		t.Fatalf("HoverAt: %v", err)
	}
	if hover == nil || !strings.Contains(hover.Contents, "first") {
		t.Errorf("hover on the declaration of 'first' = %v, want the signature", hover)
	}
}

// TestJavaScriptReferencesFollowJsconfig is the JS half: no types, no tsconfig,
// just a jsconfig.json. Without the project config tsserver runs on a guessed
// project and the import in use.js may not resolve at all.
func TestJavaScriptReferencesFollowJsconfig(t *testing.T) {
	root := jsFixture(t)
	c := realTS(t, root)
	ctx := context.Background()

	utilPath, utilPos := posInFile(t, root, "util.js", "export function format", "format")
	locs, err := c.References(ctx, utilPath, utilPos, false)
	if err != nil {
		t.Fatalf("References on format: %v", err)
	}
	// The import binding and the call: the same two-reference shape as the
	// TypeScript fixture, which is the point of running the JS half at all.
	if got := locKeys(t, root, locs); strings.Join(got, ",") != "use.js:1,use.js:4" {
		t.Errorf("format references = %v, want [use.js:1 use.js:4]", got)
	}

	we, err := c.Rename(ctx, utilPath, utilPos, "render")
	if err != nil {
		t.Fatalf("Rename on format: %v", err)
	}
	if we == nil {
		t.Fatal("Rename returned no edit")
	}
	edits := editedFiles(t, root, we)
	if len(edits["use.js"]) == 0 || len(edits["util.js"]) == 0 {
		t.Fatalf("rename touched %v, want both util.js and use.js", edits)
	}
}

// --- the parts that need no server -----------------------------------------

// TestFindTSServerPrefersTheProjectInstall: the project's own TypeScript is
// preferred over anything global, because the answer then matches the compiler
// the project builds with. This is the same choice VS Code makes.
func TestFindTSServerPrefersTheProjectInstall(t *testing.T) {
	t.Setenv(tsserverEnv, "")
	root := t.TempDir()
	projectTS := writeFixture(t, root, filepath.Join("node_modules", "typescript", "lib", "tsserver.js"), "// project tsserver\n")
	if got := FindTSServer("", root); got != projectTS {
		t.Errorf("FindTSServer = %q, want the project's own %q", got, projectTS)
	}
	// The project's TypeScript outranks the one next to the server.
	prefix := t.TempDir()
	writeFixture(t, prefix, filepath.Join("node_modules", "typescript", "lib", "tsserver.js"), "// server's own tsserver\n")
	bin := writeFixture(t, prefix, filepath.Join("node_modules", ".bin", "typescript-language-server.cmd"), "@echo off\n")
	if got := FindTSServer(bin, root); got != projectTS {
		t.Errorf("FindTSServer = %q, want the project's own %q", got, projectTS)
	}
}

// TestFindTSServerRejectsPackageWithoutEntryPoint is the 7.x trap: the native
// port ships no lib/tsserver.js, and a prefix like that must be reported as "no
// tsserver" rather than handed to a server that dies on it.
func TestFindTSServerRejectsPackageWithoutEntryPoint(t *testing.T) {
	t.Setenv(tsserverEnv, "")
	root := t.TempDir()
	// typescript 7 layout: lib/ has tsc.js but no tsserver.js.
	broken := writeFixture(t, root, filepath.Join("node_modules", "typescript", "lib", "tsc.js"), "// native port\n")
	if got := FindTSServer("", root); got == broken {
		t.Errorf("FindTSServer = %q, want the broken prefix rejected", got)
	}
	// A file that exists but is empty is the same trap (a truncated install).
	// Only the empty candidate is asserted about: whether some other TypeScript
	// is installed on this machine is not this test's business.
	empty := t.TempDir()
	blank := writeFixture(t, empty, filepath.Join("node_modules", "typescript", "lib", "tsserver.js"), "")
	if got := FindTSServer("", empty); got == blank {
		t.Errorf("FindTSServer = %q, want an empty tsserver rejected", got)
	}
}

// TestFindTSServerHonoursTheEnvOverride: FASTEXEC_TSSERVER is the escape hatch
// for a TypeScript none of the conventions reach.
func TestFindTSServerHonoursTheEnvOverride(t *testing.T) {
	root := t.TempDir()
	other := writeFixture(t, root, filepath.Join("elsewhere", "tsserver.js"), "// explicit\n")
	t.Setenv(tsserverEnv, other)
	if got := FindTSServer("", root); got != other {
		t.Errorf("FindTSServer = %q, want the env override %q", got, other)
	}
	// A bogus override is not an answer: the conventions still apply.
	t.Setenv(tsserverEnv, filepath.Join(root, "nope", "tsserver.js"))
	project := writeFixture(t, root, filepath.Join("node_modules", "typescript", "lib", "tsserver.js"), "// project\n")
	if got := FindTSServer("", root); got != project {
		t.Errorf("FindTSServer = %q, want the project's own %q after a bogus override", got, project)
	}
}

// TestTSInitOptionsCarryInlayHints pins the preferences the lsp-tipos flow reads
// out of the hover/type hints, and the syntax-server choice that makes the
// cross-file answer exact instead of fast.
func TestTSInitOptionsCarryInlayHints(t *testing.T) {
	t.Setenv(tsserverEnv, "")
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("node_modules", "typescript", "lib", "tsserver.js"), "// tsserver\n")
	opts := tsInitOptions("", root)

	ts, ok := opts["tsserver"].(map[string]any)
	if !ok {
		t.Fatalf("tsInitOptions has no tsserver block: %v", opts)
	}
	if got := ts["path"]; got != filepath.Join(root, "node_modules", "typescript", "lib", "tsserver.js") {
		t.Errorf("tsserver.path = %v, want the project's tsserver", got)
	}
	if got := ts["useSyntaxServer"]; got != "never" {
		t.Errorf("useSyntaxServer = %v, want \"never\" (the syntax server has no project)", got)
	}
	prefs, ok := opts["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("tsInitOptions has no preferences block: %v", opts)
	}
	if prefs["includeInlayVariableTypeHints"] != true {
		t.Errorf("inlay variable type hints are off: the lsp-tipos flow shows nothing")
	}
}

// TestProjectWarningOnImplicitProject is the honesty line for a root with no
// tsconfig: the server runs, but on a project it guessed at, so the answer stops
// being labelled exact (see the bridge).
func TestProjectWarningOnImplicitProject(t *testing.T) {
	lang := tsLanguage(t)
	bare := t.TempDir()
	writeFixture(t, bare, "package.json", "{}\n")
	if got := ProjectWarning(lang, bare); got != "no tsconfig: by name for cross-file" {
		t.Errorf("ProjectWarning on a bare root = %q", got)
	}
	// A jsconfig is a config: nothing to warn about.
	configured := jsFixture(t)
	if got := ProjectWarning(lang, configured); got != "" {
		t.Errorf("ProjectWarning with a jsconfig.json = %q, want silence", got)
	}
	// A language with no strict notion never warns.
	if got := ProjectWarning(Language{ID: "go"}, bare); got != "" {
		t.Errorf("ProjectWarning for Go = %q, want silence", got)
	}
}

// TestRootForPrefersTheNearestConfigInAMonorepo: a package.json at the top and a
// tsconfig three levels down belongs to the tsconfig, because that is where the
// compiler options the answer depends on live.
func TestRootForPrefersTheNearestConfigInAMonorepo(t *testing.T) {
	lang := tsLanguage(t)
	root := t.TempDir()
	writeFixture(t, root, "package.json", "{}\n")
	pkg := filepath.Join(root, "packages", "app")
	file := writeFixture(t, pkg, filepath.Join("src", "main.ts"), "export const x = 1;\n")
	writeFixture(t, pkg, "tsconfig.json", tsConfig)

	if got := RootFor(lang, file); got != pkg {
		t.Errorf("RootFor = %q, want the package with the tsconfig %q", got, pkg)
	}
	// Without any config, the nearest plain marker is still honored: the server
	// runs, on a guessed project, and the bridge says so.
	loose := writeFixture(t, root, filepath.Join("packages", "lib", "src", "main.ts"), "export const y = 1;\n")
	if got := RootFor(lang, loose); got != root {
		t.Errorf("RootFor = %q, want the package.json root %q", got, root)
	}
}
