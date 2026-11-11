package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Símbolo pedido no arquivo errado (o chamador): o outline aponta onde ele é declarado, no mesmo repo.
func TestOutlineSymbolElsewhereInRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	caller := filepath.Join(root, "svc", "caller.go")
	decl := filepath.Join(root, "calc", "calc.go")
	for p, src := range map[string]string{
		caller: "package svc\n\nfunc Run() { ObterFee() }\n",
		decl:   "package calc\n\nfunc ObterFee() int {\n\treturn 1\n}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: caller, Symbol: "ObterFee"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "calc/calc.go:L3") || !strings.Contains(out, "declared elsewhere") {
		t.Errorf("sem apontar a declaração: %q", out)
	}

	// Não existe em lugar nenhum: mantém a mensagem original, sem ruído.
	out, _ = HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: caller, Symbol: "Inexistente"})
	if out != `(no declaration matching "Inexistente")` {
		t.Errorf("mensagem alterada: %q", out)
	}
}
