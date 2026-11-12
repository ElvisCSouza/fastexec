package lsp

// install_test.go: testes do planejador e do instalador. Nenhum deles toca a
// rede, roda `go install`/`npm`/`dotnet` nem instala gopls de verdade: o
// downloader e o runner são falsos, e é isso que permite testar falha de rede,
// checksum errado e comando de instalação que falha.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- falsos ---

type fakeDownloader struct {
	// data é o que "baixa"; err simula falha de rede.
	data []byte
	err  error
	urls []string
}

func (d *fakeDownloader) Download(_ context.Context, url, dest string) error {
	d.urls = append(d.urls, url)
	if d.err != nil {
		return d.err
	}
	return os.WriteFile(dest, d.data, 0o644)
}

type fakeRunner struct {
	err  error
	ran  []string
	exec string // cria um arquivo com esse nome na pasta, como um instalador de verdade
}

func (r *fakeRunner) Run(_ context.Context, name string, args []string, dir string, _ []string) error {
	r.ran = append(r.ran, name+" "+strings.Join(args, " "))
	if r.err != nil {
		return r.err
	}
	if r.exec != "" {
		return os.WriteFile(filepath.Join(dir, execName(r.exec)), []byte("#!/bin/sh\n"), 0o755)
	}
	return nil
}

// zipOf monta um .zip com um arquivo, e devolve os bytes e o sha256.
func zipOf(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("conteudo do servidor")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

// testPin é um pin de mentira, do tipo .zip, para não depender do catálogo real.
func testPin(name, version string, data []byte, sum string) Pin {
	return Pin{
		Lang: "go", Server: name, Version: version, Kind: KindZip,
		Origin: "test", License: "test",
		Assets: []Asset{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://example.invalid/" + name + ".zip", SHA256: sum}},
	}
}

// newTestInstaller monta um instalador isolado: a pasta de dados é um
// temporário e FASTEXEC_HOME aponta para lá, de modo que a descoberta (Find)
// enxergue exatamente o que o instalador gravou e nada do PC de verdade.
func newTestInstaller(t *testing.T, dl Downloader, rn Runner) (*Installer, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FASTEXEC_HOME", home)
	dir := filepath.Join(home, "lsp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Installer{Dir: dir, Downloader: dl, Runner: rn}, dir
}

// --- catálogo ---

func TestPinsAreFixedAndComplete(t *testing.T) {
	if len(Pins) == 0 {
		t.Fatal("the pin catalogue is empty")
	}
	for _, p := range Pins {
		if p.Server == "" || p.Version == "" || p.License == "" || p.Origin == "" {
			t.Errorf("%s: pin without server/version/license/origin: %+v", p.Server, p)
		}
		if strings.Contains(strings.ToLower(p.Version), "latest") {
			t.Errorf("%s: version %q is not fixed", p.Server, p.Version)
		}
		if _, ok := LanguageByID(p.Lang); !ok {
			t.Errorf("%s: pin points at unknown language %q", p.Server, p.Lang)
		}
		switch p.Kind {
		case KindZip, KindGz:
			if len(p.Assets) == 0 {
				t.Errorf("%s: a release pin without assets", p.Server)
			}
			for _, a := range p.Assets {
				if len(a.SHA256) != 64 {
					t.Errorf("%s %s/%s: sha256 %q is not a sha256", p.Server, a.OS, a.Arch, a.SHA256)
				}
				if !strings.HasPrefix(a.URL, "https://") {
					t.Errorf("%s: asset without https url: %s", p.Server, a.URL)
				}
			}
			if p.SizeFor(runtime.GOOS, runtime.GOARCH) == "" {
				t.Errorf("%s: no size to show before downloading", p.Server)
			}
		case KindGo, KindNpm, KindDotnet:
			if p.Pkg == "" {
				t.Errorf("%s: %s pin without package", p.Server, p.Kind)
			}
		default:
			t.Errorf("%s: unknown install kind %q", p.Server, p.Kind)
		}
	}
}

func TestEveryLanguageReachesAPinOrSaysItIsManual(t *testing.T) {
	for _, l := range Languages {
		it := PlanItem(l, nil, runtime.GOOS, runtime.GOARCH)
		if it.Ok {
			continue
		}
		if len(it.Missing) > 0 {
			continue // tem versão fixada, só falta o pré-requisito
		}
		if it.Manual == "" {
			t.Errorf("%s: neither installable nor a manual command", l.ID)
		}
	}
}

// --- detecção ---

func TestDetectCountsFindsTheLanguagesInUse(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main")
	write("internal/a.go", "package internal")
	write("web/app.ts", "export {}")
	write("web/x.tsx", "export {}")
	write("node_modules/lib/index.ts", "lixo que não conta") // pasta ignorada

	counts := DetectCounts(root)
	if counts["go"] != 2 {
		t.Errorf("go: got %d files, want 2", counts["go"])
	}
	if counts["typescript"] != 2 {
		t.Errorf("typescript: got %d files, want 2 (node_modules must not count)", counts["typescript"])
	}
	if counts["python"] != 0 {
		t.Errorf("python: got %d files, want 0", counts["python"])
	}
}

func TestPlanPreSelectsWhatIsDetectedAndLeavesTheRestAlone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := PlanAll(DetectCounts(root))
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.Lang.ID] = it
	}
	if !byID["go"].Detected || byID["go"].Files != 1 {
		t.Errorf("go should be detected with 1 file: %+v", byID["go"])
	}
	for _, id := range []string{"csharp", "python", "rust"} {
		if byID[id].Detected {
			t.Errorf("%s should not be pre-selected", id)
		}
	}
}

