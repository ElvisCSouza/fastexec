package mcp

import (
	"os"
	"testing"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/telemetry"
)

// TestMain fixa a config de exemplo: os goldens não dependem da config de quem roda.
func TestMain(m *testing.M) {
	_, cleanup := config.Isolate()
	code := m.Run()
	telemetry.Close()
	if probeFile != nil {
		_ = probeFile.Close()
	}
	cleanup()
	os.Exit(code)
}
