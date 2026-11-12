package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestParseListingKind: tabela de decisão do reconhecedor. Só listagem recursiva inequívoca entra; o resto passa
// intacto (uma reformatação errada custaria a lista inteira).
func TestParseListingKind(t *testing.T) {
	sim := map[string]listingKind{
		`dir /s /b src\Serilog\*.cs`:      listingPathLines,
		`dir /s /b`:                       listingPathLines,
		`dir /sb src`:                     listingPathLines,
		`cmd /c dir /s /b src`:            listingPathLines,
		`ls -R`:                           listingLsRecursive,
		`ls -laR src`:                     listingLsRecursive,
		`ls --recursive .`:                listingLsRecursive,
		`find . -name '*.cs'`:             listingPathLines,
		`find src -iname '*_test.go'`:     listingPathLines,
		`find . -name '*.ext' -print`:     listingPathLines,
		`find . -name '*.cs' -maxdepth 4`: listingPathLines,
	}
	for cmd, want := range sim {
		if got := parseListingKind(cmd); got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
	nao := []string{
		`dir /b src`,                  // sem /s não há recursão: a lista já é curta
		`dir src`,                     // saída em colunas (data, tamanho, nome)
		`ls -l src`,                   // sem -R
		`ls src`,                      // sem -R
		`find . -type f`,              // sem -name
		`find . -name '*.cs' -delete`, // muda o disco, a saída não é a lista
		`find . -name '*.cs' -exec rm {} +`,
		`ls -R | head -50`, // com pipe a saída não é só a listagem
		`grep -rn 'x' src`,
		``,
	}
	for _, cmd := range nao {
		if got := parseListingKind(cmd); got != listingNone {
			t.Errorf("%q: não é listagem, veio %v", cmd, got)
		}
	}
}

// linhasDeCaminho monta a saída crua de um 'dir /s /b': n arquivos por pasta, em pastas diferentes.
func linhasDeCaminho(dirs []string, n int, ext string) string {
	var b strings.Builder
	for _, d := range dirs {
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, `src\%s\Arquivo%d%s`+"\n", d, i, ext)
		}
	}
	return b.String()
}

// TestRegroupListingAgrupaSemPerderArquivo: o critério do passo B2 — a lista sai agrupada por diretório com a
// contagem, e TODO arquivo continua presente (nada é resumido nem cortado).
func TestRegroupListingAgrupaSemPerderArquivo(t *testing.T) {
	raw := linhasDeCaminho([]string{"Core", "Sinks", "Parsing"}, 40, ".cs")
	out, note, ok := regroupListing(`dir /s /b src\*.cs`, raw)
	if !ok {
		t.Fatalf("a listagem deveria ser reagrupada")
	}
	if note != listingGroupNote {
		t.Errorf("rodapé inesperado: %q", note)
	}
	linhas := strings.Split(out, "\n")
	if len(linhas) >= 80 {
		t.Errorf("reagrupamento não encurtou: %d linhas", len(linhas))
	}
	for _, d := range []string{"Core", "Sinks", "Parsing"} {
		if !strings.Contains(out, fmt.Sprintf(`src\%s\ (40):`, d)) {
			t.Errorf("faltou o grupo %s: %q", d, out)
		}
	}
	for _, d := range []string{"Core", "Sinks", "Parsing"} {
		for i := 0; i < 40; i++ {
			if !strings.Contains(out, fmt.Sprintf("Arquivo%d.cs", i)) {
				t.Fatalf("arquivo Arquivo%d.cs (%s) sumiu do agrupamento", i, d)
			}
		}
	}
	if got := strings.Count(out, ".cs"); got != 120 {
		t.Errorf("esperava os 120 nomes no texto agrupado, achei %d", got)
	}
}

