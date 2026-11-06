package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"homelab/fastexec/internal/golden"
	"homelab/fastexec/internal/tools"
)

// listToolsLine devolve a linha JSON-RPC exata que o servidor escreveria para tools/list (com o '\n' final).
func listToolsLine(t *testing.T, s *Server) []byte {
	t.Helper()
	var buf bytes.Buffer
	s.writer = bufio.NewWriter(&buf)
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	if err := s.serve(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// toolsListGolden monta o envelope {wire_bytes, result}: wire_bytes é o tamanho em bytes da resposta no fio,
// result é o array de tools que o LLM recebe, indentado para revisão humana do diff.
func toolsListGolden(t *testing.T, line []byte) []byte {
	t.Helper()
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, resp.Result, "  ", "  "); err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf("{\n  \"wire_bytes\": %d,\n  \"result\": %s\n}\n", len(line), pretty.String()))
}

func setModule(t *testing.T, action, module string) {
	t.Helper()
	raw := fmt.Sprintf(`{"action":%q,"module":%q}`, action, module)
	if _, err := tools.HandleToolManage([]byte(raw)); err != nil {
		t.Fatalf("tool_manage %s %s: %v", action, module, err)
	}
}

// TestGoldenToolsList trava o tools/list visto pelo LLM: núcleo, cada módulo sob demanda isolado e todos juntos.
// Regenerar: go test ./internal/mcp -run TestGoldenToolsList -update
func TestGoldenToolsList(t *testing.T) {
	cases := []struct {
		golden  string
		modules []string
	}{
		{"tools_list_core.json", nil},
		{"tools_list_docker.json", []string{"docker"}},
		{"tools_list_sql.json", []string{"sql"}},
		{"tools_list_homelab.json", []string{"homelab"}},
		{"tools_list_all.json", []string{"docker", "sql", "homelab"}},
	}

	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			for _, m := range tc.modules {
				setModule(t, "enable", m)
			}
			t.Cleanup(func() {
				for _, m := range tc.modules {
					setModule(t, "disable", m)
				}
			})

			s := &Server{}
			line := listToolsLine(t, s)
			golden.Check(t, tc.golden, toolsListGolden(t, line))
		})
	}
}
