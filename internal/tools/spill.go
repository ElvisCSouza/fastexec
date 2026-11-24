package tools

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/corpus"
	"homelab/fastexec/internal/telemetry"
)

// Native output shaper for exec ("our RTK").
//
// Outputs above a soft cap are cut to head + tail (errors and summaries are
// usually at the end), the full text is saved to a spill file and the footer
// says how to read the omitted part cheaply. exec only: read_file has its own
// rules because there the model asked for the file on purpose.

const (
	execSoftCapDefault = 10 * 1024 // ~3k tokens (bytes/3.5)
	spillHeadShare     = 0.3       // of the cap; the tail gets the rest
	spillMaxAge        = 24 * time.Hour
)

var spillCleanOnce sync.Once

func execSoftCap() int {
	if v := os.Getenv("FASTEXEC_EXEC_SOFT_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1024 {
			return n
		}
	}
	return execSoftCapDefault
}

func spillDir() string {
	if v := os.Getenv("FASTEXEC_SPILL_DIR"); v != "" {
		_ = os.MkdirAll(v, 0o755)
		return v
	}
	return config.Get().Dir("spill")
}

func cleanSpill(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && time.Since(info.ModTime()) > spillMaxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// shapeOrCap shapes big exec output; anything it cannot save falls back to
// the plain capOutput so no information is dropped silently.
func shapeOrCap(s string) string { return shapeOrCapFor("", s) }

func shapeOrCapFor(id, s string) string {
	out, info := shapeOrCapInner(id, s)
	if info.cut() {
		telemetry.NoteCut(len(s), len(out), info.note(), info.detail())
	}
	return out
}

// shapeOrCapInner returns the text to show and what was left out (zero cutInfo when nothing was cut).
func shapeOrCapInner(id, s string) (string, cutInfo) {
	if os.Getenv("FASTEXEC_NO_SHAPE") != "" || outputFilterOff() || abOff("cut", id) || len(s) <= execSoftCap() {
		return capOutputInfo(s)
	}
	if out, info, ok := shapeBig(s, execSoftCap()); ok {
		return out, info
	}
	return capOutputInfo(s)
}

// cutInfo says what a cut left out. It is the source of the footer and of the telemetry record.
type cutInfo struct {
	origLines, origBytes int
	retLines, retBytes   int
	reason               string // "teto": the output was over a cap
	filter               string // "head+tail", "head only" or "byte cut (one line)"
	cursor               string // exact call that brings back the omitted part
	spill                string // path of the redacted full text; empty when nothing was spilled
}

func (c cutInfo) cut() bool { return c.origBytes > c.retBytes }

func (c cutInfo) note() string {
	return fmt.Sprintf("raw output had %d lines; returned %d (%s, %s); the rest left out",
		c.origLines, c.retLines, c.reason, c.filter)
}

func (c cutInfo) detail() telemetry.CutDetail {
	return telemetry.CutDetail{
		OrigLines: c.origLines, OrigBytes: c.origBytes,
		RetLines: c.retLines, RetBytes: c.retBytes,
		OmitLines: c.origLines - c.retLines, OmitBytes: c.origBytes - c.retBytes,
		Reason: c.reason, Filter: c.filter, Cursor: c.cursor, Spill: c.spill,
	}
}

// shapeBig keeps the head and tail of s within softCap bytes. The full text goes to a spill file
// after the same secret redaction the telemetry uses (line structure kept, so the cursor line
// numbers still match). The footer says what was left out and how to read it.
// saveSpill writes the redacted full text of an output to the spill directory and returns its path.
// ok is false when no spill can be written; callers then show what they can without one, so no
// information is dropped silently.
func saveSpill(s string) (string, bool) {
	dir := spillDir()
	if dir == "" {
		return "", false
	}
	spillCleanOnce.Do(func() { cleanSpill(dir) })

	redacted := corpus.RedactKeepLines(s, corpus.DefaultRedactors())
	sum := sha1.Sum([]byte(redacted))
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(sum[:4])+".txt")
	if err := os.WriteFile(path, []byte(redacted), 0o644); err != nil {
		return "", false
	}
	return path, true
}

