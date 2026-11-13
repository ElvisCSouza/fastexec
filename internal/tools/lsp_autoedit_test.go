// lsp_autoedit_test.go: o comportamento do assistente automático, com o servidor FALSO de fakelsp_test.go.
//
// O que se prova aqui é o contrato da diretriz, item por item: o rename é APLICADO (não só listado) e desfeito
// pelo patch; o homônimo errado não passa; acima do teto de arquivos não se aplica nada; assinatura mudada rende
// a lista exata de chamadores; import faltando é corrigido pelo quickfix; erro novo aparece e erro antigo não;
// prazo estourado omite os diagnósticos sem atrasar a resposta; CRLF continua CRLF.
//
// Os testes injetam pelas costuras documentadas (autoWarm/autoSpawn) porque o caminho de produção é o
// lsp.Manager() e subir o gopls de verdade em cada caso custaria segundos por teste — o gopls real tem o teste
// dele, no fim deste arquivo, e é pulado quando não está instalado.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/lsp"
)

// lspFixture é um projeto de mentira em disco + o servidor roteirizado.
type lspFixture struct {
	t       *testing.T
	root    string
	ctlPath string
	client  *lsp.Client
	release func()
}

// newLSPFixture monta o projeto: uma raiz temporária com `.git` (é o que ResolveRepoRoot procura) e o roteiro
// do servidor gravado em arquivo, porque quem o lê é o processo filho.
func newLSPFixture(t *testing.T, ctl toolFakeCtl) *lspFixture {
	t.Helper()
	t.Setenv("FASTEXEC_LSP", "on")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	b, err := json.Marshal(ctl)
	if err != nil {
		t.Fatalf("ctl: %v", err)
	}
	ctlPath := filepath.Join(t.TempDir(), "ctl.json")
	if err := os.WriteFile(ctlPath, b, 0o644); err != nil {
		t.Fatalf("ctl: %v", err)
	}
	return &lspFixture{t: t, root: root, ctlPath: ctlPath}
}

