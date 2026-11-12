package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/lsp"
)

// lsp_test.go cobre o que dá para provar sem rede e sem instalar nada de
// verdade: a leitura dos argumentos, os apelidos de linguagem, a recusa em
// instalar sem alguém para confirmar, e o --dry-run ponta a ponta (que roda o
// plano de verdade e não baixa byte nenhum).

// fakeItems devolve itens fixos, independentes da máquina que roda o teste: um
// teste de confirmação não pode depender de o Node estar instalado aqui.
func fakeItems(t *testing.T) []lsp.Item {
	t.Helper()
	if len(lsp.Pins) < 2 || len(lsp.Languages) < 2 {
		t.Fatal("catalogue is too small to build a fake plan")
	}
	return []lsp.Item{
		{Lang: lsp.Languages[0], Pin: lsp.Pins[0], Pinned: true, Ok: true,
			Version: lsp.Pins[0].Version, Size: "~20 MB", Origin: lsp.Pins[0].Origin, License: lsp.Pins[0].License},
		{Lang: lsp.Languages[1], Pin: lsp.Pins[1], Pinned: true, Ok: true,
			Version: lsp.Pins[1].Version, Size: "~15 MB", Origin: lsp.Pins[1].Origin, License: lsp.Pins[1].License},
	}
}

// --- argumentos ---

func TestParseLspArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want lspArgs
	}{
		{"a forma do enunciado", []string{"--yes", "--lang", "go,csharp,ts"},
			lspArgs{Yes: true, Langs: []string{"go,csharp,ts"}}},
		{"valor colado no sinal de igual", []string{"--lang=ts", "--root=w:/code"},
			lspArgs{Langs: []string{"ts"}, Roots: []string{"w:/code"}}},
		{"posicionais viram linguagens", []string{"go", "cs"},
			lspArgs{Rest: []string{"go", "cs"}}},
		{"repetir --lang acumula", []string{"--lang", "go", "-l", "ts"},
			lspArgs{Langs: []string{"go", "ts"}}},
		{"dry-run curto", []string{"-n"},
			lspArgs{DryRun: true}},
		{"nada", nil, lspArgs{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLspArgs(c.args, "install")
			if err != nil {
				t.Fatalf("parseLspArgs(%q): %v", c.args, err)
			}
			if got.Yes != c.want.Yes || got.DryRun != c.want.DryRun ||
				strings.Join(got.Langs, "|") != strings.Join(c.want.Langs, "|") ||
				strings.Join(got.Roots, "|") != strings.Join(c.want.Roots, "|") ||
				strings.Join(got.Rest, "|") != strings.Join(c.want.Rest, "|") {
				t.Fatalf("parseLspArgs(%q) = %+v, queria %+v", c.args, got, c.want)
			}
		})
	}
}

func TestParseLspArgsRejectsBadFlags(t *testing.T) {
	if _, err := parseLspArgs([]string{"--lang"}, "install"); err == nil {
		t.Fatal("--lang sem valor devia falhar")
	} else if !strings.Contains(err.Error(), "--lang") {
		t.Fatalf("o erro devia nomear --lang, veio %q", err)
	}
	if _, err := parseLspArgs([]string{"--frobnicate"}, "install"); err == nil {
		t.Fatal("flag desconhecida devia falhar")
	} else if !strings.Contains(err.Error(), "--frobnicate") || !strings.Contains(err.Error(), lspUsage) {
		t.Fatalf("o erro devia nomear a flag e mostrar o uso, veio %q", err)
	}
}

// --- detecção e apelidos ---

// tree escreve arquivos de exemplo e devolve a pasta, para o plano enxergar as
// linguagens sem varrer o repositório de verdade.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// plannedIDs é tudo que o plano decidiu, instalável agora ou bloqueado: o que
// importa aqui é que a linguagem pedida foi reconhecida.
func plannedIDs(sel lsp.Selection) map[string]bool {
	got := map[string]bool{}
	for _, it := range append(append([]lsp.Item{}, sel.Items...), sel.Blocked...) {
		got[it.Lang.ID] = true
	}
	return got
}

