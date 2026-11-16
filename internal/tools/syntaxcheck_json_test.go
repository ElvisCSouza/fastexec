package tools

import "testing"

func TestJSONSyntaxLayer1(t *testing.T) {
	if !syntaxLanguage("x.json") {
		t.Fatal("json deve ter camada 1")
	}
	if issues, st := syntaxCheck("x.json", "[1, 2, 3]"); st != syntaxReady || len(issues) != 0 {
		t.Fatalf("json valido: st=%v issues=%v", st, issues)
	}
	issues, st := syntaxCheck("x.json", "[\n1,\n2\n")
	if st != syntaxReady || len(issues) == 0 {
		t.Fatalf("json invalido deveria acusar: st=%v issues=%v", st, issues)
	}
	if issues[0].line < 3 {
		t.Fatalf("linha do erro esperada >=3, veio %d", issues[0].line)
	}
}