// TestRegroupListingLsRecursive: o 'ls -R' traz cabeçalho de pasta ('.:', './src:') e abaixo só os nomes.
func TestRegroupListingLsRecursive(t *testing.T) {
	var b strings.Builder
	b.WriteString(".:\nleia.md\n")
	for _, d := range []string{"./a", "./a/b"} {
		fmt.Fprintf(&b, "\n%s:\n", d)
		for i := 0; i < 45; i++ {
			fmt.Fprintf(&b, "f%d.go\n", i)
		}
	}
	out, _, ok := regroupListing(`ls -R`, b.String())
	if !ok {
		t.Fatalf("'ls -R' deveria ser reagrupado")
	}
	if !strings.Contains(out, "./a/ (45):") || !strings.Contains(out, "./a/b/ (45):") {
		t.Errorf("grupos do ls -R errados: %q", out)
	}
	if !strings.Contains(out, "leia.md") {
		t.Errorf("o arquivo da raiz do ls -R sumiu: %q", out)
	}
}

// TestRegroupListingNaoMexeNaDuvida: qualquer sinal de que a saída não é caminho puro, lista curta de mais ou
// desligamento explícito fazem o texto passar como veio.
func TestRegroupListingNaoMexeNaDuvida(t *testing.T) {
	grande := linhasDeCaminho([]string{"Core", "Sinks"}, 60, ".cs")

	if _, _, ok := regroupListing(`dir /s /b src\*.cs`, linhasDeCaminho([]string{"Core"}, 10, ".cs")); ok {
		t.Errorf("lista curta (10 nomes) não deveria ser reagrupada")
	}
	// uma linha em colunas (data, tamanho) no meio: formato desconhecido, não mexe
	colunas := grande + "12/11/2026  09:13 AM           1.234 outro.cs\n"
	if _, _, ok := regroupListing(`dir /s /b src\*.cs`, colunas); ok {
		t.Errorf("saída com colunas não deveria ser reagrupada")
	}
	// comando que não é listagem reconhecida: nem tenta
	if _, _, ok := regroupListing(`grep -rn x src`, grande); ok {
		t.Errorf("comando que não é listagem não deveria ser reagrupado")
	}
	// 'ls -R' sem cabeçalho de pasta (formato inesperado): não mexe
	if _, _, ok := regroupListing(`ls -R`, strings.Repeat("f.go\n", 100)); ok {
		t.Errorf("'ls -R' sem cabeçalho não deveria ser reagrupado")
	}
	t.Setenv("FASTEXEC_NO_LISTING_GROUP", "1")
	if _, _, ok := regroupListing(`dir /s /b src\*.cs`, grande); ok {
		t.Errorf("FASTEXEC_NO_LISTING_GROUP deveria desligar o agrupamento")
	}
}

// TestRawEscapeValeParaListagem: o '--raw' também sai do comando de listagem (o rodapé do agrupamento ensina a
// voltar), mas continua sendo argumento de verdade em qualquer comando que não é leitura do FastExec.
func TestRawEscapeValeParaListagem(t *testing.T) {
	args := ExecArgs{Host: "local", Command: `dir /s /b src\*.cs --raw`}
	label, ok := stripRawEscape(&args)
	if !ok || label != `dir /s /b src\*.cs` || args.Command != label {
		t.Errorf("o --raw não saiu da listagem: %q ok=%v", label, ok)
	}
	args = ExecArgs{Host: "local", Command: `ls -R --raw`}
	if label, ok := stripRawEscape(&args); !ok || label != `ls -R` {
		t.Errorf("o --raw não saiu do 'ls -R': %q ok=%v", label, ok)
	}
	args = ExecArgs{Host: "local", Command: `meu-programa --raw entrada.txt`}
	if _, ok := stripRawEscape(&args); ok {
		t.Errorf("comando que não é leitura do FastExec não pode perder o --raw")
	}
}

