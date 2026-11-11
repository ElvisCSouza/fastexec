package tools

import (
	"fmt"
	"strings"
	"testing"
)

func TestPatternDigestOnRepetitiveLog(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		if i%10 == 0 {
			fmt.Fprintf(&b, "2026-11-10 00:%02d:%02d ERROR DB timeout\n", i/60%60, i%60)
		} else if i%25 == 1 {
			fmt.Fprintf(&b, "2026-11-10 00:%02d:%02d ERROR token invalido\n", i/60%60, i%60)
		} else {
			fmt.Fprintf(&b, "2026-11-10 00:00:00 INFO ok worker=%d\n", i%2)
		}
	}
	out, handled, err := selectLines(b.String(), ReadFileArgs{Pattern: "ERROR"})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	for _, want := range []string{"distinct messages", "200x lines 1..", "DB timeout", "token invalido", "first: 2026-11-10 00:00:00 ERROR DB timeout"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q em:\n%s", want, out)
		}
	}
	if len(out) > 700 {
		t.Errorf("resumo grande demais: %d bytes", len(out))
	}
}

func TestPatternNoDigestWhenDistinctOrFew(t *testing.T) {
	// código: 100 linhas casadas, todas distintas (não são repetição) → linhas por inteiro
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "func Handler%s() {}\n", strings.Repeat("x", i+1))
	}
	out, _, _ := selectLines(b.String(), ReadFileArgs{Pattern: "Handler"})
	if strings.Contains(out, "distinct messages") {
		t.Errorf("resumiu linhas distintas:\n%.200s", out)
	}
	// poucas linhas casadas: sem resumo
	out, _, _ = selectLines(strings.Repeat("ERROR a\n", 20), ReadFileArgs{Pattern: "ERROR"})
	if strings.Contains(out, "distinct messages") {
		t.Errorf("resumiu poucas linhas")
	}
}
