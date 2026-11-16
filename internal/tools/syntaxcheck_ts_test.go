package tools

import "testing"

func TestTsSyntaxCodeFilter(t *testing.T) {
	// sintaxe: parser (1xxx) e gramatica de JSX (17xxx)
	for _, c := range []int{1002, 1005, 1109, 1128, 1134, 17008} {
		if !tsSyntaxCode(c) {
			t.Fatalf("TS%d deveria contar como sintaxe", c)
		}
	}
	// semantica/contextual: fora da camada 1
	for _, c := range []int{2304, 2307, 1375, 1378, 1308, 1208, 1219} {
		if tsSyntaxCode(c) {
			t.Fatalf("TS%d nao deveria contar como sintaxe", c)
		}
	}
}

func TestTsCheckerLive(t *testing.T) {
	if extToolPath("tsc") == "" {
		t.Skip("tsc ausente")
	}
	if iss, st := syntaxCheck("a.ts", "const x: number = 1;\nexport {};\n"); st != syntaxReady || len(iss) != 0 {
		t.Fatalf("ts valido: st=%v iss=%+v", st, iss)
	}
	iss, st := syntaxCheck("a.ts", "const = ;\n")
	if st != syntaxReady || len(iss) == 0 || iss[0].id == "" {
		t.Fatalf("ts invalido deveria acusar: st=%v iss=%+v", st, iss)
	}
	// tipo indefinido NAO e sintaxe (camada 2)
	if iss, st := syntaxCheck("a.ts", "const y: number = naoExiste;\nexport {};\n"); st != syntaxReady || len(iss) != 0 {
		t.Fatalf("tipo nao e sintaxe: st=%v iss=%+v", st, iss)
	}
	if iss, st := syntaxCheck("a.tsx", "const el = <div><span></div>;\n"); st != syntaxReady || len(iss) == 0 {
		t.Fatalf("jsx invalido deveria acusar: st=%v iss=%+v", st, iss)
	}
}
