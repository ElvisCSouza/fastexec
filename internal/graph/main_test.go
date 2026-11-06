package graph

import (
	"homelab/fastexec/internal/config"
	"os"
	"testing"
)

// TestMain usa uma pasta de dados temporária e a config de exemplo: os testes não gravam na pasta real.
func TestMain(m *testing.M) {
	_, cleanup := config.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
