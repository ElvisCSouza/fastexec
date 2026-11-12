// lsp_bridge_test.go: os dois lados da ponte. O lado que NÃO depende de nada instalado (as regras de âncora, de
// nota e de silêncio, que valem em qualquer máquina) e o lado que precisa de um servidor de verdade — o gopls — para
// provar que a divisão de trabalho funciona ponta a ponta: o índice dá a POSIÇÃO da declaração, o servidor dá o
// CONJUNTO de referências, o índice volta a dar o rótulo de cada linha.
//
// O teste de integração pula (nunca falha) quando o gopls não está instalado: servidor de linguagem é programa
// opcional, e a ausência dele não é um checkout quebrado.
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

// ---------------------------------------------------------------------------
// Sem servidor nenhum: âncora, nota e silêncio
// ---------------------------------------------------------------------------

// TestNoAnchorIsSilent prova a regra que mantém o FastExec de antes intacto: quando não há âncora, a resposta por
// nome É a resposta, sem nenhuma linha a mais.
func TestNoAnchorIsSilent(t *testing.T) {
	byName := func() (string, error) {
		return "=== callers of 'Fetch' (by name) ===\nTotal: 1 references in 1 files.\n", nil
	}
	exact := func() (string, error) { return "", errNoAnchor }

	out, err := courseWithGraph("", "Fetch", exact, byName)
	if err != nil {
		t.Fatalf("courseWithGraph: %v", err)
	}
	if strings.Contains(out, "lsp") {
		t.Fatalf("resposta por nome ganhou nota de LSP sem haver âncora:\n%s", out)
	}
	if !strings.HasPrefix(out, "=== callers of 'Fetch' (by name) ===") {
		t.Fatalf("resposta por nome alterada:\n%s", out)
	}
}

// TestDegradationNote é o outro lado: havia servidor para usar e ele não respondeu — então a linha honesta sai, com
// o motivo, e a resposta por nome continua sendo entregue inteira.
func TestDegradationNote(t *testing.T) {
	byName := func() (string, error) { return "=== callers of 'Fetch' (by name) ===\n", nil }
	exact := func() (string, error) { return "", lsp.Unavailable("server not installed") }

	out, err := courseWithGraph("", "Fetch", exact, byName)
	if err != nil {
		t.Fatalf("courseWithGraph: %v", err)
	}
	if !strings.Contains(out, "lsp unavailable (server not installed); by name") {
		t.Fatalf("nota de degradação ausente:\n%s", out)
	}
	if !strings.HasPrefix(out, "=== callers of 'Fetch' (by name) ===") {
		t.Fatalf("resposta por nome perdida na degradação:\n%s", out)
	}
}

// TestExactWinsWhenItAnswers: quando o caminho exato fecha, é ele que sai — e sem nota nenhuma.
func TestExactWinsWhenItAnswers(t *testing.T) {
	exact := func() (string, error) { return "=== callers of 'Fetch' (exact, gopls) ===\n", nil }
	byName := func() (string, error) { return "=== callers of 'Fetch' (by name) ===\n", nil }

	out, err := courseWithGraph("", "Fetch", exact, byName)
	if err != nil {
		t.Fatalf("courseWithGraph: %v", err)
	}
	if !strings.Contains(out, "(exact, gopls)") || strings.Contains(out, "by name") {
		t.Fatalf("caminho exato não venceu:\n%s", out)
	}
}

// TestNoAnchorCases são os casos em que o servidor NÃO tem o que responder: a resposta por nome não é degradação,
// é o desenho. Nenhum deles pode iniciar servidor nem varrer o índice.
func TestNoAnchorCases(t *testing.T) {
	root := t.TempDir()
	if _, err := lspCallers(root, "Fetch", 0, 2); !errors.Is(err, errNoAnchor) {
		t.Fatalf("callers com depth>1 deveria ser errNoAnchor, veio %v", err)
	}
	if _, err := lspImpact(root, filepath.Join("internal", "a.go"), 0); !errors.Is(err, errNoAnchor) {
		t.Fatalf("impact de arquivo deveria ser errNoAnchor, veio %v", err)
	}
	if _, err := lspImpact(root, "internal/a.go", 0); !errors.Is(err, errNoAnchor) {
		t.Fatalf("impact de arquivo deveria ser errNoAnchor, veio %v", err)
	}
}