// spillBytesCursor is the exact read_file call that returns the spill from byte offset off on
// (at most outputCapSize bytes per call). The offset is in redacted coordinates: the spill holds
// the redacted text, so the head that was shown is measured after redaction too.
func spillBytesCursor(path string, off int) string {
	return fmt.Sprintf("read_file('%s', offset=%d, limit=%d)", path, off, outputCapSize)
}

func shapeBig(s string, softCap int) (string, cutInfo, bool) {
	path, ok := saveSpill(s)
	if !ok {
		return "", cutInfo{}, false
	}

	lines := strings.Split(s, "\n")
	headBudget := int(float64(softCap) * spillHeadShare)
	tailBudget := softCap - headBudget

	head, used := 0, 0
	for head < len(lines) && used+len(lines[head])+1 <= headBudget {
		used += len(lines[head]) + 1
		head++
	}
	tail, used := 0, 0
	for tail < len(lines)-head && used+len(lines[len(lines)-1-tail])+1 <= tailBudget {
		used += len(lines[len(lines)-1-tail]) + 1
		tail++
	}
	info := cutInfo{origLines: len(lines), origBytes: len(s), reason: "teto", spill: path}

	// A single enormous line leaves nothing to show: cut by bytes instead.
	if head == 0 && tail == 0 {
		cut := headBudget
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		info.retLines, info.retBytes = 1, cut
		info.filter = "byte cut (one line)"
		info.cursor = "read_file(path, pattern='<text>', context=1)"
		return s[:cut] + "\n" + footer(info, fmt.Sprintf("bytes %d-%d", cut+1, len(s))), info, true
	}

	headText := strings.Join(lines[:head], "\n")
	tailText := strings.Join(lines[len(lines)-tail:], "\n")
	omittedFrom, omittedTo := head+1, len(lines)-tail
	info.retLines = head + tail
	info.retBytes = len(headText) + len(tailText)
	info.filter = "head+tail"
	info.cursor = fmt.Sprintf("read_file(path, ranges='%d-%d')", omittedFrom, min(omittedTo, omittedFrom+80))

	var b strings.Builder
	b.WriteString(headText)
	b.WriteString("\n")
	b.WriteString(footer(info, fmt.Sprintf("lines %d-%d", omittedFrom, omittedTo)))
	b.WriteString("\n")
	b.WriteString(tailText)
	return b.String(), info, true
}

// markCutRecovered is telemetry.MarkCutRecovered, a variable so tests can see which reads mark a spill.
var markCutRecovered = telemetry.MarkCutRecovered

// noteSpillRead records that a local read_file opened a spill file. A remote read never marks a local
// spill, and neither does a read of any other path. Host "" and "local" (any case) are local.
func noteSpillRead(host, path string) bool {
	if !(host == "" || strings.EqualFold(host, "local")) || !isSpillPath(path) {
		return false
	}
	markCutRecovered(path)
	return true
}

// isSpillPath reports whether p is a spill file of the current spill directory (read_file uses it to
// record that a cut was read back).
func isSpillPath(p string) bool {
	if p == "" {
		return false
	}
	return filepath.Dir(filepath.Clean(p)) == filepath.Clean(spillDir())
}

// countLines counts lines the way the footer reports them: a non-empty text with no newline is one line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// footer is the single line that tells the model what was cut and how to get the rest back.
// It starts with truncMarker so IsTruncated keeps recognising it.
func footer(c cutInfo, where string) string {
	omitLines, omitBytes := c.origLines-c.retLines, c.origBytes-c.retBytes
	counts := fmt.Sprintf("Original %d lines / %d bytes; returned %d lines / %d bytes; omitted %d lines / %d bytes. Filter: %s.",
		c.origLines, c.origBytes, c.retLines, c.retBytes, omitLines, omitBytes, c.filter)
	if c.spill == "" {
		return fmt.Sprintf("%s%s omitted (%s). %s Next: narrow the command or pipe it through head, tail or grep and run it again]",
			truncMarker, where, c.reason, counts)
	}
	return fmt.Sprintf("%s%s omitted (%s). %s Spill is redacted: secrets were masked before it was saved. Full output: %s — Next: %s, or read_file(path, pattern='error|fail|panic|exception', context=3)]",
		truncMarker, where, c.reason, counts, c.spill, c.cursor)
}
