package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/telemetry"
)

// Cada regra que corta ou troca texto tem que deixar uma economia medida (antes/depois) na categoria própria:
// sem isso a regra fica invisível na telemetria e não dá para decidir mantê-la ou não.

func catsOf(items []telemetry.Saving) map[string]telemetry.Saving {
	m := map[string]telemetry.Saving{}
	for _, s := range items {
		m[s.Cat] = s
	}
	return m
}

func TestReadCapAnotaEconomiaMedida(t *testing.T) {
	big := strings.Repeat("uma linha de texto razoavelmente longa para encher o limite\n", 4000)
	telemetry.BeginCall()
	out := capRead(big, ReadFileArgs{Path: "x.txt"})
	got, ok := catsOf(telemetry.EndCall())[telemetry.CatReadCap]
	if !ok {
		t.Fatal("capRead não anotou a categoria readcap")
	}
	if !got.Exact || got.Before != len(big) || got.After != len(out) || got.Bytes != len(big)-len(out) {
		t.Errorf("economia: %+v (antes %d, depois %d)", got, len(big), len(out))
	}

	telemetry.BeginCall()
	_ = capRead("curto\n", ReadFileArgs{Path: "x.txt"})
	if _, ok := catsOf(telemetry.EndCall())[telemetry.CatReadCap]; ok {
		t.Error("leitura abaixo do limite não pode anotar economia")
	}
}

func TestCapWholeFileAnotaEconomiaMedida(t *testing.T) {
	big := strings.Repeat("linha de arquivo grande para passar do limite de leitura\n", 4000)
	telemetry.BeginCall()
	out := capWholeFile(big, "x.go")
	got, ok := catsOf(telemetry.EndCall())[telemetry.CatReadCap]
	if !ok || got.Before != len(big) || got.After != len(out) {
		t.Errorf("capWholeFile: %+v ok=%v", got, ok)
	}
}

func TestExecDedupAnotaEconomiaMedida(t *testing.T) {
	resetExecDedup()
	args := ExecArgs{Host: "noteserver", Command: "big", Caller: "c#cat"}
	big := strings.Repeat("linha de saida\n", 400)
	telemetry.BeginCall()
	_ = dedupExecOutput(args, "big", big) // 1ª: passa inteira, sem economia
	if len(telemetry.EndCall()) != 0 {
		t.Fatal("a 1ª execução não pode anotar economia")
	}
	telemetry.BeginCall()
	stub := dedupExecOutput(args, "big", big)
	got, ok := catsOf(telemetry.EndCall())[telemetry.CatDedup]
	if !ok || got.Before != len(big) || got.After != len(stub) {
		t.Errorf("dedup: %+v ok=%v", got, ok)
	}
}

func TestNoteListingGroupAnotaEconomiaMedida(t *testing.T) {
	raw := strings.Repeat("src\\pasta\\arquivo.cs\n", 300)
	grouped := "src\\pasta\\ (300): arquivo.cs\n"
	telemetry.BeginCall()
	noteListingGroup(raw, grouped)
	got, ok := catsOf(telemetry.EndCall())[telemetry.CatGroup]
	if !ok || got.Before != len(raw) || got.After != len(grouped) || !strings.Contains(got.Note, "300 lines became 1") {
		t.Errorf("group: %+v ok=%v", got, ok)
	}
}