// TestDisabledIsNotDegradation: FASTEXEC_LSP=off é escolha do usuário. A resposta por nome sai sem nota e sem nem
// tocar no índice — é o que mantém o modo desligado idêntico ao FastExec de antes do LSP.
func TestDisabledIsNotDegradation(t *testing.T) {
	t.Setenv("FASTEXEC_LSP", "off")
	if !lsp.Disabled() {
		t.Skip("FASTEXEC_LSP=off não foi respeitado pelo ambiente")
	}
	root := t.TempDir()
	// Repositório de verdade (marca vazia basta): sem isto o índice recusaria a raiz por não ser git, e
	// "não criou o índice" seria verdade por acidente em vez de por causa do FASTEXEC_LSP=off.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marcar repositório: %v", err)
	}
	writeGo(t, root, "a.go", "package demo\n\nfunc Fetch() {}\n")
	// Banco próprio para este teste: dá para afirmar que ele NÃO foi criado.
	db := filepath.Join(t.TempDir(), "graph.db")
	graph.SetDBPath(db)
	t.Cleanup(func() { graph.SetDBPath("") })

	if _, err := lspCallers(root, "Fetch", 0, 1); !errors.Is(err, errNoAnchor) {
		t.Fatalf("com LSP desligado, callers deveria ser errNoAnchor, veio %v", err)
	}
	// Nem o índice foi criado: "sem nem tocar no índice" é parte do contrato, não um detalhe de desempenho.
	if _, err := os.Stat(db); err == nil {
		t.Fatalf("LSP desligado criou o índice em %s", db)
	}
}

// ---------------------------------------------------------------------------
// Com servidor de verdade: gopls
// ---------------------------------------------------------------------------

// Fixture: dois tipos com um método de MESMO nome em arquivos diferentes, e os dois chamados. Um índice por nome
// não sabe separá-los; ancorar em cada declaração e unir as duas respostas do servidor sabe.
const (
	bridgeAlpha = `package demo

type Alpha struct{}

func (a Alpha) Do() int { return 1 }
`
	bridgeBeta = `package demo

type Beta struct{}

func (b Beta) Do() int { return 2 }
`
	bridgeCaller = `package demo

func CallAlpha(a Alpha) int { return a.Do() }

func CallBeta(b Beta) int { return b.Do() }
`
)

