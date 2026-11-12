package lsp

import (
	"context"
	"strings"
	"testing"
)

const membersSrc = `package demo

type Reader interface {
	Read(p []byte) (int, error)
}

type Base struct {
	ID int
}

type Order struct {
	Base
	Total int
}

func (o Order) Sum() int { return o.Total }

func Free() {}
`

// TestDocumentSymbolsTreeKeepsMembersUnderTheirType: os membros de um tipo só podem ser listados se a
// árvore do servidor chegar inteira. Achatar a resposta aqui é perder justamente a informação que separa
// "membro de Order" de "função ao lado de Order" — que é o que code_graph def de um TIPO precisa mostrar.
func TestDocumentSymbolsTreeKeepsMembersUnderTheirType(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", membersSrc)
	c := fakeClient(t, root)

	tree, err := c.DocumentSymbolsTree(context.Background(), path)
	if err != nil {
		t.Fatalf("DocumentSymbolsTree: %v", err)
	}
	byName := map[string]DocumentSymbol{}
	for _, s := range tree {
		byName[s.Name] = s
	}
	for _, name := range []string{"Reader", "Base", "Order"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("a árvore não tem %q (tem %v)", name, names(tree))
		}
	}
	order := byName["Order"]
	var kids []string
	for _, ch := range order.Children {
		kids = append(kids, ch.Name)
	}
	if !contains(kids, "Base") || !contains(kids, "Total") || !contains(kids, "Sum") {
		t.Errorf("membros de Order = %v, quer Base, Total e Sum", kids)
	}
	// A função livre é irmã dos tipos, não filha de nenhum: se ela aparecesse como membro, o def do tipo
	// listaria o arquivo inteiro como se fosse o tipo.
	for _, ch := range order.Children {
		if ch.Name == "Free" {
			t.Errorf("Free apareceu como membro de Order")
		}
	}
	// Achatada, a mesma resposta traz cada símbolo UMA vez (o filho não pode sair duas vezes).
	flat, err := c.DocumentSymbols(context.Background(), path)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	seen := map[string]int{}
	for _, s := range flat {
		seen[s.Name]++
	}
	if seen["Total"] != 1 || seen["Sum"] != 1 {
		t.Errorf("achatado: %v, quer cada membro uma vez", seen)
	}
}

// TestSupertypesResolvesEmbeddedBase: "inherits" só é honesto quando o servidor resolve o tipo base. A
// linha do arquivo diz o nome; quem diz que ele é o tipo declarado (e não um campo homônimo) é o servidor.
func TestSupertypesResolvesEmbeddedBase(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", membersSrc)
	c := fakeClient(t, root)

	base, err := c.Supertypes(context.Background(), path, posOf(t, membersSrc, "type Order"))
	if err != nil {
		t.Fatalf("Supertypes: %v", err)
	}
	if len(base) != 1 || base[0].Name != "Base" {
		t.Fatalf("supertypes de Order = %+v, quer [Base]", base)
	}
	if base[0].URI != PathToURI(path) {
		t.Errorf("URI = %q, quer %q", base[0].URI, PathToURI(path))
	}

	// Sem base embutida, a resposta é vazia — e vazia não é erro: é "não herda de nada".
	none, err := c.Supertypes(context.Background(), path, posOf(t, membersSrc, "type Base"))
	if err != nil {
		t.Fatalf("Supertypes(Base): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("supertypes de Base = %+v, quer nenhum", none)
	}

	// Posição que não é um tipo: nil, sem erro. Um servidor não inventa hierarquia para uma função.
	at, err := c.Supertypes(context.Background(), path, posOf(t, membersSrc, "func Free"))
	if err != nil {
		t.Fatalf("Supertypes(Free): %v", err)
	}
	if len(at) != 0 {
		t.Errorf("supertypes de Free = %+v, quer nenhum", at)
	}
}

// TestSupertypesUnsupportedIsNotAnError: servidor sem hierarquia de tipos degrada, não falha.
func TestSupertypesUnsupportedIsNotAnError(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", membersSrc)
	c := fakeClient(t, root, "FAKELSP_NO_HIERARCHY=1")

	if _, err := c.Supertypes(context.Background(), path, Position{}); err == nil {
		t.Fatal("sem a capacidade, quer ErrUnsupported")
	}
}

func names(nodes []DocumentSymbol) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}


// TestNestReceiverMethodsPutsLooseMethodsUnderTheirType: o gopls não aninha método nenhum — ele lista
// `func (o Order) Read()` na raiz do arquivo como "(Order).Read". Quem pergunta pelos membros de Order
// espera ver Read ali, e sem esta recolocação a resposta sairia só com os campos.
func TestNestReceiverMethodsPutsLooseMethodsUnderTheirType(t *testing.T) {
	nodes := []DocumentSymbol{
		{Name: "Order", Kind: 23, Children: []DocumentSymbol{{Name: "Total", Kind: 8}}},
		{Name: "(Order).Read", Kind: 6},
		{Name: "(Box[T]).Get", Kind: 6},
		{Name: "Box", Kind: 23},
		{Name: "Free", Kind: 12},
	}
	got := nestReceiverMethods(nodes)
	if len(got) != 3 {
		t.Fatalf("raiz = %v, quer Order, Box e Free", names(got))
	}
	if n := names(got[0].Children); strings.Join(n, ",") != "Total,Read" {
		t.Fatalf("membros de Order = %v, quer [Total Read]", n)
	}
	// O nome sai sem o receptor: quem já está sob o tipo não precisa repetir de quem é.
	if n := names(got[1].Children); strings.Join(n, ",") != "Get" {
		t.Fatalf("membros de Box = %v, quer [Get]", n)
	}
	if got[2].Name != "Free" {
		t.Fatalf("função solta virou %q", got[2].Name)
	}
}

// TestNestReceiverMethodsKeepsMethodsWithoutTheirType: sem o tipo no arquivo não há onde pendurar o método,
// e inventar um dono seria inventar hierarquia.
func TestNestReceiverMethodsKeepsMethodsWithoutTheirType(t *testing.T) {
	got := nestReceiverMethods([]DocumentSymbol{{Name: "(Ausente).Faca", Kind: 6}})
	if len(got) != 1 || got[0].Name != "(Ausente).Faca" {
		t.Fatalf("método órfão = %v, quer ele mesmo", names(got))
	}
}
