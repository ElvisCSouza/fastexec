package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickTFM(t *testing.T) {
	cases := []struct {
		name  string
		seen  []string
		major int
		want  string
	}{
		{"modern capped by SDK", []string{"net462", "netstandard2.0", "net6.0", "net7.0", "net8.0"}, 7, "net7.0"},
		{"modern with newer SDK", []string{"netstandard2.0", "net6.0", "net8.0"}, 9, "net8.0"},
		{"only old targets", []string{"net462", "netstandard2.0", "netstandard2.1"}, 8, "netstandard2.1"},
		{"windows suffix ignored", []string{"net8.0-windows", "netstandard2.0"}, 8, "netstandard2.0"},
		{"nothing usable", []string{"net462", "net48"}, 8, ""},
	}
	for _, c := range cases {
		seen := map[string]bool{}
		for _, s := range c.seen {
			seen[s] = true
		}
		if got := pickTFM(seen, c.major); got != c.want {
			t.Errorf("%s: pickTFM = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMultiTargetFramework(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/Lib/Lib.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><TargetFrameworks>net462;netstandard2.0;net6.0;net7.0</TargetFrameworks></PropertyGroup></Project>")
	write("test/App.Tests/App.Tests.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><TargetFramework>net7.0</TargetFramework></PropertyGroup></Project>")
	if got := multiTargetFramework(root, 8); got != "net7.0" {
		t.Errorf("multiTargetFramework = %q, want net7.0", got)
	}

	single := t.TempDir()
	if err := os.WriteFile(filepath.Join(single, "A.csproj"), []byte("<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := multiTargetFramework(single, 8); got != "" {
		t.Errorf("a single-target repository must not set TargetFramework, got %q", got)
	}
}
