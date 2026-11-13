package tools

import (
	"strings"
	"testing"
)

// TestSplitLeadingCd: `cd <pasta> && <busca>` separa pasta e comando; qualquer outra forma fica como esta.
func TestSplitLeadingCd(t *testing.T) {
	rest, dir, ok := splitLeadingCd(`cd /w/Data/x/serilog && grep -rln "ILogEventSink" src/Serilog/ --include=*.cs`)
	if !ok || dir != "/w/Data/x/serilog" || rest != `grep -rln "ILogEventSink" src/Serilog/ --include=*.cs` {
		t.Fatalf("got rest=%q dir=%q ok=%v", rest, dir, ok)
	}
	if _, dir, ok := splitLeadingCd(`cd /d "W:\Data x\repo" && findstr /s Foo *.cs`); !ok || dir != `W:\Data x\repo` {
		t.Errorf("cd /d com aspas: dir=%q ok=%v", dir, ok)
	}
	for _, s := range []string{`grep -rn X .`, `cd src`, `cd a; grep X .`, `cd a | grep X`, `cd && grep X`, `cd a && `} {
		if _, _, ok := splitLeadingCd(s); ok {
			t.Errorf("%q nao deveria ser um cd inicial", s)
		}
	}
}

// TestGraphSearchComCdInicial: a busca depois de `cd` e respondida pelo grafo na pasta do cd, e o rodape manda repetir
// o comando ORIGINAL (com o cd) com --raw.
func TestGraphSearchComCdInicial(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
		"main.go":  "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
	})
	cmd := `cd ` + repo + ` && grep -rn CalcTotal .`
	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: cmd, Cwd: t.TempDir(), Caller: "test#cd1"}, cmd, false)
	if !handled || !strings.Contains(out, "def: pkg/a.go:3-5 (func)") {
		t.Fatalf("a busca depois do cd tem de sair respondida do grafo (handled=%v):\n%s", handled, out)
	}
	if !strings.Contains(out, "cd "+repo+" && grep -rn CalcTotal . --raw") {
		t.Errorf("o rodape tem de repetir o comando original com --raw:\n%s", out)
	}
}
