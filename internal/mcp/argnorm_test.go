package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/tools"
)

func norm(t *testing.T, tool, args string) (json.RawMessage, []string, error) {
	t.Helper()
	return normalizeCall(tool, json.RawMessage(args))
}

// The payload Antigravity sent: three of its keys were dropped by the decoder and index.html came back whole.
func TestNormalizeAntigravityReadFile(t *testing.T) {
	out, notes, err := norm(t, "read_file", `{"ByteOffset":0,"EndLine":120,"Path":"W:\\x\\index.html","StartLine":15}`)
	if err != nil {
		t.Fatal(err)
	}
	var a tools.ReadFileArgs
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	if a.StartLine != 15 || a.EndLine != 120 || a.Path != `W:\x\index.html` {
		t.Fatalf("keys not read: %+v", a)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "StartLine -> start_line") || !strings.Contains(notes[0], "ByteOffset -> offset") {
		t.Fatalf("notes: %v", notes)
	}
}

func TestNormalizeExecTargetIsHost(t *testing.T) {
	out, _, err := norm(t, "exec", `{"command":"dir","target":"local"}`)
	if err != nil {
		t.Fatal(err)
	}
	var a tools.ExecArgs
	_ = json.Unmarshal(out, &a)
	if a.Host != "local" || a.Command != "dir" {
		t.Fatalf("target not read as host: %+v", a)
	}
	out, _, _ = norm(t, "exec", `{"cmd":"dir"}`)
	_ = json.Unmarshal(out, &a)
	if a.Command != "dir" {
		t.Fatalf("cmd not read as command: %+v", a)
	}
}

func TestNormalizeReadFileSpellings(t *testing.T) {
	for _, in := range []string{`{"file_path":"a.txt"}`, `{"target":"a.txt"}`, `{"FilePath":"a.txt"}`} {
		out, _, err := norm(t, "read_file", in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var a tools.ReadFileArgs
		_ = json.Unmarshal(out, &a)
		if a.Path != "a.txt" {
			t.Errorf("%s: path = %q", in, a.Path)
		}
	}
	out, _, _ := norm(t, "read_file", `{"path":"a","range":"10-20"}`)
	var a tools.ReadFileArgs
	_ = json.Unmarshal(out, &a)
	if a.Ranges != "10-20" {
		t.Errorf("range not read as ranges: %+v", a)
	}
}

func TestNormalizeFileOpsAndSQL(t *testing.T) {
	out, _, _ := norm(t, "file_ops", `{"operation":"list","path":"."}`)
	var f tools.FileOpsArgs
	_ = json.Unmarshal(out, &f)
	if f.Action != "list" {
		t.Errorf("operation not read as action: %+v", f)
	}
	if _, _, err := norm(t, "file_ops", `{"action":"copy","path":"a","target":"b"}`); err != nil {
		t.Errorf("file_ops has a real 'target' field: %v", err)
	}
}

func TestNormalizeCanonicalIsUntouched(t *testing.T) {
	in := `{"path":"a","start_line":1,"end_line":5,"_caller":"c#1","_conversation":"x"}`
	out, notes, err := norm(t, "read_file", in)
	if err != nil || len(notes) != 0 || string(out) != in {
		t.Fatalf("canonical call changed: %s %v %v", out, notes, err)
	}
	in = `{"path":"p","find":"a","replace":"b"}` // edit_file synonyms handled by its own decoder
	if out, notes, err := norm(t, "edit_file", in); err != nil || len(notes) != 0 || string(out) != in {
		t.Fatalf("edit_file synonyms changed: %s %v %v", out, notes, err)
	}
}

func TestNormalizeUnknownKeyIsRefused(t *testing.T) {
	_, _, err := norm(t, "read_file", `{"path":"x","StartLin":1}`)
	if err == nil {
		t.Fatal("unknown key was accepted")
	}
	for _, want := range []string{"unknown parameter", "'StartLin'", "Valid:", "start_line", "Did you mean 'start_line'", "Nothing was run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, _, err := norm(t, "job", `{"action":"start","command":"x","timeout_sec":5}`); err == nil {
		t.Error("job timeout_sec (not a parameter) was accepted")
	}
}

func TestNormalizeMetadataIsIgnoredAndSaid(t *testing.T) {
	out, notes, err := norm(t, "exec", `{"command":"dir","description":"list"}`)
	if err != nil || !strings.Contains(strings.Join(notes, ";"), "'description' (metadata)") {
		t.Fatalf("metadata: %v %v", notes, err)
	}
	if strings.Contains(string(out), "description") {
		t.Fatalf("metadata key kept: %s", out)
	}
}

func TestNormalizeAliasDoesNotOverrideExplicit(t *testing.T) {
	out, notes, _ := norm(t, "read_file", `{"path":"a","file_path":"b"}`)
	var a tools.ReadFileArgs
	_ = json.Unmarshal(out, &a)
	if a.Path != "a" || !strings.Contains(strings.Join(notes, ";"), "'file_path' (path was already given)") {
		t.Fatalf("alias overrode the explicit key: %+v %v", a, notes)
	}
}

func TestNormalizeLenientToolsPass(t *testing.T) {
	in := `{"remote_path":"/a","whatever":1}`
	if out, _, err := norm(t, "get_file", in); err != nil || string(out) != in {
		t.Fatalf("get_file is lenient: %s %v", out, err)
	}
	if out, _, err := norm(t, "some_module_tool", in); err != nil || string(out) != in {
		t.Fatalf("untyped tool changed: %s %v", out, err)
	}
}

func TestNormalizeLenientEnvDowngradesToNote(t *testing.T) {
	t.Setenv("FASTEXEC_STRICT_ARGS", "0")
	out, notes, err := norm(t, "read_file", `{"path":"a","bogus":1}`)
	if err != nil || strings.Contains(string(out), "bogus") || !strings.Contains(strings.Join(notes, ";"), "'bogus' (unknown)") {
		t.Fatalf("lenient mode: %s %v %v", out, notes, err)
	}
}

// End to end through DispatchTool: the Antigravity read of lines 2-3 returns lines 2-3 (not the file) and says what
// it read; an unknown key runs nothing.
func TestDispatchReadWithPascalCaseKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"Path": p, "StartLine": 2, "EndLine": 3, "ByteOffset": 0})
	res := DispatchTool("read_file", args)
	text := res.Content[0].Text
	if res.IsError || !strings.HasPrefix(text, "l2\nl3") || strings.Contains(text, "l4") {
		t.Fatalf("range not honoured: %q", text)
	}
	if !strings.Contains(text, "[FastExec] Parameters adjusted") || !strings.Contains(text, "StartLine -> start_line") {
		t.Fatalf("model was not told: %q", text)
	}
	bad, _ := json.Marshal(map[string]any{"path": p, "Lines": "1-2"})
	if res := DispatchTool("read_file", bad); !res.IsError || !strings.Contains(res.Content[0].Text, "unknown parameter") {
		t.Fatalf("unknown key not refused: %+v", res)
	}
}
