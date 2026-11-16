package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTsProject(t *testing.T) (dir, src string) {
	t.Helper()
	dir = t.TempDir()
	cfg := "{\"compilerOptions\":{\"strict\":true,\"noEmit\":true}}"
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "a.ts")
}

func TestTsProjectDiags(t *testing.T) {
	if extToolPath("tsc") == "" {
		t.Skip("tsc ausente")
	}
	_, src := writeTsProject(t)
	if err := os.WriteFile(src, []byte("export const n: number = \"x\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, ok := tsProjectDiags(filepath.Dir(src))
	if !ok {
		t.Fatal("tsc deveria ter respondido")
	}
	found := false
	for _, d := range diags {
		if d.code == "TS2322" {
			found = true
		}
	}
	if !found {
		t.Fatalf("esperava TS2322, veio %+v", diags)
	}
}

func TestTsHandOffPublishesLate(t *testing.T) {
	if extToolPath("tsc") == "" {
		t.Skip("tsc ausente")
	}
	_, src := writeTsProject(t)
	text := "export const n: number = \"x\";\n"
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &autoEdit{lang: "typescript", abs: src}
	a.tsHandOff(text)
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if s := TakeLate(); strings.Contains(s, "2322") {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("nao chegou diagnostico tardio com TS2322")
}