// TestExecListagemRecursivaAgrupada: ponta a ponta no host local — 'dir /s /b' com mais de 80 arquivos volta
// agrupado por diretório, com o rodapé, e com o '--raw' volta linha por linha.
func TestExecListagemRecursivaAgrupada(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("'dir /s /b' é do cmd.exe")
	}
	semAmbienteUnix(t)
	root := t.TempDir()
	files := map[string]string{}
	for _, d := range []string{"Core", "Sinks", "Parsing"} {
		for i := 0; i < 40; i++ {
			files[fmt.Sprintf("src/%s/Arquivo%d.cs", d, i)] = "namespace X { class C { } }\n"
		}
	}
	writeFiles(t, root, files)

	out, err := HandleExec(ExecArgs{Host: "local", Command: `dir /s /b src\*.cs`, Cwd: root})
	if err != nil {
		t.Fatalf("exec falhou: %v (%s)", err, out)
	}
	if !strings.Contains(out, listingGroupNote) {
		t.Fatalf("faltou o rodapé do agrupamento: %s", out)
	}
	for _, d := range []string{"Core", "Sinks", "Parsing"} {
		if !strings.Contains(out, fmt.Sprintf(`%s\ (40):`, d)) {
			t.Errorf("faltou o grupo %s: %s", d, out)
		}
	}
	if strings.Count(out, "Arquivo") != 120 {
		t.Errorf("o agrupamento perdeu arquivos: %s", out)
	}

	raw, err := HandleExec(ExecArgs{Host: "local", Command: `dir /s /b src\*.cs --raw`, Cwd: root})
	if err != nil {
		t.Fatalf("exec --raw falhou: %v (%s)", err, raw)
	}
	if !strings.Contains(raw, "raw listing as asked") {
		t.Errorf("faltou a nota do --raw: %s", raw)
	}
	// linha por linha: o caminho completo de cada arquivo aparece (o agrupamento só imprime o nome), e nenhum
	// cabeçalho de grupo. O teto de saída do exec (spill) pode cortar o miolo da lista; o que o --raw garante é
	// que o FastExec não reagrupa.
	if !strings.Contains(raw, `src\Core\Arquivo0.cs`) {
		t.Errorf("o --raw deveria devolver os caminhos linha por linha: %s", raw)
	}
	if got, agrupado := nonEmptyLines(raw), nonEmptyLines(out); got <= agrupado {
		t.Errorf("o --raw deveria ser mais longo que o agrupado (%d x %d): %s", got, agrupado, raw)
	}
	if strings.Contains(raw, `Core\ (40):`) {
		t.Errorf("o --raw não pode agrupar: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "Core", "Arquivo0.cs")); err != nil {
		t.Fatalf("o teste não montou a árvore esperada: %v", err)
	}
}

// TestRegroupListingOmiteRuido: num repositório o interior de '.git', 'node_modules' e afins é a maior parte das
// linhas e não tem código; o agrupamento os deixa de fora e o rodapé diz quantos itens saíram e de quais pastas.
func TestRegroupListingOmiteRuido(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 90; i++ {
		fmt.Fprintf(&b, `src\Core\Arquivo%d.cs`+"\n", i)
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, `W:\repo\.git\objects\a%d`+"\n", i)
	}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, `W:\repo\node_modules\pkg%d\index.js`+"\n", i)
	}
	out, note, ok := regroupListing(`dir /s /b`, b.String())
	if !ok {
		t.Fatalf("a listagem deveria ser reagrupada")
	}
	if !strings.Contains(note, "210 item(s) omitted in .git, node_modules") {
		t.Errorf("rodapé sem a contagem do que foi omitido: %q", note)
	}
	if !strings.Contains(note, "--raw returns everything") {
		t.Errorf("o rodapé deveria ensinar o caminho de volta: %q", note)
	}
	if strings.Contains(out, ".git") || strings.Contains(out, "node_modules") {
		t.Errorf("o interior das pastas de ruído apareceu no agrupamento: %q", out)
	}
	if !strings.Contains(out, `src\Core\ (90):`) {
		t.Errorf("o grupo de código sumiu: %q", out)
	}
	if got := strings.Count(out, "Arquivo"); got != 90 {
		t.Errorf("o agrupamento perdeu arquivos de código: achei %d de 90", got)
	}
	// o --raw e o FASTEXEC_NO_LISTING_GROUP continuam devolvendo tudo
	t.Setenv("FASTEXEC_NO_LISTING_GROUP", "1")
	if _, _, ok := regroupListing(`dir /s /b`, b.String()); ok {
		t.Errorf("FASTEXEC_NO_LISTING_GROUP deveria devolver a saída completa")
	}
}

