package mcp

import (
	"os"
	"testing"

	"homelab/fastexec/internal/config"
)

// TestMain fixa a config de exemplo: os goldens não dependem da config de quem roda.
func TestMain(m *testing.M) {
	_, cleanup := config.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}
