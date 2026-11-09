package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchFuzzyWhitespaceSingleLine(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tval := compute( 1, 2 )\n\tprintln(val)\n}\n"
	// Target com espaços diferentes e sem tabs
	target := "val := compute(1, 2)"
	exact, desc, ok := matchFuzzyWhitespace(content, target)
	if !ok {
		t.Fatalf("esperava matchFuzzyWhitespace para linha única com espaços diferentes")
	}
	if !strings.Contains(desc, "line 4") {
		t.Errorf("desc inesperada: %s", desc)
	}
	if exact != "\tval := compute( 1, 2 )" {
		t.Errorf("exact incorreto: %q", exact)
	}
}

func TestMatchFuzzyWhitespaceMultiLine(t *testing.T) {
	content := "package main\n\nfunc add(a, b int) int {\n\tx := a + b\n\ty := x * 2\n\treturn y\n}\n"
	// Target com 4 espaços em vez de tab e quebras normais
	target := "    x := a + b\n    y := x * 2\n    return y\n"
	exact, desc, ok := matchFuzzyWhitespace(content, target)
	if !ok {
		t.Fatalf("esperava matchFuzzyWhitespace para bloco multilinha")
	}
	if !strings.Contains(desc, "lines 4-6") {
		t.Errorf("desc inesperada: %s", desc)
	}
	want := "\tx := a + b\n\ty := x * 2\n\treturn y\n"
	if exact != want {
		t.Errorf("exact esperado %q, obteve %q", want, exact)
	}
}

func TestMatchFuzzyWhitespaceAmbiguous(t *testing.T) {
	content := "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 1\n}\n"
	// Alvo ambíguo (duas linhas iguais no arquivo)
	target := "return 1"
	_, _, ok := matchFuzzyWhitespace(content, target)
	if ok {
		t.Fatalf("alvo ambíguo não deveria ser remediado automaticamente")
	}
}

func TestHandleEditFileRemediationWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "calc.go")
	orig := "package calc\n\nfunc Calc() int {\n\ta := 10\n\tb := 20\n\treturn a + b\n}\n"
	_ = os.WriteFile(p, []byte(orig), 0o644)

	// A LLM manda 4 espaços e espaços divergentes
	target := "    a := 10\n    b := 20\n    return a + b\n"
	repl := "\ta := 100\n\tb := 200\n\treturn a + b\n"

	msg, err := HandleEditFile(EditFileArgs{
		Path:        p,
		Target:      target,
		Replacement: repl,
	})
	if err != nil {
		t.Fatalf("HandleEditFile com remediação de whitespace falhou: %v", err)
	}
	if !strings.Contains(msg, "[FastExec Auto-Remediation]") {
		t.Errorf("mensagem deveria conter nota de auto-remediação: %s", msg)
	}

	updated, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("erro ao ler arquivo atualizado: %v", err)
	}
	if !strings.Contains(string(updated), "a := 100") || !strings.Contains(string(updated), "b := 200") {
		t.Errorf("arquivo não foi atualizado corretamente:\n%s", string(updated))
	}
}

func TestHandleEditFileRemediationSymbolAST(t *testing.T) {
	p := filepath.Join(t.TempDir(), "service.go")
	orig := "package service\n\nfunc ProcessData(id string) error {\n\tif id == \"\" {\n\t\treturn nil\n\t}\n\t// corpo antigo\n\treturn nil\n}\n\nfunc Other() {}\n"
	_ = os.WriteFile(p, []byte(orig), 0o644)

	// Target mirando ProcessData mas com corpo desatualizado
	target := "func ProcessData(id string) error {\n    // linha desatualizada\n    return nil\n}"
	repl := "func ProcessData(id string) error {\n\tlog.Println(id)\n\treturn nil\n}"

	msg, err := HandleEditFile(EditFileArgs{
		Path:        p,
		Target:      target,
		Replacement: repl,
	})
	if err != nil {
		t.Fatalf("HandleEditFile com remediação AST falhou: %v", err)
	}
	if !strings.Contains(msg, "[FastExec Auto-Remediation]") || !strings.Contains(msg, "ProcessData") {
		t.Errorf("mensagem deveria indicar auto-remediação do símbolo: %s", msg)
	}

	updated, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("erro ao ler arquivo atualizado: %v", err)
	}
	if !strings.Contains(string(updated), "log.Println(id)") {
		t.Errorf("corpo da função não foi atualizado:\n%s", string(updated))
	}
}
