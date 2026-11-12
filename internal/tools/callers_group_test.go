package tools

import (
	"fmt"
	"strings"
	"testing"
)

func manySites(n int, label func(i int) string, rel string) string {
	var b strings.Builder
	b.WriteString("=== callers of 'F' (exact, gopls) ===\n")
	for i := 0; i < n; i++ {
		file := "a.go"
		if i >= n/2 {
			file = "doc/b.go"
		}
		if l := label(i); l != "" {
			fmt.Fprintf(&b, "- %s:%d in %s (%s)\n", file, 10+i, l, rel)
		} else {
			fmt.Fprintf(&b, "- %s:%d (%s)\n", file, 10+i, rel)
		}
	}
	fmt.Fprintf(&b, "Total: %d references in 2 files.", n)
	return b.String()
}

func TestGroupCallerSitesByFile(t *testing.T) {
	in := manySites(20, func(i int) string { return fmt.Sprintf("fn%d", i/2) }, "calls")
	out := groupCallerSites(in)
	if len(out) >= len(in) {
		t.Fatalf("o agrupado deveria ser menor (%d >= %d)", len(out), len(in))
	}
	for _, want := range []string{
		"=== callers of 'F' (exact, gopls) ===", "(20 sites, all 'calls', grouped by file",
		"- a.go (10): fn0 10,11; fn1 12,13", "- doc/b.go (10): fn5 20,21", "Total: 20 references in 2 files.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q em:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- a.go:10") {
		t.Errorf("linha por ponto de chamada não deveria sobrar:\n%s", out)
	}
}

func TestGroupCallerSitesWithoutLabels(t *testing.T) {
	out := groupCallerSites(manySites(16, func(int) string { return "" }, "reference"))
	if !strings.Contains(out, "- a.go (8): 10,11,12,13,14,15,16,17") {
		t.Fatalf("sem rótulo, só as linhas:\n%s", out)
	}
}

func TestGroupCallerSitesLeavesSmallAndLossyOutputAlone(t *testing.T) {
	small := manySites(10, func(i int) string { return "fn" }, "calls")
	if got := groupCallerSites(small); got != small {
		t.Errorf("poucos pontos não se agrupam:\n%s", got)
	}
	via := strings.Replace(manySites(20, func(i int) string { return "fn" }, "calls"), "in fn (calls)", "in fn via x -> T (calls)", -1)
	if got := groupCallerSites(via); got != via {
		t.Errorf("com 'via' o agrupamento esconderia o tipo do receptor:\n%s", got)
	}
	mixed := manySites(20, func(i int) string { return "fn" }, "calls")
	mixed = strings.Replace(mixed, "- a.go:10 in fn (calls)", "- a.go:10 in fn (implements)", 1)
	if got := groupCallerSites(mixed); got != mixed {
		t.Errorf("relações diferentes não se agrupam:\n%s", got)
	}
	if got := groupCallerSites("(no callers of 'x' in 3 indexed files)"); got != "(no callers of 'x' in 3 indexed files)" {
		t.Errorf("texto sem pontos de chamada deve passar intacto: %q", got)
	}
}