// TestRegroupListingRaizRuidosaNaoOmite: pedir a listagem DENTRO de uma pasta de ruído é pedir justamente aquele
// interior ('dir /s /b node_modules'), então ali nada é omitido.
func TestRegroupListingRaizRuidosaNaoOmite(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, `node_modules\pkg\f%d.js`+"\n", i)
	}
	for _, cmd := range []string{`dir /s /b node_modules`, `dir /s /b node_modules\*.js`, `find node_modules -name '*.js'`} {
		out, note, ok := regroupListing(cmd, b.String())
		if !ok {
			t.Fatalf("%q deveria ser reagrupado", cmd)
		}
		if note != listingGroupNote {
			t.Errorf("%q: nada deveria ser omitido, rodapé veio %q", cmd, note)
		}
		if !strings.Contains(out, "f0.js") || !strings.Contains(out, "f99.js") {
			t.Errorf("%q: a lista pedida foi perdida: %q", cmd, out)
		}
	}
}

// TestRegroupListingLsRecursiveOmiteRuido: no 'ls -R' quem diz a pasta é o cabeçalho; o bloco inteiro de uma pasta de
// ruído sai da resposta e é contado no rodapé.
func TestRegroupListingLsRecursiveOmiteRuido(t *testing.T) {
	var b strings.Builder
	b.WriteString(".:\nleia.md\n")
	fmt.Fprintf(&b, "\n%s:\n", "./src")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&b, "f%d.go\n", i)
	}
	fmt.Fprintf(&b, "\n%s:\n", "./node_modules/dep")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "dep%d.js\n", i)
	}
	out, note, ok := regroupListing(`ls -R`, b.String())
	if !ok {
		t.Fatalf("'ls -R' deveria ser reagrupado")
	}
	if !strings.Contains(note, "60 item(s) omitted in node_modules") {
		t.Errorf("rodapé sem a contagem do bloco omitido: %q", note)
	}
	if strings.Contains(out, "dep0.js") {
		t.Errorf("o bloco de node_modules apareceu: %q", out)
	}
	if !strings.Contains(out, "./src/ (45):") || !strings.Contains(out, "leia.md") {
		t.Errorf("o resto da listagem se perdeu: %q", out)
	}
}

// TestRegroupListingSoRuidoDevolveTudo: se a listagem inteira está dentro de pastas de ruído, omitir deixaria a
// resposta vazia — melhor devolver as linhas do que uma resposta sem nada.
func TestRegroupListingSoRuidoDevolveTudo(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, `W:\repo\.git\objects\a%d`+"\n", i)
	}
	if _, _, ok := regroupListing(`dir /s /b`, b.String()); ok {
		t.Errorf("listagem só de ruído deveria passar intacta")
	}
}

// TestDetectListCommandRecursivaVaiParaOShell: 'ls -R' e 'dir /s' NÃO podem ser respondidos pelo file_ops 'list'
// (que lê um diretório só) — responder a pasta de cima como se fosse a árvore seria pior que rodar o comando.
func TestDetectListCommandRecursivaVaiParaOShell(t *testing.T) {
	for _, cmd := range []string{`ls -R`, `ls -laR src`, `ls --recursive .`, `dir /s /b src`, `dir /s src`} {
		if _, p := detectListCommand(ExecArgs{Host: "local", Command: cmd}); p != "" {
			t.Errorf("%q deveria seguir para o shell, veio caminho %q", cmd, p)
		}
	}
	if _, p := detectListCommand(ExecArgs{Host: "local", Command: `ls src`}); p != "src" {
		t.Errorf("'ls src' continua sendo listagem simples, veio %q", p)
	}
}
