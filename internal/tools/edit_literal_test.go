package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Alvo com "\n" literal (duas letras) num arquivo com quebras reais: aplica sozinho e avisa; nada de "target not found".
func TestEditFileAlvoComBarraNLiteral(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(p, []byte("func A() {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleEditFile(EditFileArgs{Path: p, Target: `func A() {\n\treturn 1`, Replacement: `func A() {\n\treturn 2`})
	if err != nil {
		t.Fatalf("deveria corrigir o escape literal: %v", err)
	}
	if !strings.Contains(out, "Auto-Remediation") || !strings.Contains(out, "literal backslash-n") {
		t.Errorf("faltou o aviso de autocorrecao: %s", out)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "func A() {\n\treturn 2\n}\n" {
		t.Errorf("conteudo inesperado: %q", b)
	}
}

// Se o arquivo contem de fato a sequencia literal \n (ex.: uma string em codigo), o alvo literal vale e nada e reescrito.
func TestEditFileBarraNLiteralPresenteNoArquivo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.go")
	if err := os.WriteFile(p, []byte("fmt.Println(\"a\\nb\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: `"a\nb"`, Replacement: `"a\nc"`}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "fmt.Println(\"a\\nc\")\n" {
		t.Errorf("conteudo inesperado: %q", b)
	}
}

// Sem correspondencia nem escapando: continua o erro de sempre e nada e gravado.
func TestEditFileAlvoAusenteContinuaErro(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(p, []byte("um\ndois\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := HandleEditFile(EditFileArgs{Path: p, Target: `tres\nquatro`, Replacement: "x"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("deveria falhar: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "um\ndois\n" {
		t.Errorf("arquivo alterado: %q", b)
	}
}
