package tools

import "testing"

// TestIsHeadLimit: só `head` com limite puro conta; qualquer outra coisa depois do pipe continua sendo comando de verdade.
func TestIsHeadLimit(t *testing.T) {
	yes := []string{"head -100", "head -n 100", "head -n100", "head -5"}
	no := []string{"head", "head file.txt", "head -c 100", "head -n", "tail -5", "sort", "head -n 5 file", "wc -l"}
	for _, s := range yes {
		if !isHeadLimit(s) {
			t.Errorf("%q deveria ser limite de head", s)
		}
	}
	for _, s := range no {
		if isHeadLimit(s) {
			t.Errorf("%q nao deveria ser limite de head", s)
		}
	}
}

// TestParseIdentSearchComHead: `grep -rn X dir | head -N` vira a mesma busca de `grep -rn X dir`; outro pipe nao.
func TestParseIdentSearchComHead(t *testing.T) {
	base, ok := parseIdentSearch(`grep -rn "ILogEventSink" src/Serilog --include=*.cs`)
	if !ok {
		t.Fatal("a busca pura deveria ser reconhecida")
	}
	piped, ok := parseIdentSearch(`grep -rn "ILogEventSink" src/Serilog --include=*.cs | head -100`)
	if !ok || piped.pattern != base.pattern || len(piped.paths) != len(base.paths) {
		t.Fatalf("com head: %#v ok=%v, sem head: %#v", piped, ok, base)
	}
	if _, ok := parseIdentSearch(`grep -rn "ILogEventSink" src/Serilog | sort`); ok {
		t.Error("pipe para sort nao pode ser tratado como a busca pura")
	}
	if _, ok := parseIdentSearch(`grep -rn "ILogEventSink" src/Serilog | wc -l`); ok {
		t.Error("pipe para wc nao pode ser tratado como a busca pura")
	}
}
