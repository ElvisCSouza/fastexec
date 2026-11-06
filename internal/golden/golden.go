// Package golden compara a saída dos testes com arquivos versionados em testdata/golden do pacote testado.
//
// Regenerar (só quando a mudança for intencional, e revise o diff antes de commitar):
//
//	go test ./internal/mcp ./internal/tools ./internal/transport ./cmd/fastexec -run Golden -update
package golden

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing")

// Check compara got com testdata/golden/<name>. Com -update, grava got no arquivo.
func Check(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden atualizado: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file missing (%v); create it with: go test -run Golden -update", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("output differs from %s (if the change is intended, regenerate with -update)\n--- expected\n%s\n--- got\n%s", path, want, got)
	}
}
