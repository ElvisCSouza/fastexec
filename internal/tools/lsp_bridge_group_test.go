package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// TestWriteGroupedSites: acima do limite a lista sai por arquivo, com as linhas juntas por membro.
func TestWriteGroupedSites(t *testing.T) {
	sites := []graph.Site{{File: "a.cs", Line: 42}, {File: "a.cs", Line: 58}, {File: "a.cs", Line: 79}, {File: "b.cs", Line: 7}, {File: "b.cs", Line: 9}}
	infos := []graph.RefInfo{{Label: "A.Sink"}, {Label: "A.Sink", Relation: "reference"}, {Label: "A.Logger"}, {Label: "B.M", Relation: "calls"}, {Label: "A.Logger", Relation: "references"}}
	var b strings.Builder
	writeGroupedSites(&b, sites, infos, []int{0, 1, 2, 3, 4})
	want := "- a.cs (3): 42,58 in A.Sink; 79 in A.Logger\n- b.cs (2): 7 in B.M (calls); 9 in A.Logger\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}
