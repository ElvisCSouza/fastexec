package tools

import "testing"

func TestGoSyntaxMissingBrace(t *testing.T) {
	issues, st := syntaxCheck("a.go", "package a\n\nfunc F() {\n\tx := 1\n")
	if st != syntaxReady || len(issues) == 0 {
		t.Fatalf("state=%v issues=%+v", st, issues)
	}
	if issues[0].line < 3 {
		t.Fatalf("linha inesperada: %+v", issues[0])
	}
}

func TestGoSyntaxValidAndNoTypeAnalysis(t *testing.T) {
	// símbolo indefinido NÃO é erro de sintaxe: é da camada 2
	issues, st := syntaxCheck("a.go", "package a\n\nfunc F() int { return naoExiste }\n")
	if st != syntaxReady || len(issues) != 0 {
		t.Fatalf("state=%v issues=%+v", st, issues)
	}
}

func TestSyntaxNoneForOtherLanguages(t *testing.T) {
	for _, p := range []string{"a.ts", "a.py", "a.rs", "README.md"} {
		if _, st := syntaxCheck(p, "qualquer coisa {"); st != syntaxNone {
			t.Fatalf("%s: state=%v", p, st)
		}
	}
}

func TestNewSyntaxIssuesDropsPreexisting(t *testing.T) {
	before := []syntaxIssue{{id: "CS1002", line: 3, msg: "; expected"}}
	now := []syntaxIssue{
		{id: "CS1002", line: 9, msg: "; expected"}, // o mesmo erro, linha andou
		{id: "CS1513", line: 12, msg: "} expected"},
	}
	got := newSyntaxIssues(now, before)
	if len(got) != 1 || got[0].id != "CS1513" {
		t.Fatalf("got %+v", got)
	}
}
