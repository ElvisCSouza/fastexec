// lsp_types_test.go: a linha "types:". O que se prova aqui é o CONTRATO dela, não o gopls: quais
// declarações viram candidatas (só as que NÃO escrevem o tipo), e — o mais importante — o silêncio.
//
// A linha só existe quando o servidor responde. Sem servidor, desligada, ou fora do orçamento, a resposta
// tem de sair byte a byte como saía antes desta feature; por isso metade dos casos abaixo espera "".
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"homelab/fastexec/internal/lsp"
)

// TestInferCandidatesOnlyUndeclaredTypes: o candidato é a declaração que deixou o tipo por conta do
// compilador. Uma declaração que ESCREVE o tipo não é candidata — pedir o tipo dela ao servidor seria
// gastar uma ida ao servidor para repetir o que o texto já diz.
func TestInferCandidatesOnlyUndeclaredTypes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"go short decl", "\tx := service.Load()", []string{"x"}},
		{"go multi", "\ta, b := f()", []string{"a", "b"}},
		{"go range", "\tfor i, v := range xs {", []string{"i", "v"}},
		{"go var", "\tvar total = 0", []string{"total"}},
		{"go const", "\tconst pi = 3.14", []string{"pi"}},
		{"go var com tipo escrito", "\tvar n int = 0", nil},
		{"csharp var", "\t\tvar pedido = new Pedido();", []string{"pedido"}},
		{"csharp foreach", "\tforeach (var item in list)", []string{"item"}},
		{"ts const", "\tconst total = compute();", []string{"total"}},
		{"ts com anotação", "\tconst total: number = 1;", nil},
		{"rust let mut", "\tlet mut acc = 0;", []string{"acc"}},
		{"java var", "\tvar v = 1;", []string{"v"}},
		{"lambda", "\tlist.map((a, b) => a + b)", []string{"a", "b"}},
		{"comentário não declara", "\t// x := 1", nil},
		{"chamada não declara", "\treturn compute(a)", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := []string{"package demo", "", "func F() {", tc.line, "}"}
			got := inferCandidates(file, []int{4})
			var names []string
			for _, c := range got {
				names = append(names, c.name)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("candidatos de %q = %v, quer %v", tc.line, names, tc.want)
			}
			// A linha é absoluta no arquivo (1-based) e a coluna é UTF-16, que é como o servidor conta:
			// uma coluna em bytes anotaria o símbolo errado num arquivo com acento. Desfazer a contagem
			// (coluna -> byte) e conferir que o nome começa ali prova as duas coisas de uma vez.
			for _, c := range got {
				if c.line != 4 {
					t.Fatalf("linha do candidato %q = %d, quer 4", c.name, c.line)
				}
				if rest := tc.line[utf16ToByte(tc.line, c.startCol):]; !strings.HasPrefix(rest, c.name) {
					t.Fatalf("a coluna UTF-16 %d de %q aponta para %q", c.startCol, c.name, rest)
				}
			}
		})
	}
}

// TestInferCandidatesCountsUTF16Columns: o servidor conta colunas em unidades UTF-16, e um nome declarado
// depois de um acento fica numa coluna menor que o byte correspondente. Contar bytes aqui anotaria o
// símbolo errado — ou seja, daria o tipo de OUTRA variável.
func TestInferCandidatesCountsUTF16Columns(t *testing.T) {
	line := "\tmensagem(\"coração\"); lista.map((v) => v)"
	file := []string{"package demo", "", "func F() {", line, "}"}
	got := inferCandidates(file, []int{4})
	if len(got) != 1 || got[0].name != "v" {
		t.Fatalf("candidatos = %+v, quer [v]", got)
	}
	byteOff := strings.Index(line, "v) =>")
	if byteOff < 0 {
		t.Fatalf("fixture sem o parâmetro da lambda: %q", line)
	}
	if rest := line[utf16ToByte(line, got[0].startCol):]; !strings.HasPrefix(rest, "v") {
		t.Fatalf("a coluna %d de v aponta para %q", got[0].startCol, rest)
	}
	if got[0].startCol != utf16Len(line[:byteOff]) {
		t.Fatalf("coluna de v = %d, quer %d", got[0].startCol, utf16Len(line[:byteOff]))
	}
	if got[0].startCol == byteOff {
		t.Fatalf("coluna %d igual ao byte: o acento antes do nome não foi contado", byteOff)
	}
}

