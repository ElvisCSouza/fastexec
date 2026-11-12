package mcp

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func argsOf(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("argumentos inválidos: %v (%s)", err, raw)
	}
	return m
}

func TestClientCwdFillsAndResolvesOnLocalHost(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		name, tool, in, key, want string
	}{
		{"cwd ausente vira a pasta do cliente", "exec", `{"host":"local","command":"dir /b"}`, "cwd", cwd},
		{"cwd relativo parte da pasta do cliente", "exec", `{"host":"local","command":"dir","cwd":"src"}`, "cwd", filepath.Join(cwd, "src")},
		{"job também", "job", `{"host":"local","command":"x"}`, "cwd", cwd},
		{"path relativo de read_file", "read_file", `{"host":"local","path":"a/b.go"}`, "path", filepath.Join(cwd, "a", "b.go")},
		{"repo_path relativo de code_graph", "code_graph", `{"host":"local","repo_path":"pkg"}`, "repo_path", filepath.Join(cwd, "pkg")},
		{"path absoluto fica como está", "read_file", `{"host":"local","path":"` + filepath.ToSlash(filepath.Join(cwd, "x.go")) + `"}`, "path", filepath.ToSlash(filepath.Join(cwd, "x.go"))},
		{"caminho de Git Bash fica como está", "read_file", `{"host":"local","path":"/w/Data/x.go"}`, "path", "/w/Data/x.go"},
		{"til fica como está", "read_file", `{"host":"local","path":"~/x.go"}`, "path", "~/x.go"},
	}
	for _, c := range cases {
		out := withClientCwd(c.tool, json.RawMessage(c.in), cwd)
		if got := argsOf(t, out)[c.key]; got != c.want {
			t.Errorf("%s: %s = %q, esperado %q", c.name, c.key, got, c.want)
		}
	}
}

func TestClientCwdLeavesRemoteHostsAndUnknownToolsAlone(t *testing.T) {
	cwd := t.TempDir()
	for _, c := range []struct{ tool, in string }{
		{"exec", `{"host":"ssh:srv","command":"ls"}`},
		{"read_file", `{"host":"ssh:srv","path":"a.go"}`},
		{"tool_manage", `{"host":"local","path":"a.go"}`},
		{"read_file", `{"host":"local"}`}, // path ausente é erro do chamador: segue como veio
	} {
		out := withClientCwd(c.tool, json.RawMessage(c.in), cwd)
		if string(out) != c.in {
			t.Errorf("%s %s mudou para %s", c.tool, c.in, out)
		}
	}
	in := json.RawMessage(`{"host":"local","command":"dir"}`)
	if out := withClientCwd("exec", in, ""); string(out) != string(in) {
		t.Errorf("sem pasta do cliente nada deve mudar: %s", out)
	}
}

func TestOnlyClaudeCodeGetsTheClientCwd(t *testing.T) {
	for name, want := range map[string]bool{"claude-code": true, "claude-code-ide": true, "claude-ai": false, "": false, "Cursor": false, "antigravity-client": false} {
		if got := isClaudeCodeClient(name); got != want {
			t.Errorf("isClaudeCodeClient(%q) = %v, esperado %v", name, got, want)
		}
	}
}
