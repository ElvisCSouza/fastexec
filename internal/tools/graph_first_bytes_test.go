package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Arquivo de muitas linhas curtas e poucos bytes (o caso da CS6 do ciclo6: 138 linhas, 3,8 KB): o texto inteiro é
// mais barato que outline + segunda leitura, então vem o texto.
func TestGraphFirstSmallBytesFileReturnsTheText(t *testing.T) {
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "func F%d() int {\n\treturn %d\n}\n\n", i, i) // ~140 linhas, ~0,9 KB
	}
	p := filepath.Join(t.TempDir(), "small.go")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleReadFile(ReadFileArgs{Path: p, Caller: "test#gfb1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, GraphMarkOutline) || !strings.Contains(out, "func F29() int") {
		t.Fatalf("arquivo pequeno em bytes deve vir inteiro:\n%.300s", out)
	}
}

// Muitas declarações e texto grande, mas o outline não é muito menor que o texto (assinaturas longas, corpos de uma
// linha): também vem o texto.
func TestGraphFirstOutlineNotMuchSmallerReturnsTheText(t *testing.T) {
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "func Function%dWithAVeryLongName(argumentNumberOne int, argumentNumberTwo string) int { return %d }\n", i, i)
	}
	p := filepath.Join(t.TempDir(), "dense.go")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleReadFile(ReadFileArgs{Path: p, Caller: "test#gfb2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, GraphMarkOutline) {
		t.Fatalf("outline quase do tamanho do texto não deveria ser usado:\n%.300s", out)
	}
}