func writeGo(t *testing.T, root, name, src string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// realGoplsBridge escreve a fixture e pula o teste quando o gopls não está instalado ou o LSP está desligado.
func realGoplsBridge(t *testing.T) string {
	t.Helper()
	t.Setenv("FASTEXEC_LSP", "on")
	lang, ok := lsp.LanguageByID("go")
	if !ok {
		t.Skip("Go não está na tabela de linguagens")
	}
	root := t.TempDir()
	// O índice só age dentro de um repositório git (é a trava do code_graph, não uma escolha da ponte), e é
	// dele que vem a POSIÇÃO da declaração. A marca vazia basta: o índice cai na varredura sem git.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marcar repositório: %v", err)
	}
	writeGo(t, root, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	writeGo(t, root, "alpha.go", bridgeAlpha)
	writeGo(t, root, "beta.go", bridgeBeta)
	writeGo(t, root, "caller.go", bridgeCaller)
	if _, _, ok := lsp.Find(lang); !ok {
		t.Skipf("%s not installed: %s", lang.Server, lang.Install)
	}
	// No Windows o servidor quente tem este diretório como cwd, e o t.TempDir não consegue apagá-lo por baixo
	// dele. Encerrar a raiz é o mesmo que o daemon faz quando o repositório vai embora — e é o que devolve o
	// processo antes da limpeza (t.Cleanup é LIFO: este roda antes do TempDir).
	t.Cleanup(func() { lsp.Manager().StopRoot(root) })
	return root
}

// TestBridgeCallersExact é a prova ponta a ponta da ponte: índice -> posição da declaração -> gopls -> conjunto de
// referências -> rótulo do índice. As duas declarações de 'Do' rendem duas âncoras, e a união das respostas são os
// dois pontos de chamada.
func TestBridgeCallersExact(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsBridge(t)

	out, err := lspCallers(root, "Do", 50, 1)
	if err != nil {
		t.Fatalf("lspCallers: %v", err)
	}
	if !strings.Contains(out, "=== callers of 'Do' (exact, gopls) ===") {
		t.Fatalf("cabeçalho exato ausente:\n%s", out)
	}
	for _, want := range []string{"caller.go:3", "caller.go:5", "Total: 2 references in 1 files."} {
		if !strings.Contains(out, want) {
			t.Fatalf("faltou %q em:\n%s", want, out)
		}
	}
	// Duas declarações com o mesmo nome: no modo exato isso deixa de ser ambiguidade, e a resposta diz por quê.
	if !strings.Contains(out, "declared in 2 places") {
		t.Fatalf("nota de homônimo ausente:\n%s", out)
	}
	// Nada de nota de degradação quando o servidor respondeu.
	if strings.Contains(out, "lsp unavailable") {
		t.Fatalf("degradação numa resposta exata:\n%s", out)
	}
}

// TestBridgeDefinitionExact: o qualificador de tipo chega ao servidor pela âncora certa — 'Alpha.Do' responde a
// declaração de Alpha, e não a de Beta.
func TestBridgeDefinitionExact(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsBridge(t)

	out, err := lspDefinition(root, "Alpha.Do")
	if err != nil {
		t.Fatalf("lspDefinition: %v", err)
	}
	if !strings.Contains(out, "=== definition of 'Do' (exact, gopls) ===") {
		t.Fatalf("cabeçalho exato ausente:\n%s", out)
	}
	if !strings.Contains(out, "alpha.go:5") {
		t.Fatalf("declaração de Alpha ausente:\n%s", out)
	}
	if strings.Contains(out, "beta.go") {
		t.Fatalf("Alpha.Do respondeu a declaração de Beta:\n%s", out)
	}
}

// TestBridgeEditFooterExact: o rodapé de edição usa o mesmo caminho, mas SEM iniciar servidor. Com um servidor
// quente ele responde exato; sem servidor quente ele degrada na hora, e quem chama volta para a linha por nome.
func TestBridgeEditFooterExact(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsBridge(t)

	cold, _ := filepath.Abs(filepath.Join(root, "alpha.go"))
	line := 5 // a linha da declaração em bridgeAlpha
	col := graph.NameCol(strings.Split(bridgeAlpha, "\n")[line-1], "Do")

	// Sem nada quente: falha rápida, nunca espera por um servidor que não existe.
	ctx, cancel := context.WithTimeout(context.Background(), editImpactLSPBudget)
	defer cancel()
	if _, _, err := warmUses(ctx, root, cold, "Do", line, col); err == nil {
		t.Fatalf("warmUses respondeu sem servidor quente")
	}

	// Esquenta pelo caminho normal e repete: agora a resposta vem do servidor, do documento em disco.
	if _, err := lspCallers(root, "Do", 50, 1); err != nil {
		t.Fatalf("lspCallers (aquecimento): %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), editImpactLSPBudget)
	defer cancel2()
	sites, server, err := warmUses(ctx2, root, cold, "Do", line, col)
	if err != nil {
		t.Fatalf("warmUses com servidor quente: %v", err)
	}
	if server != "gopls" {
		t.Fatalf("servidor inesperado: %q", server)
	}
	if len(sites) == 0 {
		t.Fatalf("nenhuma referência exata com servidor quente")
	}
	for _, s := range sites {
		if s.File == "alpha.go" && s.Line == 5 {
			t.Fatalf("a própria declaração voltou como uso: %+v", s)
		}
	}
}
