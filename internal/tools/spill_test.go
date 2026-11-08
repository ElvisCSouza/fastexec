package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestShapeOrCapSpillsAndKeepsTail(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	var b strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&b, "line %d some build output text\n", i)
	}
	b.WriteString("FATAL: the real error is here")
	in := b.String()

	out := shapeOrCap(in)
	if len(out) >= len(in)/2 || len(out) > execSoftCapDefault+1024 {
		t.Fatalf("not shaped: %d -> %d", len(in), len(out))
	}
	if !strings.HasPrefix(out, "line 1 ") || !strings.HasSuffix(out, "FATAL: the real error is here") {
		t.Fatal("head and tail must survive")
	}
	if !IsTruncated(out) || !strings.Contains(out, "read_file(path, pattern=") {
		t.Fatalf("missing marker/hint: %q", out[:200])
	}
	// the spill file holds the full text
	i := strings.Index(out, "Full output: ")
	path := out[i+len("Full output: "):]
	path = path[:strings.Index(path, " — ")]
	got, err := os.ReadFile(path)
	if err != nil || string(got) != in {
		t.Fatalf("spill file wrong: %v", err)
	}
}

func TestShapeOrCapSmallAndDisabled(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	if got := shapeOrCap("short"); got != "short" {
		t.Fatalf("small output changed: %q", got)
	}
	big := strings.Repeat("x\n", 20000)
	t.Setenv("FASTEXEC_NO_SHAPE", "1")
	if got := shapeOrCap(big); got != big {
		t.Fatal("FASTEXEC_NO_SHAPE must disable shaping")
	}
}