// --- pré-requisitos ---

func TestPrereqsExplainWhatIsMissing(t *testing.T) {
	old, oldProbe := lookPath, runProbe
	defer func() { lookPath, runProbe = old, oldProbe }()
	lookPath = func(name string) (string, error) {
		if name == "node" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	runProbe = func(args []string) (string, error) {
		if args[0] == "dotnet" {
			return "", errors.New("exit status 1") // dotnet com runtime, sem SDK
		}
		return "v1", nil
	}

	pin, ok := PinByServer("typescript-language-server")
	if !ok {
		t.Fatal("no pin for typescript-language-server")
	}
	prs := Prereqs(pin)
	byTool := map[string]Prereq{}
	for _, p := range prs {
		byTool[p.Tool] = p
	}
	if byTool["node"].Found {
		t.Error("node should be reported as missing")
	}
	if !strings.Contains(byTool["node"].Install, "not found on PATH") || byTool["node"].Install == "" {
		t.Errorf("node must come with the command that installs it: %q", byTool["node"].Install)
	}
	if !byTool["npm"].Found {
		t.Error("npm should be found")
	}
	if why := byTool["npm"].Why; why == "" {
		t.Error("the prerequisite must say why it is needed")
	}

	dotnetPin, ok := PinByServer("csharp-ls")
	if !ok {
		t.Fatal("no pin for csharp-ls")
	}
	if p := Prereqs(dotnetPin)[0]; p.Found {
		t.Errorf("a dotnet without an SDK must count as missing: %+v", p)
	}
}

func TestPlanBlocksAPinWhosePrerequisiteIsMissing(t *testing.T) {
	old, oldProbe := lookPath, runProbe
	defer func() { lookPath, runProbe = old, oldProbe }()
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	runProbe = func([]string) (string, error) { return "", errors.New("not found") }

	lang, ok := LanguageByID("go")
	if !ok {
		t.Fatal("no go in the catalogue")
	}
	it := PlanItem(lang, map[string]int{"go": 3}, runtime.GOOS, runtime.GOARCH)
	if it.Ok {
		t.Error("without go on PATH the item cannot be installed")
	}
	if len(it.Missing) == 0 || it.Missing[0] != "go" {
		t.Errorf("the missing tool must be named: %+v", it.Missing)
	}
	if !it.Detected || it.Files != 3 {
		t.Errorf("detection is independent of the prerequisite: %+v", it)
	}
}

// --- escolha / confirmação ---

func TestChooseUsesDetectedSetWhenNoLanguageIsAsked(t *testing.T) {
	items := []Item{
		{Lang: Language{ID: "go"}, Ok: true, Detected: true},
		{Lang: Language{ID: "rust"}, Ok: true, Detected: false},
		{Lang: Language{ID: "cpp"}, Ok: false, Manual: "instale na mão", Detected: true},
	}
	sel, err := Choose(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Items) != 1 || sel.Items[0].Lang.ID != "go" {
		t.Errorf("only the detected and installable ones: %+v", sel.Items)
	}
	if len(sel.Blocked) != 1 || sel.Blocked[0].Lang.ID != "cpp" {
		t.Errorf("the detected-but-impossible one must be reported as blocked: %+v", sel.Blocked)
	}
}

func TestChooseAcceptsAListAndRejectsUnknown(t *testing.T) {
	items := []Item{
		{Lang: Language{ID: "go"}, Ok: true},
		{Lang: Language{ID: "python"}, Ok: true},
		{Lang: Language{ID: "java"}, Manual: "sem comando único"},
	}
	sel, err := Choose(items, []string{"go,python"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Items) != 2 {
		t.Errorf("got %d items, want 2", len(sel.Items))
	}

	sel, err = Choose(items, []string{"java"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Items) != 0 || len(sel.Blocked) != 1 {
		t.Errorf("java has no pinned install: %+v", sel)
	}

	if _, err := Choose(items, []string{"klingon"}); err == nil {
		t.Error("an unknown language must be an error, not a silent skip")
	} else if !strings.Contains(err.Error(), "klingon") || !strings.Contains(err.Error(), "go") {
		t.Errorf("the error must name what was asked and what exists: %v", err)
	}
}

func TestAskConfirmsPerLanguage(t *testing.T) {
	in := strings.NewReader("n\ny\n\ns\n")
	ask := Ask(in, &bytes.Buffer{}, func(it Item) string { return "install " + it.Lang.ID + "? " })
	want := []bool{false, true, false, true}
	for i, w := range want {
		if got := ask(Item{Lang: Language{ID: "go"}}); got != w {
			t.Errorf("answer %d: got %v, want %v", i, got, w)
		}
	}
	if ask(Item{}) {
		t.Error("a reader at end of file must answer no, never yes by accident")
	}
}

// --- instalação ---

func TestInstallZipChecksTheChecksumAndPlacesTheBinary(t *testing.T) {
	data, sum := zipOf(t, execName("fakels"))
	pin := testPin("fakels", "1.2.3", data, sum)
	dl := &fakeDownloader{data: data}
	in, dir := newTestInstaller(t, dl, &fakeRunner{})
	in.Log = func(string, ...any) {}

	st, err := in.Install(context.Background(), pin)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	bin := filepath.Join(dir, "fakels", execName("fakels"))
	if !isExecutable(bin) {
		t.Fatalf("the executable is not at %s", bin)
	}
	if st.Version != "1.2.3" || st.SHA256 != sum {
		t.Errorf("wrong state: %+v", st)
	}
	got, ok := in.State("fakels")
	if !ok || got.Version != "1.2.3" || got.License != "test" {
		t.Errorf("installed.json not written or wrong: %+v ok=%v", got, ok)
	}
	if found, _, ok := Find(Language{ID: "go", Servers: []Server{{Name: "fakels"}}}); !ok || found != bin {
		t.Errorf("discovery must find the installed server: %q %v", found, ok)
	}
	if len(dl.urls) != 1 || !strings.Contains(dl.urls[0], "fakels.zip") {
		t.Errorf("downloaded %v, want the pinned url", dl.urls)
	}
}

func TestInstallRefusesAFileThatDoesNotMatchThePinnedHash(t *testing.T) {
	_, sum := zipOf(t, execName("fakels"))
	data, _ := zipOf(t, "outro.zip")
	in, dir := newTestInstaller(t, &fakeDownloader{data: data}, &fakeRunner{})

	_, err := in.Install(context.Background(), testPin("fakels", "1.2.3", data, sum))
	if err == nil {
		t.Fatal("a wrong checksum must fail the install")
	}
	if !strings.Contains(err.Error(), "SHA-256") || !strings.Contains(err.Error(), "nothing was installed") {
		t.Errorf("the error must be clear about the checksum: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakels")); !os.IsNotExist(err) {
		t.Error("nothing may be left behind after a bad checksum")
	}
	assertNoStaging(t, dir)
}

func TestNetworkFailureIsAClearErrorWithNoBrokenState(t *testing.T) {
	data, sum := zipOf(t, execName("fakels"))
	dl := &fakeDownloader{err: errors.New("dial tcp: no such host")}
	in, dir := newTestInstaller(t, dl, &fakeRunner{})

	_, err := in.Install(context.Background(), testPin("fakels", "1.2.3", data, sum))
	if err == nil {
		t.Fatal("a network failure must be an error")
	}
	if !strings.Contains(err.Error(), "no such host") {
		t.Errorf("the error must carry the cause: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the install folder must be empty, found %d entries", len(entries))
	}
}

func TestFailedInstallKeepsThePreviousVersionWorking(t *testing.T) {
	data, sum := zipOf(t, execName("fakels"))
	pin := testPin("fakels", "1.2.3", data, sum)
	in, dir := newTestInstaller(t, &fakeDownloader{data: data}, &fakeRunner{})
	if _, err := in.Install(context.Background(), pin); err != nil {
		t.Fatal(err)
	}

	// A segunda tentativa (versão nova) falha na rede.
	in.Downloader = &fakeDownloader{err: errors.New("connection reset")}
	if _, err := in.Install(context.Background(), testPin("fakels", "9.9.9", data, sum)); err == nil {
		t.Fatal("the second install was supposed to fail")
	}

	bin := filepath.Join(dir, "fakels", execName("fakels"))
	if !isExecutable(bin) {
		t.Fatal("the previous installation must still be there and runnable")
	}
	if st, ok := in.State("fakels"); !ok || st.Version != "1.2.3" {
		t.Errorf("the state must still describe the old version: %+v ok=%v", st, ok)
	}
	if got, _, ok := Find(Language{ID: "go", Servers: []Server{{Name: "fakels"}}}); !ok || got != bin {
		t.Errorf("discovery must still find the old binary: %q %v", got, ok)
	}
	assertNoStaging(t, dir)
}

func TestInstallerCommandFailureLeavesNoBrokenState(t *testing.T) {
	pin, ok := PinByServer("gopls")
	if !ok {
		t.Fatal("no pin for gopls")
	}
	rn := &fakeRunner{err: errors.New("exit status 1: no required module")}
	in, dir := newTestInstaller(t, &fakeDownloader{}, rn)

	_, err := in.Install(context.Background(), pin)
	if err == nil {
		t.Fatal("a failed go install must fail the install")
	}
	if !strings.Contains(err.Error(), "go install") || !strings.Contains(err.Error(), pin.Version) {
		t.Errorf("the error must show the command and the pinned version: %v", err)
	}
	if len(rn.ran) != 1 || !strings.Contains(rn.ran[0], "@"+pin.Version) {
		t.Errorf("the command must pin the version: %v", rn.ran)
	}
	if _, err := os.Stat(filepath.Join(dir, "gopls")); !os.IsNotExist(err) {
		t.Error("nothing may be left behind")
	}
	assertNoStaging(t, dir)
}

func TestInstallVerifiesTheBinaryReallyLanded(t *testing.T) {
	// O instalador "roda" mas não deixa executável nenhum: tem de falhar aqui,
	// não na primeira requisição LSP.
	in, dir := newTestInstaller(t, &fakeDownloader{}, &fakeRunner{})
	_, err := in.Install(context.Background(), Pin{
		Lang: "go", Server: "gopls", Version: "v0.24.0", Kind: KindGo, Pkg: "golang.org/x/tools/gopls",
	})
	if err == nil {
		t.Fatal("an install that produced no executable must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "gopls")); !os.IsNotExist(err) {
		t.Error("nothing may be left behind")
	}
	assertNoStaging(t, dir)
}

func TestInstallRejectsAPinWithoutAVersion(t *testing.T) {
	in, _ := newTestInstaller(t, &fakeDownloader{}, &fakeRunner{})
	if _, err := in.Install(context.Background(), Pin{Server: "gopls", Kind: KindGo, Pkg: "x"}); err == nil {
		t.Error("a pin without a fixed version must be refused")
	}
}

func TestInstallReplacesAnOlderVersionOfTheSameServer(t *testing.T) {
	data1, sum1 := zipOf(t, execName("fakels"))
	in, dir := newTestInstaller(t, &fakeDownloader{data: data1}, &fakeRunner{})
	if _, err := in.Install(context.Background(), testPin("fakels", "1.0.0", data1, sum1)); err != nil {
		t.Fatal(err)
	}
	data2, sum2 := zipOf(t, execName("fakels"))
	in.Downloader = &fakeDownloader{data: data2}
	if _, err := in.Install(context.Background(), testPin("fakels", "2.0.0", data2, sum2)); err != nil {
		t.Fatal(err)
	}
	if st, _ := in.State("fakels"); st.Version != "2.0.0" {
		t.Errorf("the state must describe the new version: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakels.old")); err == nil {
		t.Error("the old folder must not stay around")
	}
}

// --- remoção ---

func TestRemoveOnlyDeletesWhatFastExecInstalled(t *testing.T) {
	in, dir := newTestInstaller(t, &fakeDownloader{}, &fakeRunner{})
	foreign := filepath.Join(dir, "gopls")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, execName("gopls")), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := in.Remove("gopls"); err == nil {
		t.Fatal("a folder this installer did not create must not be deleted")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("the user's own folder must be left alone")
	}

	data, sum := zipOf(t, execName("fakels"))
	in.Downloader = &fakeDownloader{data: data}
	if _, err := in.Install(context.Background(), testPin("fakels", "1.2.3", data, sum)); err != nil {
		t.Fatal(err)
	}
	if err := in.Remove("fakels"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakels")); !os.IsNotExist(err) {
		t.Error("the installed server must be gone")
	}
	if len(InstalledList()) != 0 {
		t.Errorf("nothing should be listed as installed: %+v", InstalledList())
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`} {
		if err := in.Remove(bad); err == nil {
			t.Errorf("Remove(%q) must be refused", bad)
		}
	}
}

// --- arquivos ---

func TestExtractZipRefusesPathsOutsideTheFolder(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("malicioso")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(file, out); err == nil {
		t.Fatal("an archive entry escaping the folder must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("the file must not have been written outside")
	}
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("staging folder left behind: %s", e.Name())
		}
	}
}
