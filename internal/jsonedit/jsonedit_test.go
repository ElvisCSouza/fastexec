package jsonedit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSetKeepsOrderAndValues(t *testing.T) {
	in := `{"z": 1, "mcpServers": {"b": {"command": "x"}, "a": {"command": "y", "env": {"K": "1.50"}}}, "big": 12345678901234567890}`
	out, err := Set([]byte(in), map[string]any{"command": `C:\fx\fastexec.exe`, "args": []string{}}, "mcpServers", "fastexec")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"big": 12345678901234567890`, `"K": "1.50"`, `C:\\fx\\fastexec.exe`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in\n%s", want, s)
		}
	}
	if strings.Index(s, `"z"`) > strings.Index(s, `"mcpServers"`) || strings.Index(s, `"b"`) > strings.Index(s, `"a"`) || strings.Index(s, `"a"`) > strings.Index(s, `"fastexec"`) {
		t.Fatalf("order changed:\n%s", s)
	}
	v, ok, err := Get(out, "mcpServers", "fastexec", "command")
	var cmd string
	_ = json.Unmarshal(v, &cmd)
	if err != nil || !ok || cmd != `C:\fx\fastexec.exe` {
		t.Fatalf("Get: %q %v %v", cmd, ok, err)
	}
}

func TestSetCreatesAndDeletes(t *testing.T) {
	out, err := Set(nil, []string{"Bash"}, "permissions", "deny")
	if err != nil || !strings.Contains(string(out), `"deny": [`) {
		t.Fatalf("%s %v", out, err)
	}
	out, err = Set([]byte(`{"mcpServers": null}`), map[string]string{"command": "x"}, "mcpServers", "fastexec")
	if err != nil || !strings.Contains(string(out), `"fastexec"`) {
		t.Fatalf("null parent: %s %v", out, err)
	}
	out, err = Set(out, nil, "mcpServers", "fastexec")
	if err != nil || strings.Contains(string(out), "fastexec") {
		t.Fatalf("delete: %s %v", out, err)
	}
}

func TestRejectsNonObject(t *testing.T) {
	for _, in := range []string{`[1]`, `{"a":1} x`, `{"a":`} {
		if _, err := Set([]byte(in), 1, "k"); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
	if _, err := Set([]byte(`{"a": 5}`), 1, "a", "b"); err == nil {
		t.Error("expected error setting inside a number")
	}
}