func TestLspPlanAcceptsShortLanguageNames(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "package main\n"})
	_, sel, err := lspPlan([]string{root}, []string{"cs,ts"})
	if err != nil {
		t.Fatalf("apelidos deviam ser aceitos: %v", err)
	}
	got := plannedIDs(sel)
	for _, want := range []string{"csharp", "typescript"} {
		if !got[want] {
			t.Fatalf("`--lang cs,ts` devia resolver %s, veio %v", want, got)
		}
	}
	// Pedido explícito não vira "detectado": nada aqui foi achado na pasta.
	for _, it := range sel.Items {
		if it.Detected {
			t.Fatalf("%s não devia estar marcado como detectado", it.Lang.ID)
		}
	}
	// Bloqueado continua sendo bloqueado, mas com o motivo: faltando
	// pré-requisito, a mensagem diz qual.
	for _, it := range sel.Blocked {
		if len(it.Missing) == 0 {
			t.Fatalf("%s foi bloqueado por outro motivo; o teste espera pré-requisito", it.Lang.ID)
		}
	}
}

func TestLspPlanUnknownLanguageQuotesWhatWasTyped(t *testing.T) {
	_, _, err := lspPlan(nil, []string{"cobol"})
	if err == nil {
		t.Fatal("linguagem fora do catálogo devia falhar")
	}
	if !strings.Contains(err.Error(), "cobol") || !strings.Contains(err.Error(), "go") {
		t.Fatalf("o erro devia citar o que foi digitado e o catálogo, veio %q", err)
	}
}

func TestLspPlanWithoutLangIsTheDetectedSet(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n", "b/c.go": "package c\n"})
	items, sel, err := lspPlan([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := plannedIDs(sel)
	if !got["go"] {
		t.Fatalf("os dois .go deviam pré-selecionar Go, veio %v", got)
	}
	if got["zig"] {
		t.Fatal("Zig não aparece na pasta e não devia ser pré-selecionado")
	}
	// O resto do catálogo fica de fora da seleção, e é isso que o modo
	// interativo mostra desmarcado.
	for _, it := range items {
		if it.Lang.ID == "go" {
			continue
		}
		if it.Detected {
			t.Fatalf("%s não devia ter sido detectado", it.Lang.ID)
		}
	}
}

// --- confirmação ---

func TestConfirmationWithYesInstallsEverything(t *testing.T) {
	items := fakeItems(t)
	got, err := decideConfirmation(items, true, nil, "Install")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(items) {
		t.Fatalf("--yes devia levar tudo, levou %d de %d", len(got), len(items))
	}
}

func TestConfirmationWithoutTerminalInstallsNothing(t *testing.T) {
	items := fakeItems(t)
	got, err := decideConfirmation(items, false, nil, "Install")
	if err == nil {
		t.Fatal("sem --yes e sem terminal nada pode ser instalado")
	}
	if len(got) != 0 {
		t.Fatalf("nada devia ser escolhido, veio %d", len(got))
	}
	for _, want := range []string{"--yes", "--lang", items[0].Lang.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("o erro devia ensinar %q, veio %q", want, err)
		}
	}
}

func TestConfirmationAsksOneByOne(t *testing.T) {
	items := fakeItems(t)
	var asked []string
	answers := map[string]bool{items[0].Lang.ID: true} // só a primeira leva sim
	got, err := decideConfirmation(items, false, func(it lsp.Item) bool {
		asked = append(asked, it.Lang.ID)
		return answers[it.Lang.ID]
	}, "Install")
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != len(items) {
		t.Fatalf("cada linguagem devia ser perguntada, perguntou %v", asked)
	}
	if len(got) != 1 || got[0].Lang.ID != items[0].Lang.ID {
		t.Fatalf("só a primeira devia ter sido aceita, veio %+v", got)
	}
}

// --- ponta a ponta, sem rede ---

// runLSP executa o CLI num processo filho com um FASTEXEC_HOME vazio e devolve
// a saída, o código e a pasta usada: é como se prova que nada foi baixado.
func runLSP(t *testing.T, args ...string) (string, int, string) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], append([]string{cliChildArg}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "FASTEXEC_HOME="+home,
		"FASTEXEC_CONFIG=", "FASTEXEC_DEFAULT_HOST=", "FASTEXEC_DATA_DIR=", "FASTEXEC_TELEMETRY=",
		"FASTEXEC_NO_TRAY=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code, home
}

