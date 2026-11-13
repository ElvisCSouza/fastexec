package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// Um main.zig não é coberto por main_test.go de pacotes Go: a linguagem do teste tem que ser a do arquivo editado.
func TestRelatedTestsLineIgnoresTestsOfAnotherLanguage(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"server/main.zig":             "pub fn main() void {}",
		"internal/graph/main_test.go": "package graph",
		"internal/mcp/main_test.go":   "package mcp",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "server", "main.zig"), lang: "zig"}
	if line := a.relatedTestsLine(); line != "" {
		t.Fatalf("a Go test must not be offered for a .zig file: %q", line)
	}
}

// Nome genérico (main, index, util): o teste de nome parecido só vale na mesma pasta.
func TestRelatedTestsLineGenericNameNeedsTheSameFolder(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"cmd/a/main.go":      "package main",
		"cmd/a/main_test.go": "package main",
		"cmd/b/main_test.go": "package main",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "cmd", "a", "main.go"), lang: "go"}
	line := a.relatedTestsLine()
	if !strings.Contains(line, "cmd/a/main_test.go") || strings.Contains(line, "cmd/b/main_test.go") {
		t.Fatalf("only the test next to the file should be listed: %q", line)
	}
}
