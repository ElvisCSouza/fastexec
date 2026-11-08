package tools

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/fastexec/internal/analytics"
	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/telemetry"
)

// TestMain fixa a config de exemplo (server, router, devbox) e uma pasta de dados temporária: os testes não
// dependem da config de quem roda nem gravam na pasta real.
func TestMain(m *testing.M) {
	dir, cleanup := config.Isolate()
	analytics.SessionsRoot = filepath.Join(dir, "sessions") // calculada no init, antes do Isolate
	code := m.Run()
	telemetry.Close()
	cleanup()
	os.Exit(code)
}

// remoteHost devolve o host real dos testes de integração (FASTEXEC_TEST_HOST, um nome da sua config, com daemon
// server-exec Linux) e usa os hosts da sua config (FASTEXEC_TEST_CONFIG, senão ~/.fastexec/config.json) durante o
// teste, com a pasta de dados ainda temporária; sem a variável o teste é pulado.
func remoteHost(t *testing.T) string {
	t.Helper()
	h := os.Getenv("FASTEXEC_TEST_HOST")
	if h == "" {
		t.Skip("FASTEXEC_TEST_HOST not set (integration test against a real host)")
	}
	p := os.Getenv("FASTEXEC_TEST_CONFIG")
	if p == "" {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, ".fastexec", "config.json")
	}
	t.Setenv("FASTEXEC_CONFIG", p)
	c := config.Load()
	if c.Err() != nil {
		t.Fatalf("config: %v", c.Err())
	}
	c.DataDir, c.HandoffDir = "", ""
	t.Cleanup(config.Set(c))
	return h
}
