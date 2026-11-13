package lsp

import (
	"testing"
	"time"
)

// Integração real: precisa de um SDK do .NET na máquina (e compila o helper uma vez). Sem SDK, pula.
func TestRoslynSyntax(t *testing.T) {
	if sdk, _ := dotnetSDK(); sdk == "" || roslynBincore(sdk) == "" {
		t.Skip("sem SDK do .NET")
	}
	t.Setenv("FASTEXEC_HOME", t.TempDir()) // o helper é compilado num lugar descartável
	RoslynPrepare()
	deadline := time.Now().Add(2 * time.Minute)
	var (
		issues []SyntaxIssue
		st     RoslynState
		err    error
	)
	for time.Now().Before(deadline) {
		issues, st, err = RoslynSyntax("class A { void M() { var x = 1 } }")
		if st != RoslynBuilding {
			break
		}
	}
	if st == RoslynUnavailable {
		t.Skipf("helper indisponível neste ambiente: %v", err)
	}
	if st != RoslynReady || len(issues) != 1 || issues[0].ID != "CS1002" || issues[0].Line != 1 {
		t.Fatalf("state=%v issues=%+v err=%v", st, issues, err)
	}
	ok, st, err := RoslynSyntax("class B { void M() { var x = 1; } }")
	if err != nil || st != RoslynReady || len(ok) != 0 {
		t.Fatalf("código correto: state=%v issues=%+v err=%v", st, ok, err)
	}
	// Só erro: um warning do compilador (variável não usada) nunca sai da camada de sintaxe.
	w, _, _ := RoslynSyntax("class C { void M() { int x; } }")
	if len(w) != 0 {
		t.Fatalf("warning vazou: %+v", w)
	}
	roslynStop()
}
