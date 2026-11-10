package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func numberedLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d of the file\n", i)
	}
	return b.String()
}

// The trapdoor: the same whole-file request, repeated, must never return more than the cap.
func TestReadCapHasNoRepeatEscape(t *testing.T) {
	p := writeTemp(t, "big.txt", numberedLines(8000)) // ~160 KB
	for i := 0; i < 4; i++ {
		out, err := HandleReadFile(ReadFileArgs{Path: p})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > readWholeCap+1024 {
			t.Fatalf("read %d returned %d bytes, above the cap", i+1, len(out))
		}
		if !strings.Contains(out, "start_line=") {
			t.Fatalf("read %d has no continuation call: %q", i+1, out[len(out)-200:])
		}
	}
}

// Paging by the footer's call must continue exactly where the previous page stopped.
func TestReadCapContinuationIsExact(t *testing.T) {
	p := writeTemp(t, "big.txt", numberedLines(8000))
	out1, _ := HandleReadFile(ReadFileArgs{Path: p})
	var last, next int
	if _, err := fmt.Sscanf(out1[strings.Index(out1, "Showing lines "):], "Showing lines 1-%d", &last); err != nil {
		t.Fatalf("cannot read the shown range: %v", err)
	}
	if !strings.Contains(out1, fmt.Sprintf("line %d of the file", last)) || strings.Contains(out1, fmt.Sprintf("line %d of the file\n", last+1)) {
		t.Fatalf("page 1 does not end at line %d", last)
	}
	next = last + 1
	if !strings.Contains(out1, fmt.Sprintf("start_line=%d)", next)) {
		t.Fatalf("footer does not point to start_line=%d", next)
	}
	out2, err := HandleReadFile(ReadFileArgs{Path: p, StartLine: next})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out2, fmt.Sprintf("line %d of the file", next)) {
		t.Fatalf("page 2 does not start at line %d: %q", next, out2[:40])
	}
}

// ranges, pattern and byte offset used to bypass the cap (38k tokens from ranges=1-70 on long lines).
func TestReadCapAppliesToEveryMode(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 70; i++ {
		b.WriteString(strings.Repeat("x", 3000))
		b.WriteString("\n")
	}
	p := writeTemp(t, "long.json", b.String()) // 70 lines, 210 KB
	cases := map[string]ReadFileArgs{
		"ranges":   {Path: p, Ranges: "1-70"},
		"pattern":  {Path: p, Pattern: "x", Context: 1},
		"lines":    {Path: p, StartLine: 1, EndLine: 60},
		"byteread": {Path: p, Offset: 0, Limit: 200000},
	}
	for name, a := range cases {
		out, err := HandleReadFile(a)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(out) > readWholeCap+1024 {
			t.Errorf("%s: %d bytes, above the cap", name, len(out))
		}
		if !strings.Contains(out, "[FastExec]") {
			t.Errorf("%s: no cap footer", name)
		}
	}
	out, _ := HandleReadFile(ReadFileArgs{Path: p, Offset: 0, Limit: 200000})
	if !strings.Contains(out, "offset=") {
		t.Errorf("byte read footer has no offset continuation: %q", out[len(out)-160:])
	}
}

func TestReadCapSingleHugeLineKeepsUTF8(t *testing.T) {
	p := writeTemp(t, "min.html", strings.Repeat("ação—", 40000)) // one 280 KB line, multi-byte runes
	out, err := HandleReadFile(ReadFileArgs{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(out) {
		t.Fatal("cut inside a rune")
	}
	if !strings.Contains(out, "One line is longer than the read cap") {
		t.Fatalf("missing long-line footer: %q", out[len(out)-200:])
	}
}

func TestReadCapKeepsSmallReadsUntouched(t *testing.T) {
	p := writeTemp(t, "small.txt", numberedLines(50))
	out, _ := HandleReadFile(ReadFileArgs{Path: p})
	if strings.Contains(out, "[FastExec]") || out != numberedLines(50) {
		t.Fatalf("small read was changed: %q", out[:60])
	}
}

func TestReadCapAddsStructureForMarkdown(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&b, "## Section %d\n%s\n", i, strings.Repeat("text text text text\n", 8))
	}
	p := writeTemp(t, "doc.md", b.String())
	out, _ := HandleReadFile(ReadFileArgs{Path: p})
	if !strings.Contains(out, "--- structure") || !strings.Contains(out, "## Section 2") {
		t.Fatalf("no structure index in the capped markdown read")
	}
}

// Reading one file in many ranges is normal work, not re-reading: only the same read repeated earns the hint.
func TestRereadHintOnlyForTheSameRead(t *testing.T) {
	p := writeTemp(t, "f.txt", numberedLines(400))
	for i := 0; i < 6; i++ {
		out, _ := HandleReadFile(ReadFileArgs{Path: p, StartLine: 1 + i*50, EndLine: 50 + i*50, Caller: "hint-test#1"})
		if strings.Contains(out, "[FastExec Hint]") {
			t.Fatalf("range %d got the re-read hint", i+1)
		}
	}
	var last string
	for i := 0; i < 3; i++ {
		last, _ = HandleReadFile(ReadFileArgs{Path: p, StartLine: 10, EndLine: 20, Caller: "hint-test#1"})
	}
	if !strings.Contains(last, "[FastExec Hint] This exact read was made 3 times") {
		t.Fatalf("third identical read has no hint: %q", last)
	}
}

// Remote and local share selectFromText: every selection parameter must be honoured (a remote ranges=1-80 once
// returned the whole file).
func TestSelectFromTextHonoursEverySelector(t *testing.T) {
	data := numberedLines(100)
	if out, _ := selectFromText("f.txt", data, ReadFileArgs{Ranges: "2-3"}); !strings.Contains(out, "line 2 of") || strings.Contains(out, "line 4 of") {
		t.Errorf("ranges ignored: %q", out)
	}
	if out, _ := selectFromText("f.txt", data, ReadFileArgs{Pattern: "line 77 "}); !strings.Contains(out, "line 77 of") || strings.Contains(out, "line 1 of") {
		t.Errorf("pattern ignored: %q", out)
	}
	if out, _ := selectFromText("f.txt", data, ReadFileArgs{Offset: 5, Limit: 4}); out != data[5:9] {
		t.Errorf("offset/limit ignored: %q", out)
	}
	if out, _ := selectFromText("f.txt", data, ReadFileArgs{StartLine: 10, EndLine: 11}); out != "line 10 of the file\nline 11 of the file" {
		t.Errorf("start/end ignored: %q", out)
	}
}
