package tools

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// minified one-line JSON array of records of a given size; the record under test is #50 of 100
func recordsLine(recSize int) (line, rec string, idMatch, midMatch string) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 100; i++ {
		pad := recSize - 90
		if pad < 0 {
			pad = 0
		}
		r := fmt.Sprintf(`{"id":"ORD-%04d","customer":{"name":"Cliente %d","email":"cli%d@x.com"},"note":"%s"}`, i, i, i, strings.Repeat("n", pad))
		if i == 50 {
			rec = r
		}
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(r)
	}
	b.WriteString("]")
	return b.String(), rec, "ORD-0050", "cli50@x.com"
}

// fixedWindow is the first version of the fix (300 before, 700 after), kept to measure what the structural window gains.
func fixedWindow(l string, ms, me int) (int, int) {
	return max(ms-winBefore, 0), min(me+winAfter, len(l))
}

// TestWindowRisk measures, per record size, whether the window returned around a match holds the WHOLE record
// (the thing an agent needs) and how many bytes it costs. Run with -v to see the table.
func TestWindowRisk(t *testing.T) {
	t.Logf("%-8s %-6s | fixed: complete  bytes | structural: complete  bytes", "record", "hit")
	for _, size := range []int{200, 600, 1500, 3000, 4000, 6000, 12000} {
		line, rec, idHit, midHit := recordsLine(size)
		for _, hit := range []struct{ name, text string }{{"id", idHit}, {"field", midHit}} {
			re := regexp.MustCompile(regexp.QuoteMeta(hit.text))
			loc := re.FindStringIndex(line)
			recStart := strings.Index(line, rec)
			recEnd := recStart + len(rec)
			fa, fb := fixedWindow(line, loc[0], loc[1])
			sa, sb, kind := matchWindow(line, loc[0], loc[1])
			fixedOK := fa <= recStart && fb >= recEnd
			structOK := sa <= recStart && sb >= recEnd
			t.Logf("%-8d %-6s | %-14v %6d | %-9v %6d (%s)", len(rec), hit.name, fixedOK, fb-fa, structOK, sb-sa, kind)
			if len(rec) <= maxRecordBytes-200 && !structOK {
				t.Errorf("record of %d bytes (%s hit) not returned whole: window %d-%d vs record %d-%d", len(rec), hit.name, sa, sb, recStart, recEnd)
			}
			if sb-sa > maxRecordBytes+winBefore+winAfter {
				t.Errorf("window too big: %d", sb-sa)
			}
		}
	}
}

func TestWindowStringsWithBraces(t *testing.T) {
	line := strings.Repeat("x", 3000) + `{"a":"}{\"}","needle":"here","b":{"c":1}}` + strings.Repeat("y", 3000)
	loc := regexp.MustCompile("needle").FindStringIndex(line)
	a, b, kind := matchWindow(line, loc[0], loc[1])
	// the backward scan does not know about strings, so braces inside them may defeat the structural window; the
	// contract is then to degrade to the fixed window, never to return less than the match or more than the cap
	if a > loc[0] || b < loc[1] || b-a > maxRecordBytes+winBefore+winAfter {
		t.Fatalf("bad window %d-%d (%s) for match at %d", a, b, kind, loc[0])
	}
}
