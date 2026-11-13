package tools

import (
	"strings"
	"testing"
)

// TestGraphSearchTipoSemReferenciaNaoResponde: o indice por nome nao ve toda referencia a um tipo (ILogEventSink deu
// 0 contra 64 do servidor de linguagem), entao "0 referencias" de um tipo nao pode virar a resposta: a saida crua
// vale e o grafo so diz onde o tipo e declarado. Funcao sem chamador continua respondida pelo grafo.
func TestGraphSearchTipoSemReferenciaNaoResponde(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go": "package pkg\n\ntype Sink interface {\n\tEmit(x int)\n}\n\nfunc Lonely() {\n}\n",
	})
	out, handled, note := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn Sink .", Cwd: repo, Caller: "test#zr1"}, "grep -rn Sink .", false)
	if handled || out != "" {
		t.Fatalf("tipo sem referencia indexada nao pode ser respondido pelo indice:\n%s", out)
	}
	if !strings.Contains(note, "declared at pkg/a.go") || !strings.Contains(note, "does not mean there are none") {
		t.Errorf("a nota deve dizer onde e declarado e que o zero nao prova nada: %q", note)
	}
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn Lonely .", Cwd: repo, Caller: "test#zr2"}, "grep -rn Lonely .", false); !handled {
		t.Error("funcao sem chamador continua sendo uma resposta do grafo")
	}
}
