package tools

import (
	"strings"
	"testing"
)

func TestSelectLinesRangesAndPattern(t *testing.T) {
	data := "a\nb\nc\nd\ne\nf\ng\n"

	out, ok, err := selectLines(data, ReadFileArgs{Ranges: "1-2,6-"})
	if err != nil || !ok || out != "1\ta\n2\tb\n...\n6\tf\n7\tg\n" {
		t.Fatalf("ranges: %q %v %v", out, ok, err)
	}

	out, _, _ = selectLines(data, ReadFileArgs{Pattern: "^d$", Context: 1})
	if out != "3\tc\n4\td\n5\te\n" {
		t.Fatalf("pattern: %q", out)
	}

	if _, ok, _ := selectLines(data, ReadFileArgs{}); ok {
		t.Fatal("no selector must fall through")
	}
	if _, _, err := selectLines(data, ReadFileArgs{Ranges: "x-3"}); err == nil {
		t.Fatal("bad range must error")
	}
}

func TestPatternWindowsGiantLine(t *testing.T) {
	filler := strings.Repeat("abcdefghij", 12000) // 120 KB
	data := "head\n" + filler + "NEEDLE-1" + filler + "NEEDLE-2" + filler + "\ntail\n"

	out, ok, err := selectLines(data, ReadFileArgs{Pattern: "NEEDLE-1", Context: 1})
	if err != nil || !ok {
		t.Fatalf("pattern: %v %v", ok, err)
	}
	if !strings.Contains(out, "NEEDLE-1") {
		t.Fatal("window must contain the match")
	}
	if len(out) > 3000 {
		t.Fatalf("giant line not windowed: %d bytes", len(out))
	}
	if !strings.Contains(out, "line is 360016 bytes") && !strings.Contains(out, "line is") {
		t.Fatalf("note missing: %q", out[len(out)-200:])
	}
	if !strings.Contains(out, "head") || !strings.Contains(out, "tail") {
		t.Fatal("short context lines must stay whole")
	}

	// two matches on the line: the other one is reported by byte offset
	out, _, _ = selectLines(data, ReadFileArgs{Pattern: "NEEDLE-\\d", Context: 0})
	if !strings.Contains(out, "other matches at bytes") {
		t.Fatalf("other matches not reported: %q", out[len(out)-200:])
	}

	// no match inside a giant context line: head only
	out, _, _ = selectLines(data, ReadFileArgs{Pattern: "^head$", Context: 1})
	if len(out) > 3000 || !strings.Contains(out, "head shown") {
		t.Fatalf("context giant line not clipped: %d", len(out))
	}

	// short lines untouched
	if got := windowLongLine("short line", nil); got != "short line" {
		t.Fatalf("short line changed: %q", got)
	}
}

func TestCapWholeFile(t *testing.T) {
	small := strings.Repeat("x\n", 100)
	if capWholeFile(small, "f") != small {
		t.Fatal("small file must pass untouched")
	}
	big := strings.Repeat("0123456789\n", 5000)
	out := capWholeFile(big, "f")
	if len(out) >= len(big) || !strings.Contains(out, "[FastExec] File has 5000 lines") {
		t.Fatalf("big file not capped: %d", len(out))
	}
}
