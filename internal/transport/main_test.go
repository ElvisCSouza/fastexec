package transport

import (
	"os"
	"testing"

	"homelab/fastexec/internal/config"
)

// TestMain fixa a config de exemplo: os testes não dependem da config de quem roda.
func TestMain(m *testing.M) {
	_, cleanup := config.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// testDaemonAddr devolve o daemon Linux real dos testes de integração (FASTEXEC_TEST_ADDR=ip:porta) ou pula.
func testDaemonAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("FASTEXEC_TEST_ADDR")
	if addr == "" {
		t.Skip("FASTEXEC_TEST_ADDR not set (integration test against a real daemon)")
	}
	return addr
}
