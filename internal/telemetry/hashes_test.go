package telemetry

import (
	"strings"
	"testing"
)

func TestArgsHashIgnoresInternalFieldsAndKeyOrder(t *testing.T) {
	a := ArgsHash([]byte(`{"command":"ls","host":"noteserver","_caller":"x#1","_conversation":"c1"}`))
	b := ArgsHash([]byte(`{"host":"noteserver","command":"ls","_caller":"y#2"}`))
	if a == "" || a != b {
		t.Fatalf("same call must hash the same: %q vs %q", a, b)
	}
	c := ArgsHash([]byte(`{"command":"ls","host":"noteserver","input":"x"}`))
	if c == a {
		t.Fatal("stdin input must change the hash")
	}
	if len(a) != 16 {
		t.Fatalf("want 16 hex, got %q", a)
	}
}

func TestShortHash(t *testing.T) {
	if ShortHash("") != "" {
		t.Fatal("empty in, empty out")
	}
	if ShortHash("a") == ShortHash("b") {
		t.Fatal("different text, different hash")
	}
}

func TestMarksTemplateIdStable(t *testing.T) {
	o1 := "x\n[FastExec] Showing lines 1-400 (32 KB of 300 KB, read cap; 900 more lines). Next: read_file(path='C:\\a.txt', start_line=401) continues"
	o2 := "y\n[FastExec] Showing lines 1-812 (32 KB of 90 KB, read cap; 12 more lines). Next: read_file(path='/b/c.md', start_line=813) continues"
	m1, m2 := Marks(o1, false), Marks(o2, false)
	if m1 == "" || m1 != m2 {
		t.Fatalf("same template must get the same id: %q vs %q", m1, m2)
	}
	if !strings.HasPrefix(m1, "FastExec:") {
		t.Fatalf("plain marker kind: %q", m1)
	}
	h := Marks("[FastExec Hint] Second edit_file in a row on this file", true)
	if !strings.HasPrefix(h, "args,Hint:") {
		t.Fatalf("got %q", h)
	}
	g := Marks("[FastExec Auto-Remediation] 'cat x' was run as read_file", false)
	if !strings.HasPrefix(g, "Auto-Remediation:") {
		t.Fatalf("got %q", g)
	}
	if Marks("plain output", false) != "" {
		t.Fatal("no marks for plain output")
	}
}

func TestMarksDedupAndLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("[FastExec Guard] blocked 'cmd" + string(rune('a'+i)) + "'\n")
	}
	if got := Marks(b.String(), false); strings.Count(got, ",") != 0 {
		t.Fatalf("same template repeated must appear once: %q", got)
	}
}
