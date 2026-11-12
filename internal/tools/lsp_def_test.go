// lsp_def_test.go: as relações e os membros que o servidor sabe de um símbolo.
//
// O que se prova aqui é o que o texto NÃO diz: em Go um tipo satisfaz uma interface por estrutura, e não há
// uma linha no arquivo que ligue os dois. Só o servidor sabe responder "quem implementa isto" e "o que este
// tipo tem dentro" — e é isso que evita o modelo abrir mais dois arquivos para descobrir.
//
// A fixture é Go real analisado pelo gopls de verdade: com um servidor falso, o teste provaria apenas que o
// falso concorda consigo mesmo.
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/lsp"
)

const relFixture = `package demo

type Reader interface {
	Read() int
}

type Base struct {
	ID int
}

type Order struct {
	Base
	Total int
}

func (o Order) Read() int { return o.Total }

func New() Reader { return Order{} }

func Use(r Reader) int { return r.Read() }
`

// realGoplsRelations escreve a fixture de relações e sobe o gopls de verdade, pulando o teste quando ele não
// está instalado ou o LSP está desligado.
func realGoplsRelations(t *testing.T) string {
	t.Helper()
	t.Setenv("FASTEXEC_LSP", "on")
	lang, ok := lsp.LanguageByID("go")
	if !ok {
		t.Skip("Go não está na tabela de linguagens")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marcar repositório: %v", err)
	}
	writeGo(t, root, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	writeGo(t, root, "order.go", relFixture)
	if _, _, ok := lsp.Find(lang); !ok {
		t.Skipf("%s not installed: %s", lang.Primary().Name, lang.Primary().Install)
	}
	t.Cleanup(func() { lsp.Manager().StopRoot(root) })
	return root
}

// TestDefOfInterfaceListsImplementorsAndMembers: em uma interface, a pergunta útil é o INVERSO do texto —
// quem a cumpre. Em Go isso não está escrito em lugar nenhum; sem esta linha o modelo teria de varrer o
// repositório procurando tipos com o método certo.
func TestDefOfInterfaceListsImplementorsAndMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsRelations(t)

	out, err := lspDefinition(root, "Reader")
	if err != nil {
		t.Fatalf("lspDefinition(Reader): %v", err)
	}
	if !strings.Contains(out, "implemented by: Order") {
		t.Errorf("o def da interface não diz quem a implementa:\n%s", out)
	}
	if !strings.Contains(out, "members (1):") || !strings.Contains(out, "Read (method)") {
		t.Errorf("o def da interface não lista o próprio contrato:\n%s", out)
	}
}

// TestDefOfTypeListsWhatItImplementsAndHas: um tipo concreto responde as duas perguntas de uma vez — de que
// contrato ele é implementação (relação que o texto não escreve) e o que ele tem dentro (o que o modelo
// descobriria abrindo o arquivo do tipo).
func TestDefOfTypeListsWhatItImplementsAndHas(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsRelations(t)

	out, err := lspDefinition(root, "Order")
	if err != nil {
		t.Fatalf("lspDefinition(Order): %v", err)
	}
	if !strings.Contains(out, "implements: Reader") {
		t.Errorf("o def do tipo não diz que ele implementa a interface:\n%s", out)
	}
	if !strings.Contains(out, "members (3):") {
		t.Errorf("o def do tipo não lista os membros:\n%s", out)
	}
	// Base embutida, campo e método: os três são membros de Order, e cada um em UMA linha, sem corpo.
	for _, want := range []string{"Base", "Total", "Read (method)"} {
		if !strings.Contains(out, want) {
			t.Errorf("o membro %q não saiu no def do tipo:\n%s", want, out)
		}
	}
	// A assinatura de um TIPO não é o corpo dele: o hover do gopls devolve o struct inteiro, e despejar isso
	// no def não é assinatura.
	if strings.Contains(out, "{") {
		t.Errorf("o def do tipo despejou um corpo:\n%s", out)
	}
}

// TestDefOfMethodSaysWhatItImplements: um método é a implementação de um contrato (o da interface em Go, a
// base em C#). É essa a relação que muda a leitura de um def de método.
func TestDefOfMethodSaysWhatItImplements(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsRelations(t)

	out, err := lspDefinition(root, "Read")
	if err != nil {
		t.Fatalf("lspDefinition(Read): %v", err)
	}
	if !strings.Contains(out, "implements: ") {
		t.Errorf("o def do método não diz de que contrato ele é implementação:\n%s", out)
	}
}