// TestInferCandidatesReportsEachDeclarationOnce: as formas de cada linguagem se sobrepõem de propósito
// (`var x = ...` é Go, C#, TS e Java). Sem desempate a mesma variável entraria quatro vezes, e a linha
// sairia repetindo "total: int, total: int" — e cada repetição custa uma pergunta ao servidor.
func TestInferCandidatesReportsEachDeclarationOnce(t *testing.T) {
	file := []string{"package demo", "", "func F() {", "\tvar total = 0", "}"}
	got := inferCandidates(file, []int{4})
	if len(got) != 1 || got[0].name != "total" {
		t.Fatalf("candidatos = %+v, quer [total] uma vez", got)
	}
}

// utf16ToByte desfaz a contagem de colunas do servidor (é o inverso de utf16Len, e existe aqui só para o
// teste poder conferir que a coluna aponta mesmo para o nome).
func utf16ToByte(s string, col int) int {
	n := 0
	for i, r := range s {
		if n >= col {
			return i
		}
		n += len(utf16.Encode([]rune{r}))
	}
	return len(s)
}
// TestTypeLineSilentWithoutServer é o contrato que mantém o FastExec de antes intacto: sem servidor não sai
// linha nenhuma (nem "não foi possível"), porque não há o que dizer sobre o tipo sem inventar.
func TestTypeLineSilentWithoutServer(t *testing.T) {
	src := []byte("package demo\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n")

	// 1. FASTEXEC_LSP=off: escolha do usuário, silêncio absoluto.
	t.Setenv("FASTEXEC_LSP", "off")
	if out := typeLine("a.go", src, spanLines(4, 4)); out != "" {
		t.Fatalf("com LSP desligado a linha saiu: %q", out)
	}

	// 2. Linguagem sem servidor configurado: não está degradada, é o esperado.
	t.Setenv("FASTEXEC_LSP", "on")
	if _, ok := lsp.LanguageForPath("notas.txt"); ok {
		t.Skip("txt ganhou servidor de linguagem; escolha outro arquivo para este caso")
	}
	if out := typeLine("notas.txt", src, spanLines(4, 4)); out != "" {
		t.Fatalf("linha saída para linguagem sem servidor: %q", out)
	}

	// 3. Servidor FRIO: ler arquivo nunca inicia gopls, então a resposta sai sem a linha — igual à de antes.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marcar repositório: %v", err)
	}
	p := writeGo(t, root, "a.go", string(src))
	if out := typeLine(p, src, spanLines(4, 4)); out != "" {
		t.Fatalf("servidor frio produziu linha: %q", out)
	}

	// 4. Sem candidato no trecho mostrado, nem se chega a perguntar.
	if out := typeLine(p, src, spanLines(1, 2)); out != "" {
		t.Fatalf("linha saída sem candidato no trecho: %q", out)
	}
	// 5. Trecho vazio.
	if out := typeLine(p, src, nil); out != "" {
		t.Fatalf("linha saída para trecho vazio: %q", out)
	}
}