// gitRepo transforma a raiz num repositório de verdade com um commit: é o que permite medir "já estava sujo no
// git" e desfazer o patch com `git apply -R`. É um repositório descartável em TempDir, não o do usuário.
func (f *lspFixture) gitRepo() {
	f.t.Helper()
	run := func(args ...string) {
		f.t.Helper()
		cmd := exec.Command("git", append([]string{"-C", f.root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	// O repositório do teste manda no próprio fim de linha: sem isso um core.autocrlf global converteria o
	// arquivo ao desfazer o patch e o teste mediria a config da máquina, não o código.
	run("config", "core.autocrlf", "false")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture")
}

func (f *lspFixture) write(name, body string) string {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func (f *lspFixture) read(name string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil {
		f.t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func (f *lspFixture) path(name string) string {
	return filepath.Join(f.root, filepath.FromSlash(name))
}

// start sobe o servidor falso e o entrega às costuras do assistente. O caminho quente de produção já devolve um
// servidor que viu o arquivo, então o falso é aberto do mesmo jeito: é isso que dá ao teste o "antes" dos
// diagnósticos.
func (f *lspFixture) start() *lspFixture {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := lsp.New(ctx, lsp.Config{
		Launch: lsp.Launch{
			Name:       "tool-fake",
			LanguageID: "go",
			Command:    os.Args[0],
			Env: []string{
				"FAKELSP_TOOLS=1",
				"FAKELSP_CTL=" + f.ctlPath,
				"FAKELSP_ROOT=" + f.root,
			},
			Root: f.root,
		},
	})
	if err != nil {
		f.t.Fatalf("fake lsp: %v", err)
	}
	f.client, f.release = c, func() { c.Close() }

	oldWarm, oldSpawn := autoWarm, autoSpawn
	f.t.Cleanup(func() { autoWarm, autoSpawn = oldWarm, oldSpawn })
	acquire := func(ctx context.Context, file string) (*lsp.Client, func(), error) {
		// Mesma espera do spawn de produção: sem ela o "antes" dos diagnósticos seria vazio e todo erro
		// antigo contaria como novo. O prazo é curto porque num servidor só de push (o caso do teto de
		// diagnósticos) não há publicação nenhuma e a espera é paga até o fim.
		settle := 800 * time.Millisecond
		if err := c.WaitSettled(ctx, file, settle); err != nil && err != lsp.ErrLoading {
			return nil, nil, err
		}
		return c, func() {}, nil
	}
	autoWarm, autoSpawn = acquire, acquire
	f.t.Cleanup(f.release)
	return f
}

// noServer é o cenário oposto: nenhum servidor, nenhuma assistência, resposta de sempre.
func (f *lspFixture) noServer() *lspFixture {
	oldWarm, oldSpawn := autoWarm, autoSpawn
	f.t.Cleanup(func() { autoWarm, autoSpawn = oldWarm, oldSpawn })
	deny := func(context.Context, string) (*lsp.Client, func(), error) {
		return nil, nil, lsp.Unavailable("no server in this test")
	}
	autoWarm, autoSpawn = deny, deny
	return f
}

// edit chama a tool de verdade — o mesmo caminho do MCP — e devolve a resposta.
func (f *lspFixture) edit(name, content string) string {
	f.t.Helper()
	msg, err := HandleWriteFile(WriteFileArgs{
		Host:      "local",
		Path:      f.path(name),
		Content:   content,
		Overwrite: true,
	})
	if err != nil {
		f.t.Fatalf("write_file %s: %v", name, err)
	}
	return msg
}

const (
	oldA = "package main\n\nfunc ParseLine() {}\n"
	newA = "package main\n\nfunc ParseRecord() {}\n"
)

// renameCtl é o roteiro do rename correto: a declaração no arquivo editado e o uso no outro arquivo. As
// colunas são as do texto real: em "func ParseLine() {}" a declaração começa na coluna 5; em
// "func main() { ParseLine() }" o uso começa na 14.
func renameCtl(name string) toolFakeCtl {
	return toolFakeCtl{Rename: map[string][]toolFakeEdit{
		"":     {{Line: 2, Start: 5, End: 14, New: name}},
		"b.go": {{Line: 2, Start: 14, End: 23, New: name}},
	}}
}

// TestAutoRenameAppliesAndUndoes é o item (1) inteiro: o rename é aplicado nos outros arquivos, a resposta diz
// o que mudou e o patch desfaz.
func TestAutoRenameAppliesAndUndoes(t *testing.T) {
	f := newLSPFixture(t, renameCtl("ParseRecord"))
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	f.gitRepo()
	f.start()

	msg := f.edit("a.go", newA)

	// Duas ocorrências (a do próprio arquivo e a de b.go) em dois arquivos.
	if !strings.Contains(msg, "DONE ParseLine -> ParseRecord: all 2 uses in 2 files updated") {
		t.Fatalf("rename note missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "undo: git apply -R ") {
		t.Fatalf("undo hint missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "files changed: b.go; re-read before editing them") {
		t.Fatalf("changed-files note missing, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseRecord()") {
		t.Fatalf("b.go not renamed:\n%s", got)
	}
	if got := f.read("a.go"); got != newA {
		t.Fatalf("a.go = %q, want %q", got, newA)
	}

	// O patch é o caminho de volta: aplicado ao contrário, os dois arquivos voltam ao texto de antes.
	patch := ""
	for _, line := range strings.Split(msg, "\n") {
		if i := strings.Index(line, "undo: git apply -R "); i >= 0 {
			patch = strings.TrimSpace(line[i+len("undo: git apply -R "):])
		}
	}
	if patch == "" {
		t.Fatalf("no patch path in:\n%s", msg)
	}
	cmd := exec.Command("git", "-C", f.root, "apply", "-R", patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply -R %s: %v: %s", patch, err, out)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine()") {
		t.Fatalf("b.go not undone by the patch:\n%s", got)
	}
	if got := f.read("a.go"); got != oldA {
		t.Fatalf("a.go not undone by the patch: %q", got)
	}
}

// TestAutoRenameRejectsExtraChanges é a prova da máscara: o texto do servidor vale só quando é o texto novo a
// menos dos dois nomes. Qualquer mudança a mais — aqui, um comentário que ninguém pediu — derruba a prova e
// nada é gravado.
func TestAutoRenameRejectsExtraChanges(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{Rename: map[string][]toolFakeEdit{
		"": {
			{Line: 2, Start: 5, End: 14, New: "ParseRecord"},
			{Line: 0, Start: 0, End: 0, New: "// a change nobody asked for\n"},
		},
		"b.go": {{Line: 2, Start: 14, End: 23, New: "ParseRecord"}},
	}})
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	f.start()

	msg := f.edit("a.go", newA)

	if !strings.Contains(msg, "not applied: the server's edit does not match the change asked; by name") {
		t.Fatalf("mask proof did not reject, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine()") {
		t.Fatalf("b.go was edited anyway:\n%s", got)
	}
}

// TestAutoRenameNotRenamable: o servidor diz que este símbolo não é renomeável (PrepareRename devolve null).
// Nada é gravado e o aviso por nome assume.
func TestAutoRenameNotRenamable(t *testing.T) {
	ctl := renameCtl("ParseRecord")
	ctl.NoPrepareNull = true
	f := newLSPFixture(t, ctl)
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	f.start()

	msg := f.edit("a.go", newA)

	if !strings.Contains(msg, "not applied: the server does not rename this symbol; by name") {
		t.Fatalf("null prepare was not honoured, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine()") {
		t.Fatalf("b.go was edited anyway:\n%s", got)
	}
}

// TestAutoRenameOverFileLimit: um rename grande demais não é decisão de um efeito colateral de edit_file.
func TestAutoRenameOverFileLimit(t *testing.T) {
	ctl := renameCtl("ParseRecord")
	for i := 0; i < lspAutoRenameMaxFiles+5; i++ {
		ctl.Rename[filepath.ToSlash(filepath.Join("pkg", "f"+itoa(i)+".go"))] = []toolFakeEdit{
			{Line: 0, Start: 0, End: 0, New: ""},
		}
	}
	f := newLSPFixture(t, ctl)
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	f.start()

	msg := f.edit("a.go", newA)

	if !strings.Contains(msg, "above the limit of 100; not applied (by name)") {
		t.Fatalf("limit not enforced, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine()") {
		t.Fatalf("b.go was edited anyway:\n%s", got)
	}
}

// TestAutoRenameListsDirtyOutside: o arquivo já estava sujo por fora do FastExec — aplica assim mesmo e LISTA,
// porque quem decide sobre essa alteração é o usuário.
func TestAutoRenameListsDirtyOutside(t *testing.T) {
	f := newLSPFixture(t, renameCtl("ParseRecord"))
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	f.gitRepo()
	f.start()
	// Sujeira de fora: alguém mexeu em b.go depois do commit.
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n// a comment added outside FastExec\n")

	msg := f.edit("a.go", newA)

	if !strings.Contains(msg, "already modified outside FastExec: b.go") {
		t.Fatalf("outside change not listed, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseRecord()") {
		t.Fatalf("the rename was skipped because of the dirt, want applied:\n%s", got)
	}
	// A alteração de fora continua lá: o FastExec renomeou em cima dela, não a apagou.
	if got := f.read("b.go"); !strings.Contains(got, "a comment added outside FastExec") {
		t.Fatalf("the outside change was lost:\n%s", got)
	}
}

// TestAutoSignatureListsCallers é o item (2) pelo caminho que o gopls usa: sem ação pronta de mudança de
// assinatura, a resposta entrega os chamadores exatos.
func TestAutoSignatureListsCallers(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{References: map[string][]toolFakeEdit{
		"a.go": {{Line: 2, Start: 5, End: 14}}, // a declaração (sai quando includeDeclaration é falso)
		"b.go": {{Line: 2, Start: 13, End: 22}, {Line: 4, Start: 13, End: 22}},
	}})
	f.write("a.go", "package main\n\nfunc ParseLine() {}\n")
	f.write("b.go", "package main\n\nfunc one() { ParseLine() }\n\nfunc two() { ParseLine() }\n")
	f.start()

	msg := f.edit("a.go", "package main\n\nfunc ParseLine(n int) {}\n")

	if !strings.Contains(msg, "ParseLine: signature changed; exactly 2 caller(s) need the new arguments") {
		t.Fatalf("caller list missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "b.go:3, b.go:5") {
		t.Fatalf("caller sites missing, got:\n%s", msg)
	}
}

// TestAutoSignatureActionApplied: quando o servidor OFERECE a refatoração pronta, ela é aplicada.
func TestAutoSignatureActionApplied(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{Actions: []toolFakeAction{{
		Title: "Change signature of ParseLine",
		Kind:  "refactor",
		Edits: []toolFakeEdit{
			{File: "b.go", Line: 2, Start: 13, End: 22, New: "ParseLine(0)"},
		},
	}}})
	f.write("a.go", "package main\n\nfunc ParseLine() {}\n")
	f.write("b.go", "package main\n\nfunc one() { ParseLine() }\n")
	f.start()

	msg := f.edit("a.go", "package main\n\nfunc ParseLine(n int) {}\n")

	if !strings.Contains(msg, "ParseLine: signature change applied by the server (exact)") {
		t.Fatalf("action not applied, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine(0)") {
		t.Fatalf("caller not updated:\n%s", got)
	}
}

// TestAutoImportQuickfix é o item (3): o erro novo é de símbolo não resolvido e o servidor oferece o import.
func TestAutoImportQuickfix(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{Actions: []toolFakeAction{{
		Title: "Add import \"fmt\"",
		Kind:  "quickfix",
		Edits: []toolFakeEdit{{File: "", Line: 2, Start: 0, End: 0, New: "import \"fmt\"\n"}},
	}}})
	f.write("a.go", "package main\n\nfunc main() {}\n")
	f.start()

	msg := f.edit("a.go", "package main\n\nimport \"fmt\"\n\nfunc main() {}\n//ERR undefined: fmt\n")

	if !strings.Contains(msg, "[lsp] added import \"fmt\"") {
		t.Fatalf("import note missing, got:\n%s", msg)
	}
	if strings.Contains(msg, "diagnostics:") {
		t.Fatalf("the fixed diagnostic was reported as new, got:\n%s", msg)
	}
	if got := f.read("a.go"); !strings.Contains(got, "import \"fmt\"\n") {
		t.Fatalf("import not written:\n%s", got)
	}
}

// TestAutoDiagnosticsNewOnly é o item (4): erro que já estava lá não é notícia; erro que a edição criou é.
func TestAutoDiagnosticsNewOnly(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{})
	f.write("a.go", "package main\n\nfunc main() {}\n//ERR undefined: old\n")
	f.start()

	msg := f.edit("a.go", "package main\n\nfunc main() {}\n//ERR undefined: old\n//ERR undefined: fresh\n")

	if !strings.Contains(msg, "diagnostics: 1 error") {
		t.Fatalf("want exactly one new error, got:\n%s", msg)
	}
	if !strings.Contains(msg, "undefined: fresh") {
		t.Fatalf("the new error is missing, got:\n%s", msg)
	}
	if strings.Contains(msg, "undefined: old") {
		t.Fatalf("the pre-existing error was reported as new, got:\n%s", msg)
	}
}

// TestAutoDiagnosticsTimeoutOmitted: o prazo estoura, a resposta sai sem diagnósticos e não espera por eles.
func TestAutoDiagnosticsTimeoutOmitted(t *testing.T) {
	body := "package main\n\nfunc main() {}\n"

	f := newLSPFixture(t, toolFakeCtl{Hang: []string{"textDocument/diagnostic"}, NoPush: true})
	f.write("a.go", body)
	f.start()
	start := time.Now()
	withServer := f.edit("a.go", "package main\n\nfunc main() {\n\t// a body edit\n}\n")
	elapsed := time.Since(start)

	if strings.Contains(withServer, "[lsp]") {
		t.Fatalf("diagnostics should have been omitted, got:\n%s", withServer)
	}
	if elapsed > lspAutoDiagBudget+2*time.Second {
		t.Fatalf("the edit waited for diagnostics: %v", elapsed)
	}

	// Byte a byte igual à resposta sem servidor nenhum: é o "nada a dizer" da diretriz.
	plain := newLSPFixture(t, toolFakeCtl{}).noServer()
	plain.write("a.go", body)
	without := plain.edit("a.go", "package main\n\nfunc main() {\n\t// a body edit\n}\n")
	if withServer != without {
		t.Fatalf("response changed with a silent server:\n%q\n%q", withServer, without)
	}
}

// TestAutoNoSemanticEffectByteIdentical: corpo e comentário não mexem em declaração nenhuma — a resposta com
// servidor é exatamente a de sempre.
func TestAutoNoSemanticEffectByteIdentical(t *testing.T) {
	oldText := "package main\n\nfunc main() {\n}\n"
	newText := "package main\n\nfunc main() {\n\t// only the body changed\n}\n"

	withServer := newLSPFixture(t, toolFakeCtl{})
	withServer.write("a.go", oldText)
	withServer.start()
	got := withServer.edit("a.go", newText)

	plain := newLSPFixture(t, toolFakeCtl{}).noServer()
	plain.write("a.go", oldText)
	want := plain.edit("a.go", newText)

	if got != want {
		t.Fatalf("a body edit changed the response:\n%q\n%q", got, want)
	}
}

// TestAutoRenameKeepsCRLF: o arquivo do outro lado tem fim de linha do Windows e continua tendo.
func TestAutoRenameKeepsCRLF(t *testing.T) {
	f := newLSPFixture(t, renameCtl("ParseRecord"))
	f.write("a.go", strings.ReplaceAll(oldA, "\n", "\r\n"))
	f.write("b.go", "package main\r\n\r\nfunc main() { ParseLine() }\r\n")
	f.start()

	msg := f.edit("a.go", strings.ReplaceAll(newA, "\n", "\r\n"))
	if !strings.Contains(msg, "DONE ParseLine -> ParseRecord: all 2 uses in 2 files updated") {
		t.Fatalf("rename did not run, got:\n%s", msg)
	}
	got := f.read("b.go")
	if !strings.Contains(got, "ParseRecord()") {
		t.Fatalf("b.go not renamed:\n%q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("CRLF was lost:\n%q", got)
	}
	if !strings.HasSuffix(got, "\r\n") {
		t.Fatalf("trailing CRLF lost:\n%q", got)
	}
}

// TestAutoOrphansExact é o item (6): a declaração sumiu e o servidor diz exatamente quem ficou órfão.
func TestAutoOrphansExact(t *testing.T) {
	f := newLSPFixture(t, toolFakeCtl{References: map[string][]toolFakeEdit{
		"a.go": {{Line: 2, Start: 5, End: 14}}, // a declaração
		"b.go": {{Line: 2, Start: 14, End: 23}},
	}})
	f.write("a.go", "package main\n\nfunc ParseLine() {}\n\nfunc Keep() {}\n")
	f.write("b.go", "package main\n\nfunc one() { ParseLine() }\n")
	f.start()

	// Some a declaração; outra fica, senão o arquivo não teria mais declaração nenhuma para comparar.
	msg := f.edit("a.go", "package main\n\nfunc Keep() {}\n")

	if !strings.Contains(msg, "ParseLine removed but still used 1 time(s) (exact): b.go:3") {
		t.Fatalf("orphan list missing, got:\n%s", msg)
	}
}

// itoa evita importar strconv só para montar nomes de arquivo.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestAutoRenameWithRealGopls é a prova com o servidor de verdade: dois métodos de mesmo nome em tipos
// diferentes, e só o certo muda. Sem gopls instalado, o teste é pulado — o núcleo já foi exercitado acima.
func TestAutoRenameWithRealGopls(t *testing.T) {
	if testing.Short() {
		t.Skip("short: the real server loads the module")
	}
	if _, _, ok := lsp.Find(lsp.Language{ID: "go", Servers: []lsp.Server{{Name: "gopls"}}}); !ok {
		t.Skip("gopls not installed")
	}
	t.Setenv("FASTEXEC_LSP", "on")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.21\n")
	oldSrc := `package fixture

type A struct{}

func (A) Parse() {}

type B struct{}

func (B) Parse() {}

func UseA(a A) { a.Parse() }

func UseB(b B) { b.Parse() }
`
	write("a.go", oldSrc)
	newSrc := strings.Replace(oldSrc, "func (A) Parse() {}", "func (A) ParseLine() {}", 1)

	// Caminho de produção, sem costura nenhuma: quem sobe e mantém o gopls é o lsp.Manager(). O servidor deste
	// projeto é devolvido antes da remoção do TempDir (cleanups rodam do último para o primeiro), senão o gopls
	// ainda seguraria os arquivos abertos e o Windows recusaria apagá-los. StopRoot, e não Shutdown: o gerente é
	// global e os outros testes desta suíte ainda vão usá-lo.
	t.Cleanup(func() { lsp.Manager().StopRoot(root) })

	msg, err := HandleWriteFile(WriteFileArgs{
		Host: "local", Path: filepath.Join(root, "a.go"), Content: newSrc, Overwrite: true,
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(msg, "Parse -> ParseLine") {
		t.Fatalf("gopls rename not applied, got:\n%s", msg)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.go"))
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}
	if !strings.Contains(string(got), "func (A) ParseLine() {}") || !strings.Contains(string(got), "a.ParseLine()") {
		t.Fatalf("the right symbol was not renamed:\n%s", got)
	}
	// O homônimo do outro tipo fica intacto — é o ponto inteiro do teste.
	if !strings.Contains(string(got), "func (B) Parse() {}") || !strings.Contains(string(got), "b.Parse()") {
		t.Fatalf("the homonym was renamed too:\n%s", got)
	}
}

// realGoplsAuto monta o projeto mínimo para os testes de edição assistida com o gopls de verdade.
func realGoplsAuto(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("short: the real server loads the module")
	}
	if _, _, ok := lsp.Find(lsp.Language{ID: "go", Servers: []lsp.Server{{Name: "gopls"}}}); !ok {
		t.Skip("gopls not installed")
	}
	t.Setenv("FASTEXEC_LSP", "on")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeGo(t, root, "go.mod", "module fixture\n\ngo 1.21\n")
	t.Cleanup(func() { lsp.Manager().StopRoot(root) })
	return root
}

// TestAutoSignatureListsSameFileCallers é o item E: quem chama a assinatura antiga DENTRO do arquivo editado
// também quebra o build, e era justamente ele que ficava de fora do aviso (o build acusava dois erros e a
// resposta listava um).
func TestAutoSignatureListsSameFileCallers(t *testing.T) {
	root := realGoplsAuto(t)
	const both = `package fixture

type Both struct{}

func (b Both) Run(x int) int { return x }

func UseBoth(b Both) int { return b.Run(1) }
`
	writeGo(t, root, "both.go", both)
	writeGo(t, root, "other.go", "package fixture\n\nfunc UseBothAgain(b Both) int { return b.Run(2) }\n")

	msg, err := handleWriteFile(WriteFileArgs{
		Host: "local", Path: filepath.Join(root, "both.go"),
		Content: strings.Replace(both, "Run(x int) int", "Run(x int, y int) int", 1), Overwrite: true,
	}, false)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(msg, "Run: signature changed; exactly 2 caller(s) need the new arguments") {
		t.Fatalf("same-file caller missing from the warning, got:\n%s", msg)
	}
	if !strings.Contains(msg, "both.go:7") || !strings.Contains(msg, "other.go:3") {
		t.Fatalf("want the two call sites, got:\n%s", msg)
	}
}

// TestAutoDiagnosticsOnBrokenBuild é o item F com o servidor de verdade: a edição quebra o build e o erro novo
// aparece — inclusive fora do trecho editado, que é onde a recuperação do parser o coloca. E o mesmo erro não é
// anunciado de novo na edição seguinte: só o que é novo é notícia.
func TestAutoDiagnosticsOnBrokenBuild(t *testing.T) {
	root := realGoplsAuto(t)
	writeGo(t, root, "alpha.go", bridgeAlpha)
	writeGo(t, root, "beta.go", bridgeBeta)
	writeGo(t, root, "caller.go", bridgeCaller)
	// O arquivo editado nunca foi aberto pelo servidor: é o caso em que não havia "antes" nenhum e o erro novo
	// fora do trecho era descartado como se fosse antigo.
	if _, err := lspCallers(root, "Do", 50, 1); err != nil {
		t.Fatalf("aquecer (lsp_callers): %v", err)
	}

	broken := "package demo\n\nfunc CallAlpha(a Alpha) int { return a.Do( \n\nfunc CallBeta(b Beta) int { return b.Do() }\n"
	msg, err := handleWriteFile(WriteFileArgs{
		Host: "local", Path: filepath.Join(root, "caller.go"), Content: broken, Overwrite: true,
	}, false)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(msg, "[lsp] diagnostics:") || !strings.Contains(msg, "expected '('") {
		t.Fatalf("the new error did not show up, got:\n%s", msg)
	}

	// Segunda edição, mesmo erro: a mensagem já era conhecida, então não é anunciada de novo.
	broken2 := strings.Replace(broken, "\n\nfunc CallBeta", "\n// comentário novo\nfunc CallBeta", 1)
	msg2, err := handleWriteFile(WriteFileArgs{
		Host: "local", Path: filepath.Join(root, "caller.go"), Content: broken2, Overwrite: true,
	}, false)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if strings.Contains(msg2, "[lsp] diagnostics:") {
		t.Fatalf("the same error was announced twice, got:\n%s", msg2)
	}
}

// TestAutoUndoPatchUndoesTheWholeEdit é o item D com o servidor de verdade: o patch anunciado na resposta
// precisa desfazer a edição inteira — o rename do servidor, os outros arquivos e a formatação que veio depois —
// e devolver cada arquivo ao conteúdo original, byte a byte.
func TestAutoUndoPatchUndoesTheWholeEdit(t *testing.T) {
	root := realGoplsAuto(t)
	// Indentação torta de propósito: a formatação do trecho vem DEPOIS do apply do rename e é ela que deixava
	// o patch anunciado obsoleto.
	const alpha = `package fixture

func   Alpha(x int) int {
return x
}
`
	const caller = "package fixture\n\nfunc Use(a int) int {\n\treturn Alpha(a)\n}\n"
	writeGo(t, root, "alpha.go", alpha)
	writeGo(t, root, "caller.go", caller)
	alphaPath := filepath.Join(root, "alpha.go")
	callerPath := filepath.Join(root, "caller.go")

	msg, err := handleWriteFile(WriteFileArgs{
		Host: "local", Path: alphaPath,
		Content: "package fixture\n\nfunc   Omega(x int) int {\nreturn x\n}\n", Overwrite: true,
	}, false)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	patch := undoPathIn(msg)
	if patch == "" {
		t.Fatalf("a resposta não anunciou patch de undo:\n%s", msg)
	}
	if _, err := os.Stat(patch); err != nil {
		t.Fatalf("o patch anunciado não existe: %v", err)
	}

	if _, err := lsp.Undo(patch, lsp.UndoOptions{Root: root}); err != nil {
		t.Fatalf("desfazer pelo patch do aviso: %v\nresposta:\n%s", err, msg)
	}
	if got := string(mustRead(t, alphaPath)); got != alpha {
		t.Errorf("alpha.go depois do undo = %q, queria %q", got, alpha)
	}
	if got := string(mustRead(t, callerPath)); got != caller {
		t.Errorf("caller.go depois do undo = %q, queria %q", got, caller)
	}
}

// undoPathIn tira da resposta o caminho do patch que o aviso manda aplicar.
func undoPathIn(msg string) string {
	const mark = "undo: git apply -R "
	i := strings.Index(msg, mark)
	if i < 0 {
		return ""
	}
	rest := msg[i+len(mark):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ler %s: %v", path, err)
	}
	return b
}