// TestCallersOfInterfaceListImplementors: a lista de referências de uma interface não mostra quem a cumpre
// (não há referência ao nome dela no tipo que a satisfaz). Sem a linha de relação, "callers de Reader"
// responderia "só o parâmetro de Use" — que é verdade e é inútil para quem vai mudar o contrato.
func TestCallersOfInterfaceListImplementors(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsRelations(t)

	out, err := lspCallers(root, "Reader", 50, 1)
	if err != nil {
		t.Fatalf("lspCallers(Reader): %v", err)
	}
	if !strings.Contains(out, "implemented by: Order") {
		t.Errorf("os callers da interface não listam quem a implementa:\n%s", out)
	}
	// A relação vem antes da lista: quem vai mudar o contrato precisa dela primeiro.
	if i, j := strings.Index(out, "implemented by"), strings.Index(out, "- "); i > j && j >= 0 {
		t.Errorf("a relação saiu depois das referências:\n%s", out)
	}
}

// TestTypeLineEndToEndWithRealGopls fecha o caminho inteiro do item mais caro da diretriz: um trecho lido
// com `x := ...` sem tipo escrito, e o servidor QUENTE dizendo o tipo. É o que evita abrir outro arquivo só
// para descobrir o que é `x`.
func TestTypeLineEndToEndWithRealGopls(t *testing.T) {
	if testing.Short() {
		t.Skip("integração com gopls real")
	}
	root := realGoplsRelations(t)

	const extra = `package demo

func Exemplo() int {
	pedido := New()
	return pedido.Read()
}
`
	path := writeGo(t, root, "exemplo.go", extra)
	// Esquentar o servidor pelo caminho normal (uma tool de grafo): ler arquivo nunca inicia gopls.
	if _, err := lspCallers(root, "Exemplo", 50, 1); err != nil {
		t.Fatalf("esquentar o servidor: %v", err)
	}
	// O trecho lido é a função inteira: a declaração `pedido := New()` sem tipo escrito e o uso dela.
	out := typeLine(path, []byte(extra), spanLines(3, 6))
	if !strings.Contains(out, "pedido: ") {
		t.Fatalf("a linha de tipos não trouxe o tipo de pedido: %q", out)
	}
	if !strings.Contains(out, "Reader") {
		t.Fatalf("o tipo de pedido não é o que o servidor resolveu: %q", out)
	}
}

// TestDefEndLineUsesTheSymbolTree: o fim da faixa de uma declaração vem da árvore de símbolos do servidor, e
// do nó MAIS INTERNO que cobre a âncora — a pergunta é onde o membro termina, não onde o tipo que o contém
// termina. Sem árvore (servidor que não responde documentSymbol) devolve 0, e quem chamou fica com a âncora
// do índice: a resposta de linha única de antes, que é o piso, não o alvo.
func TestDefEndLineUsesTheSymbolTree(t *testing.T) {
	tree := []lsp.DocumentSymbol{{
		Name:      "Order",
		Kind:      23,
		Range:     lsp.Range{Start: lsp.Position{Line: 2}, End: lsp.Position{Line: 9}},
		Selection: lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 10}},
		Children: []lsp.DocumentSymbol{{
			Name:      "Read",
			Kind:      6,
			Range:     lsp.Range{Start: lsp.Position{Line: 4}, End: lsp.Position{Line: 7}},
			Selection: lsp.Range{Start: lsp.Position{Line: 4, Character: 15}, End: lsp.Position{Line: 4, Character: 19}},
		}},
	}}
	cases := []struct {
		name string
		pos  lsp.Position
		want int
	}{
		{"nome do método", lsp.Position{Line: 4, Character: 16}, 8},
		{"corpo do método", lsp.Position{Line: 6, Character: 2}, 8},
		{"nome do tipo", lsp.Position{Line: 2, Character: 6}, 10},
		{"faixa do tipo, fora do nome", lsp.Position{Line: 3, Character: 0}, 10},
		{"fora de tudo", lsp.Position{Line: 20, Character: 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defEndLine(tree, tc.pos); got != tc.want {
				t.Fatalf("defEndLine(%v) = %d, quer %d", tc.pos, got, tc.want)
			}
		})
	}
	if got := defEndLine(nil, lsp.Position{Line: 4, Character: 16}); got != 0 {
		t.Fatalf("sem árvore o fim saiu %d, quer 0 (a âncora do índice)", got)
	}
}
