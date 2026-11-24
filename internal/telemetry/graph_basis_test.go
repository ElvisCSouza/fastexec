package telemetry

import "testing"

func TestGraphBasisColumnsStoreAndDefaultUnknown(t *testing.T) {
	db := openCutDB(t)
	if err := writeEntry(db, AuditEntry{Tool: "code_graph", Basis: GraphBasis{
		Action: "callers", Lang: "csharp", Provider: "csharp-ls", Grade: "semantic", Cache: "warm", Depth: 2, Refs: 7, Files: 3,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := writeEntry(db, AuditEntry{Tool: "exec"}); err != nil {
		t.Fatal(err)
	}
	var action, lang, provider, grade, cache string
	var depth, refs, files int
	if err := db.QueryRow(`SELECT graph_action, graph_lang, graph_provider, graph_grade, graph_cache, graph_depth, graph_refs, graph_files FROM audit WHERE tool = 'code_graph'`).
		Scan(&action, &lang, &provider, &grade, &cache, &depth, &refs, &files); err != nil {
		t.Fatal(err)
	}
	if action != "callers" || lang != "csharp" || provider != "csharp-ls" || grade != "semantic" || cache != "warm" || depth != 2 || refs != 7 || files != 3 {
		t.Fatalf("basis row wrong: %s %s %s %s %s %d %d %d", action, lang, provider, grade, cache, depth, refs, files)
	}
	var eDepth, eRefs int
	if err := db.QueryRow(`SELECT graph_depth, graph_refs FROM audit WHERE tool = 'exec'`).Scan(&eDepth, &eRefs); err != nil {
		t.Fatal(err)
	}
	if eDepth != -1 || eRefs != -1 {
		t.Fatalf("calls without basis must store -1 (unknown), got depth=%d refs=%d", eDepth, eRefs)
	}
}
