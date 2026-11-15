package tools

import (
	"strings"
	"testing"
)

func TestReadDedupStubsRepeat(t *testing.T) {
	args := ReadFileArgs{Path: "W:\\dedup\\x.go", Host: "local", Caller: "test#1"}
	big := strings.Repeat("uma linha qualquer\n", 200) // > readDedupMinBytes
	if got := dedupReadOutput(args, big); got != big {
		t.Fatal("a 1a leitura passa inteira")
	}
	if got := dedupReadOutput(args, big); !strings.Contains(got, "Identical to the same read") {
		t.Fatalf("a 2a leitura deve virar stub: %q", got)
	}
	if got := dedupReadOutput(args, big); got != big {
		t.Fatal("a 3a leitura devolve o texto de novo")
	}
}

func TestReadDedupNoIdentity(t *testing.T) {
	big := strings.Repeat("uma linha qualquer\n", 200)
	if got := dedupReadOutput(ReadFileArgs{Path: "W:\\dedup\\y.go"}, big); got != big {
		t.Fatal("sem identidade nao deduplica")
	}
}

func TestHintOnce(t *testing.T) {
	if !hintOnce("conv:1", "line-slice") {
		t.Fatal("1a vez mostra")
	}
	if hintOnce("conv:1", "line-slice") {
		t.Fatal("2a vez suprime")
	}
	if !hintOnce("conv:2", "line-slice") {
		t.Fatal("outra conversa mostra")
	}
	if !hintOnce("", "line-slice") {
		t.Fatal("sem identidade sempre mostra")
	}
}
