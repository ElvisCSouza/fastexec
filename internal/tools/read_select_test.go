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