// installedInHome lista o que o comando gravou em <home>/lsp. Vazio é o
// resultado esperado de todo caso que não instala.
func installedInHome(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "lsp"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestLspInstallDryRunDownloadsNothing roda o plano de verdade — detecção,
// pré-requisitos, versão fixada — e para antes de qualquer download.
func TestLspInstallDryRunDownloadsNothing(t *testing.T) {
	out, code, home := runLSP(t, "lsp", "install", "--lang", "go", "--dry-run")
	if code != 0 {
		t.Fatalf("--dry-run devia sair com 0, saiu %d:\n%s", code, out)
	}
	pin, ok := lsp.PinFor(mustLang(t, "go"))
	if !ok {
		t.Fatal("o catálogo precisa de um pin para Go")
	}
	for _, want := range []string{pin.Server, pin.Version, pin.License} {
		if !strings.Contains(out, want) {
			t.Fatalf("o plano devia mostrar %q antes de perguntar:\n%s", want, out)
		}
	}
	if got := installedInHome(t, home); len(got) != 0 {
		t.Fatalf("--dry-run não pode gravar nada, gravou %v", got)
	}
}

// TestLspDoctorDownloadsNothing prova que o diagnóstico é só leitura.
func TestLspDoctorDownloadsNothing(t *testing.T) {
	out, code, home := runLSP(t, "lsp", "doctor")
	if code != 0 {
		t.Fatalf("doctor devia sair com 0 mesmo sem servidor, saiu %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Language servers") {
		t.Fatalf("doctor devia listar os servidores:\n%s", out)
	}
	if got := installedInHome(t, home); len(got) != 0 {
		t.Fatalf("doctor não pode gravar nada, gravou %v", got)
	}
}

// TestLspRemoveRefusesStranger: remover o que o FastExec não instalou é um erro
// claro, não um delete em PATH.
func TestLspRemoveRefusesStranger(t *testing.T) {
	out, code, home := runLSP(t, "lsp", "remove", "gopls")
	if code == 0 {
		t.Fatalf("remover servidor que não é nosso devia falhar:\n%s", out)
	}
	if !strings.Contains(out, "nothing to remove") {
		t.Fatalf("o erro devia dizer que não há o que remover:\n%s", out)
	}
	if got := installedInHome(t, home); len(got) != 0 {
		t.Fatalf("nada podia ser mexido, veio %v", got)
	}
}

func TestLspUsageGolden(t *testing.T) {
	out, code, _ := runLSP(t, "lsp", "bogus")
	if code == 0 {
		t.Fatal("subcomando desconhecido devia sair diferente de 0")
	}
	for _, want := range []string{"doctor", "install", "update", "remove", "--yes", "--lang"} {
		if !strings.Contains(out, want) {
			t.Fatalf("o uso devia mencionar %q:\n%s", want, out)
		}
	}
	out, code, _ = runLSP(t, "lsp")
	if code == 0 {
		t.Fatal("`lsp` sem subcomando devia sair diferente de 0")
	}
	if !strings.Contains(out, "install") {
		t.Fatalf("o uso devia listar os subcomandos:\n%s", out)
	}
}

func mustLang(t *testing.T, id string) lsp.Language {
	t.Helper()
	lang, ok := lsp.LanguageByID(id)
	if !ok {
		t.Fatalf("o catálogo precisa de %s", id)
	}
	return lang
}
