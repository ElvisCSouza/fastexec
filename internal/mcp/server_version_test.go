package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"homelab/fastexec/internal/telemetry"
)

// initializeLine roda uma requisição initialize contra s e devolve a resposta exata escrita no fio.
func initializeLine(t *testing.T, s *Server) []byte {
	t.Helper()
	var buf bytes.Buffer
	s.writer = bufio.NewWriter(&buf)
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"tester","version":"1"}}}` + "\n"
	if err := s.serve(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serverInfoVersionOf extrai serverInfo.version da resposta initialize.
func serverInfoVersionOf(t *testing.T, line []byte) string {
	t.Helper()
	var resp struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("initialize response is not valid JSON: %v\n%s", err, line)
	}
	return resp.Result.ServerInfo.Version
}

// TestInitializeReportsInjectedVersion: o serverInfo.version é a versão injetada no ponto de inicialização, não um
// valor fixo. Mesma versão usada pela identidade do processo (app_version).
func TestInitializeReportsInjectedVersion(t *testing.T) {
	s := &Server{version: "2.0.1-test"}
	if got := serverInfoVersionOf(t, initializeLine(t, s)); got != "2.0.1-test" {
		t.Fatalf("serverInfo.version = %q, want the injected 2.0.1-test", got)
	}
}

// TestInitializeFallsBackToUnknownWithoutVersion: sem versão capturada (Server zero-valor, só em testes) o initialize
// anuncia o sentinel "unknown" da identidade — nunca vazio nem um número fixo.
func TestInitializeFallsBackToUnknownWithoutVersion(t *testing.T) {
	s := &Server{}
	if got := serverInfoVersionOf(t, initializeLine(t, s)); got != telemetry.Unknown {
		t.Fatalf("serverInfo.version = %q, want sentinel %q", got, telemetry.Unknown)
	}
}

// TestServerVersionMatchesTelemetryIdentity: a fonte é única — o que o initialize anuncia é o que a identidade de
// telemetria registra (cli.Main injeta cli.Version via SetBuildVersion antes de NewServer).
func TestServerVersionMatchesTelemetryIdentity(t *testing.T) {
	if got, want := (&Server{version: telemetry.BuildVersion()}).serverVersion(), telemetry.BuildVersion(); got != want {
		t.Fatalf("serverInfo.version = %q, want the process identity version %q", got, want)
	}
}
