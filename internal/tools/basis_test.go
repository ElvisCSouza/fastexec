package tools

import (
	"errors"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

func TestBasisByNameAfterLSPFailureIsNeverExact(t *testing.T) {
	g := graphCall{action: "callers", depth: 1, lang: "csharp"}
	out := "=== callers of 'Foo' (by name) ===\nFoo.cs:3\nlsp unavailable (server crashed)\n"
	line := g.line(out)
	for _, want := range []string{"grade=name", "fallback=lsp_unavailable", "exact=no", "cache=n/a", "freshness=unknown", "completeness=unknown"} {
		if !strings.Contains(line, want) {
			t.Fatalf("basis %q lacks %q", line, want)
		}
	}
}

func TestBasisExactOnlyWhenSemanticCompleteFresh(t *testing.T) {
	g := graphCall{action: "def", lang: "go", warm: true}
	line := g.line("=== def of 'Run' (exact, gopls) ===\nmain.go:4\n")
	for _, want := range []string{"provider=gopls", "grade=semantic", "fallback=none", "exact=yes", "cache=warm", "completeness=complete", "freshness=fresh"} {
		if !strings.Contains(line, want) {
			t.Fatalf("basis %q lacks %q", line, want)
		}
	}
	cold := graphCall{action: "def", lang: "go"}
	if !strings.Contains(cold.line("=== def of 'Run' (exact, gopls) ===\n"), "cache=cold") {
		t.Fatal("exact answer with no warm server must say cache=cold")
	}
}

func TestBasisPartialWhenTruncatedOrIndexing(t *testing.T) {
	g := graphCall{action: "callers", depth: 1, lang: "csharp", warm: true}
	trunc := g.line("=== callers of 'X' (exact, csharp-ls) ===\nTotal: 9 references in 3 files.\n... 4 more\n")
	if !strings.Contains(trunc, "completeness=partial") || !strings.Contains(trunc, "exact=no") || !strings.Contains(trunc, "omitted=4") {
		t.Fatalf("truncated answer must be partial and not exact: %q", trunc)
	}
	building := graphCall{action: "callers", depth: 1, lang: "csharp", warm: true, building: true}
	b := building.line("=== callers of 'X' (exact, csharp-ls) ===\n")
	if !strings.Contains(b, "freshness=stale") || !strings.Contains(b, "completeness=partial") || !strings.Contains(b, "exact=no") {
		t.Fatalf("index still building must be stale, partial and not exact: %q", b)
	}
}

func TestBasisDepthFallbackLabel(t *testing.T) {
	g := graphCall{action: "callers", depth: 3, lang: "csharp"}
	line := g.line("=== callers of 'X' (by name) ===\n")
	if !strings.Contains(line, "fallback=depth") || !strings.Contains(line, "depth=3") {
		t.Fatalf("deep callers by name must say fallback=depth: %q", line)
	}
}

func TestWithBasisKeepsHeaderFirst(t *testing.T) {
	g := graphCall{action: "def", lang: "unknown"}
	out := withBasis("=== def of 'X' (by name) ===\nA.go:1\n", g)
	first, _, _ := strings.Cut(out, "\n")
	if first != "=== def of 'X' (by name) ===" {
		t.Fatalf("header must stay first: %q", out)
	}
	if withBasis("", g) != "" {
		t.Fatal("empty answer must come back unchanged")
	}
}

func TestSplitImplementersKeepsCallersClean(t *testing.T) {
	sites := []graph.Site{{File: "a.cs", Line: 1}, {File: "b.cs", Line: 2}, {File: "c.cs", Line: 3}}
	infos := []graph.RefInfo{
		{Label: "Caller", Relation: "calls"},
		{Label: "Impl", Relation: "implements"},
		{Label: "Child", Relation: "inherits"},
	}
	cs, ci, is, ii := splitImplementers(sites, infos)
	if len(cs) != 1 || cs[0].File != "a.cs" || ci[0].Label != "Caller" {
		t.Fatalf("callers must hold only calls: %v %v", cs, ci)
	}
	if len(is) != 2 || len(ii) != 2 {
		t.Fatalf("implementers must hold implements and inherits: %v", is)
	}
	line := implementersLine(is, ii)
	if !strings.HasPrefix(line, "implementers (2, complete, exact): ") || !strings.Contains(line, "Impl") || !strings.Contains(line, "Child") {
		t.Fatalf("implementers line wrong: %q", line)
	}
	if strings.Contains(line, "Caller") {
		t.Fatal("a caller must not appear among implementers")
	}
	if implementersLine(nil, nil) != "" {
		t.Fatal("no implementers means no line")
	}
}

func TestCourseWithGraphNeverEmptyAfterProviderFailure(t *testing.T) {
	exact := func() (string, error) { return "", errors.New("server down") }
	byName := func() (string, error) { return "=== callers of 'X' (by name) ===\nA.cs:1\n", nil }
	out, err := courseWithGraph("root", "X", exact, byName)
	if err != nil {
		t.Fatalf("provider failure must fall back, got error %v", err)
	}
	if strings.TrimSpace(out) == "" || !strings.Contains(out, "A.cs:1") {
		t.Fatalf("fallback answer must be the by-name result, got %q", out)
	}
}

func TestImplementersLineFilterKeepsOnlyMatchingFiles(t *testing.T) {
	text := "implementers (2, complete, exact): Impl (src/a/A.cs:3), Child (src/b/B.cs:5)\n"
	files := implementerFiles(text, "src/a")
	if len(files) != 1 || files[0] != "src/a/A.cs" {
		t.Fatalf("implementerFiles must keep the sub-tree file only: %v", files)
	}
	line := filterImplementersLine("implementers (2, complete, exact): Impl (src/a/A.cs:3), Child (src/b/B.cs:5)", "src/a")
	if !strings.HasPrefix(line, "implementers (1, complete, exact): ") || strings.Contains(line, "Child") {
		t.Fatalf("filtered implementers line wrong: %q", line)
	}
	if filterImplementersLine("implementers (1, complete, exact): Child (src/b/B.cs:5)", "src/a") != "" {
		t.Fatal("no implementer under the sub-tree must give an empty line")
	}
}
