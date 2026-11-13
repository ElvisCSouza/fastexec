package mcp

import "testing"

func TestGraphDecisionEngine(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"exact csharp", "=== callers of 'CreateLogger' (exact, csharp-ls) ===\ntests: 24 files: a.cs\n- a.cs:1 (calls)", "exact:csharp-ls"},
		{"exact gopls", "=== callers of 'NewFileErrorFromName' (exact, gopls) ===\n- x.go:3 (calls)", "exact:gopls"},
		{"unavailable", "=== callers of 'X' ===\n- a.cs:1 (calls)\nlsp unavailable (restore needed: run dotnet restore); by name", "unavailable"},
		{"by name", "=== callers of 'X' ===\n- a.cs:1 (calls)\nTotal: 1 references in 1 files.", "by-name"},
		{"other output untouched", "ok\n=== callers of 'X' (exact, gopls) ===", ""},
	}
	for _, c := range cases {
		if got := graphDecision(c.out); got != c.want {
			t.Errorf("%s: graphDecision = %q, want %q", c.name, got, c.want)
		}
	}
}
