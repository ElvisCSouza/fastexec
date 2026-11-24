package mcp

import "testing"

func TestGraphBasisOfReadsLineAfterHeader(t *testing.T) {
	out := "=== callers of 'X' (exact, csharp-ls) ===\nbasis: action=callers lang=csharp provider=csharp-ls grade=semantic fallback=none exact=yes freshness=fresh completeness=complete scope=repo cache=warm depth=1 refs=5 files=2 omitted=0\nA.cs:1\n"
	b := graphBasisOf(out)
	if b.Action != "callers" || b.Lang != "csharp" || b.Provider != "csharp-ls" || b.Grade != "semantic" || b.Cache != "warm" {
		t.Fatalf("basis fields wrong: %+v", b)
	}
	if b.Depth != 1 || b.Refs != 5 || b.Files != 2 {
		t.Fatalf("counts wrong: %+v", b)
	}
}

func TestGraphBasisOfUnknownWhenNoLine(t *testing.T) {
	if b := graphBasisOf("some other output"); b.Action != "" {
		t.Fatalf("no basis line must give the zero value: %+v", b)
	}
	b := graphBasisOf("=== def of 'X' (by name) ===\nbasis: action=def lang=go provider=index grade=name fallback=no_server exact=no freshness=fresh completeness=complete scope=repo cache=n/a depth=0 omitted=0\n")
	if b.Refs != -1 || b.Files != -1 {
		t.Fatalf("missing counts must be -1 (unknown), not 0: %+v", b)
	}
}
