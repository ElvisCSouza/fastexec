package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goSource(n int, tweak int) string {
	var b strings.Builder
	b.WriteString("package x\n\n")
	for i := 0; i < n; i++ {
		v := i
		if i == tweak {
			v = 9999
		}
		fmt.Fprintf(&b, "func F%d(x int) int {\n\treturn x + %d // comentario para dar tamanho a linha\n}\n\n", i, v)
	}
	return b.String()
}

func TestRewriteHint(t *testing.T) {
	old := goSource(80, -1)
	novo := goSource(80, 40) // uma linha mudou
	h := rewriteHint(old, novo)
	if !strings.HasPrefix(h, RewriteMark) || !strings.Contains(h, "to change about 1 line(s) in 1 place(s)") || !strings.Contains(h, "edit_file") {
		t.Fatalf("aviso esperado: %q", h)
	}
	if rewriteHint("", novo) != "" {
		t.Error("arquivo novo não tem aviso")
	}
	if rewriteHint(old, goSource(80, -1)[:1500]) != "" {
		t.Error("conteúdo pequeno não tem aviso")
	}
	if rewriteHint(old, strings.ToUpper(novo)) != "" {
		t.Error("reescrita de verdade (quase tudo mudou) não tem aviso")
	}
}

func TestWriteFileRewriteHint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(p, []byte(goSource(80, -1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: p, Content: goSource(80, 3), Overwrite: true})
	if err != nil || !strings.Contains(out, RewriteMark) {
		t.Fatalf("sobrescrita quase igual deveria avisar: %v %q", err, out)
	}
	if b, _ := os.ReadFile(p); string(b) != goSource(80, 3) {
		t.Fatal("o arquivo tem que ser gravado como enviado")
	}
	out, _ = HandleWriteFile(WriteFileArgs{Host: "local", Path: filepath.Join(filepath.Dir(p), "novo.go"), Content: goSource(80, -1)})
	if strings.Contains(out, RewriteMark) {
		t.Fatal("arquivo novo não avisa")
	}
}
