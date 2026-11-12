package lsp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const hintsSrc = `package demo

func main() {
	pedidos := []string{}
	total := 42
	nome := "ana"
	_, _ = pedidos, total
	_ = nome
}
`

// TestInlayHintsAreTyped prova a resposta que sustenta a linha "types:": o servidor diz o tipo de uma
// declaração que não escreve o tipo, e as DUAS formas de rótulo que o protocolo permite chegam iguais.
func TestInlayHintsAreTyped(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", hintsSrc)
	c := fakeClient(t, root)

	hints, err := c.InlayHints(context.Background(), path, Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: 9, Character: 0},
	})
	if err != nil {
		t.Fatalf("InlayHints: %v", err)
	}
	got := map[int]string{}
	for _, h := range hints {
		// O cliente devolve o rótulo COMO o servidor mandou (o ": " é apresentação, e quem monta a resposta
		// normaliza). O que importa aqui é que as duas formas de rótulo do protocolo cheguem iguais.
		got[h.Position.Line] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h.Label), ":"))
		if h.Kind != 1 {
			t.Errorf("linha %d: kind = %d, quer 1 (tipo)", h.Position.Line, h.Kind)
		}
	}
	// As linhas 3 e 4 saem de formas de rótulo diferentes no servidor falso (string solta e lista de partes):
	// se uma delas se perdesse, o tipo sumiria em metade dos servidores reais.
	for line, want := range map[int]string{3: "[]string", 4: "int64", 5: "string"} {
		if got[line] != want {
			t.Errorf("linha %d: hint = %q, quer %q (todos: %+v)", line, got[line], want, hints)
		}
	}
}

// TestInlayHintsUnsupportedIsNotAnError: um servidor que não conhece hints não é uma falha — é uma
// capacidade a menos, e quem pergunta precisa poder distinguir isso de um erro de verdade.
func TestInlayHintsUnsupportedIsNotAnError(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", hintsSrc)
	c := fakeClient(t, root, "FAKELSP_NO_HINTS=1")

	if _, err := c.InlayHints(context.Background(), path, Range{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("sem a capacidade, err = %v, quer ErrUnsupported", err)
	}
}

// TestInlayHintsEmptyIsNotAnError: servidor com a capacidade mas sem hint nesta faixa devolve nada, e
// nada é uma resposta legítima (não há tipo a dizer). Uma lista vazia não pode virar erro.
func TestInlayHintsEmptyIsNotAnError(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "main.go", "package demo\n\nfunc main() {}\n")
	c := fakeClient(t, root)

	hints, err := c.InlayHints(context.Background(), path, Range{})
	if err != nil {
		t.Fatalf("InlayHints: %v", err)
	}
	if len(hints) != 0 {
		t.Fatalf("hints = %+v, quer nenhum", hints)
	}
}
