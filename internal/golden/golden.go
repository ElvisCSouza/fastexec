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

var update = flag.Bool("update", false, "regrava os arquivos golden em vez de comparar")

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
		t.Fatalf("golden ausente (%v); gere com: go test -run Golden -update", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("saída difere de %s (se a mudança for intencional, regere com -update)\n--- esperado\n%s\n--- obtido\n%s", path, want, got)
	}
}
