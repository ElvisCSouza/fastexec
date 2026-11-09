package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bigGoFile(t *testing.T, funcs, bodyLines int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package demo\n\nimport (\n\t\"fmt\"\n)\n\n")
	for i := 0; i < funcs; i++ {
		fmt.Fprintf(&b, "func F%d(x int) int {\n", i)
		for j := 0; j < bodyLines; j++ {
			fmt.Fprintf(&b, "\tx += %d\n", j)
		}
		b.WriteString("\treturn x\n}\n\n")
	}
	p := filepath.Join(t.TempDir(), "demo.go")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGraphFirstWholeReadAndRepeat(t *testing.T) {
	p := bigGoFile(t, 10, 30) // ~340 linhas, 10 funções
	args := ReadFileArgs{Path: p, Caller: "test#gf1"}

	out, err := HandleReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, GraphMarkOutline) || !strings.Contains(out, "L7-L39: func F0(x int) int") || !strings.Contains(out, "--- header ---") {
		t.Fatalf("expected outline:\n%s", out)
	}
	if strings.Contains(out, "x += 29") {
		t.Fatal("outline must not carry the bodies")
	}

	full, _ := HandleReadFile(args)
	if !strings.Contains(full, "x += 29") || !strings.Contains(full, GraphMarkRepeat) {
		t.Fatalf("repeated read must return the full text with a note:\n%.300s", full)
	}

	// sem alternância: dentro da janela continua vindo o texto completo
	again, _ := HandleReadFile(args)
	if !strings.Contains(again, "x += 29") {
		t.Fatal("a third read inside the window must still return the full text")
	}
}

// Duas conversas no mesmo canal MCP (Claude Desktop não manda id de conversa) ou em canais diferentes: uma nunca
// consome a repetição da outra. O pior caso é receber o texto completo antes; nunca o índice quando pediu o texto.
func TestGraphFirstSharedIdentityNeverWithholds(t *testing.T) {
	p := bigGoFile(t, 10, 30)
	for _, ids := range [][2]string{{"canal#1", "canal#1"}, {"canal#1", "canal#2"}} {
		graphSeenMu.Lock()
		graphSeen = map[string]time.Time{}
		graphSeenMu.Unlock()
		a := ReadFileArgs{Path: p, Caller: ids[0]}
		b := ReadFileArgs{Path: p, Caller: ids[1]}
		if out, _ := HandleReadFile(a); !strings.HasPrefix(out, GraphMarkOutline) {
			t.Fatalf("%v: A deveria receber o índice", ids)
		}
		if out, _ := HandleReadFile(b); !strings.Contains(out, "x += 29") {
			t.Fatalf("%v: B lendo o mesmo arquivo recebe o texto completo (lado seguro)", ids)
		}
		if out, _ := HandleReadFile(a); !strings.Contains(out, "x += 29") {
			t.Fatalf("%v: A repetiu e tem que receber o texto completo, mesmo depois da leitura de B", ids)
		}
	}
}

func TestGraphFirstLeavesSmallAndTargetedReads(t *testing.T) {
	small := bigGoFile(t, 3, 5)
	if out, _ := HandleReadFile(ReadFileArgs{Path: small, Caller: "test#gf3"}); strings.Contains(out, GraphMarkOutline) {
		t.Fatal("small file must come as text")
	}
	big := bigGoFile(t, 10, 30)
	if out, _ := HandleReadFile(ReadFileArgs{Path: big, StartLine: 7, EndLine: 38, Caller: "test#gf3"}); !strings.Contains(out, "x += 29") {
		t.Fatalf("small range must come as text:\n%s", out)
	}
	if out, _ := HandleReadFile(ReadFileArgs{Path: big, Pattern: "func F3", Caller: "test#gf3"}); strings.Contains(out, GraphMarkOutline) {
		t.Fatal("pattern read must come as text")
	}
	// intervalo gigante vira outline do trecho
	out, _ := HandleReadFile(ReadFileArgs{Path: big, StartLine: 1, EndLine: 9999, Caller: "test#gf3"})
	if !strings.HasPrefix(out, GraphMarkOutline) {
		t.Fatalf("giant range must be outlined:\n%.300s", out)
	}
}

func TestGraphFirstSkipsLinearScriptsAndDocs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy.ps1")
	_ = os.WriteFile(script, []byte(strings.Repeat("Write-Host 'passo'\n", 400)), 0o644)
	if out, _ := HandleReadFile(ReadFileArgs{Path: script, Caller: "test#gf4"}); strings.Contains(out, GraphMarkOutline) {
		t.Fatal("linear script without functions must come as text")
	}
	doc := filepath.Join(dir, "notas.md")
	_ = os.WriteFile(doc, []byte(strings.Repeat("# titulo\ntexto\n", 300)), 0o644)
	if out, _ := HandleReadFile(ReadFileArgs{Path: doc, Caller: "test#gf4"}); strings.Contains(out, GraphMarkOutline) {
		t.Fatal("documents are never outlined")
	}
}

func TestGraphFirstDoesNotAffectEdit(t *testing.T) {
	p := bigGoFile(t, 10, 30)
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "x += 29\n\treturn x\n}\n\nfunc F9", Replacement: "x += 29\n\treturn x\n}\n\nfunc G9", Caller: "test#gf5"}); err != nil {
		t.Fatalf("edit on a graph-first file: %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "func G9(") || !strings.Contains(string(b), "func F0(") {
		t.Fatal("edit must see and keep the whole file")
	}
}