// TestTypeLineForReadOnlyOnAFullRange: a linha só acompanha a leitura em que o modelo viu o texto INTEIRO
// do trecho anotado. Nos outros modos (pattern, symbol, offset, ranges) o recorte não é reconstruível aqui,
// e anotar linhas que o modelo não está vendo é pior que não anotar.
func TestTypeLineForReadOnlyOnAFullRange(t *testing.T) {
	root := t.TempDir()
	p := writeGo(t, root, "a.go", "package demo\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n")
	shown := "package demo\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n"

	off := func(args ReadFileArgs, out string) string {
		t.Helper()
		t.Setenv("FASTEXEC_LSP", "on")
		return typeLineForRead(args, out)
	}

	if got := off(ReadFileArgs{Path: p, StartLine: 1, EndLine: 5}, shown); got != "" {
		t.Fatalf("servidor frio devolveu linha: %q", got)
	}
	for _, args := range []ReadFileArgs{
		{Path: p},                                       // arquivo inteiro: o modelo pediu tudo, não uma faixa
		{Path: p, Pattern: "x"},                         // modo pattern
		{Path: p, Symbol: "F"},                          // modo symbol
		{Path: p, Offset: 10},                           // modo offset
		{Path: p, Ranges: "1-5"},                        // modo ranges
		{Path: p, StartLine: 1, EndLine: 5, Limit: 100}, // limit: o texto pode ter sido cortado
		{Path: ""},                                      // sem arquivo
	} {
		if got := off(args, shown); got != "" {
			t.Fatalf("args %+v devolveram linha: %q", args, got)
		}
	}
	// O teto de leitura cortou o fim: os candidatos saem em ordem de linha, e o corte esconde os últimos.
	if got := off(ReadFileArgs{Path: p, StartLine: 1, EndLine: 5}, shown+"\n(read cap; 5 of 40 lines)"); got != "" {
		t.Fatalf("leitura capada devolveu linha: %q", got)
	}
}

// TestCleanTypeAndHoverType: o texto do tipo vem do servidor e não é reaproveitado de qualquer jeito. Um
// hover que fala de OUTRO símbolo, ou um rótulo com quebra de linha, é descartado — tipo errado é pior que
// tipo nenhum.
func TestCleanTypeAndHoverType(t *testing.T) {
	if got := cleanType(": List<Order>"); got != "List<Order>" {
		t.Fatalf("cleanType(: List<Order>) = %q", got)
	}
	if got := cleanType("int\nlixo"); got != "" {
		t.Fatalf("cleanType com quebra de linha = %q, quer vazio", got)
	}
	if got := cleanType(strings.Repeat("x", 200)); got != "" {
		t.Fatalf("cleanType gigante = %q, quer vazio", got)
	}

	hovers := []struct {
		contents string
		name     string
		want     string
	}{
		{"```go\nvar pedido Order\n```", "pedido", "Order"},   // forma do gopls
		{"var x List<Order>", "x", "List<Order>"},             // C#
		{"var x int", "outro", ""},                            // hover de outro símbolo
		{"pedido: Order", "pedido", "Order"},                  // forma TS/Rust
		{"func F() int", "F", ""},                             // assinatura não é tipo de variável
		{"var pedido Order\n\nDocumentação longa", "pedido", "Order"}, // só a primeira linha
	}
	for _, h := range hovers {
		if got := hoverType(h.contents, h.name); got != h.want {
			t.Fatalf("hoverType(%q, %q) = %q, quer %q", h.contents, h.name, got, h.want)
		}
	}

	// O hint fica logo depois do nome; a folga de uma coluna cobre o padding de alguns servidores, e um
	// hint de outra linha não pode anotar este candidato.
	cands := []typeCand{{name: "x", line: 4, startCol: 2, endCol: 3}}
	if cd, ok := candAt(cands, lsp.Position{Line: 3, Character: 3}); !ok || cd.name != "x" {
		t.Fatalf("hint colado no nome não casou: %+v %v", cd, ok)
	}
	if _, ok := candAt(cands, lsp.Position{Line: 3, Character: 40}); ok {
		t.Fatal("hint longe do nome casou")
	}
	if _, ok := candAt(cands, lsp.Position{Line: 9, Character: 3}); ok {
		t.Fatal("hint de outra linha casou")
	}
}
