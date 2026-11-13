package graph

import (
	"fmt"
	"strings"
	"testing"
)

// Um depth=4 num símbolo popular não pode devolver centenas de linhas: o nível 1 fica inteiro, os níveis 2+ têm teto.
func TestRenderDeepCapsDeeperLevels(t *testing.T) {
	var l1, l2 []callerSite
	files := map[string]bool{}
	for i := 0; i < 3; i++ {
		l1 = append(l1, callerSite{file: fmt.Sprintf("src/a%d.cs", i), line: i + 1, count: 1})
	}
	for i := 0; i < 100; i++ {
		f := fmt.Sprintf("src/b%d.cs", i)
		files[f] = true
		l2 = append(l2, callerSite{file: f, line: i + 1, count: 1})
	}
	out := renderDeep("X", [][]callerSite{l1, l2}, 103, files, map[string]int{}, 2, 200)
	if got := strings.Count(out, "src/a"); got != 3 {
		t.Fatalf("depth 1 must be complete, got %d lines:\n%s", got, out)
	}
	if got := strings.Count(out, "src/b"); got != deepExtraLines {
		t.Fatalf("depth 2+ must be capped at %d lines, got %d", deepExtraLines, got)
	}
	if !strings.Contains(out, "more at depth 2+") {
		t.Fatalf("the cut must be announced:\n%s", out)
	}
}
