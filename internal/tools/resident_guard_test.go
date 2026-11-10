package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferBinaryFromCommand(t *testing.T) {
	tests := []struct {
		cmd      string
		expected string
	}{
		{"go build ./cmd/agenteha", "agenteha"},
		{"go build -trimpath -o bin/server.exe ./cmd/server", "server.exe"},
		{"go build -o=myprog ./...", "myprog"},
		{"cargo build --bin worker", ""}, // inferBinaryFromCommand covers flags
	}

	for _, tc := range tests {
		got := inferBinaryFromCommand(tc.cmd)
		if got != tc.expected {
			t.Errorf("cmd %q: expected %q, got %q", tc.cmd, tc.expected, got)
		}
	}
}

func TestCheckResidentService_NoBuild(t *testing.T) {
	hint := checkResidentService("", "git status", "local")
	if hint != "" {
		t.Errorf("expected empty hint for git status, got %q", hint)
	}
}

func TestCheckResidentService_DetectsActiveProcess(t *testing.T) {
	dir := t.TempDir()

	// Simula um build que gerou um binário com o mesmo nome de um processo residente real (ex: agenteha.exe)
	procName := "agenteha.exe"
	fakePath := filepath.Join(dir, procName)
	if err := os.WriteFile(fakePath, []byte("fake"), 0755); err != nil {
		t.Fatal(err)
	}

	hint := checkResidentService(dir, "go build -o agenteha.exe .", "local")
	if !strings.Contains(hint, "[FastExec Guard]") {
		t.Fatalf("esperado aviso do FastExec Guard para processo ativo, obtido: %q", hint)
	}
	if !strings.Contains(hint, "agenteha") {
		t.Errorf("esperado menção a agenteha no aviso, obtido: %q", hint)
	}
	t.Logf("Aviso gerado com sucesso:\n%s", hint)
}
