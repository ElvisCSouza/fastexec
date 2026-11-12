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
func shapeOrCap(s string) string {
	out := shapeOrCapInner(s)
	if len(s) > len(out) {
		telemetry.NoteSavingDetail(telemetry.CatCut, len(s), len(out),
			fmt.Sprintf("raw output had %d lines; returned %d (head and tail kept, the rest left out)", savLines(s), savLines(out)))
	}
	return out
}

func shapeOrCapInner(s string) string {
	if os.Getenv("FASTEXEC_NO_SHAPE") != "" || outputFilterOff() || len(s) <= execSoftCap() {
		return capOutput(s)
	}
	if out, ok := shapeBig(s, execSoftCap()); ok {
		return out
	}
	return capOutput(s)
}

func shapeBig(s string, softCap int) (string, bool) {
	dir := spillDir()
	if dir == "" {
		return "", false
	}
	spillCleanOnce.Do(func() { cleanSpill(dir) })

	sum := sha1.Sum([]byte(s))
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(sum[:4])+".txt")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return "", false
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
	// A single enormous line leaves nothing to show: cut by bytes instead.
	if head == 0 && tail == 0 {
		cut := headBudget
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return fmt.Sprintf("%s\n[truncated: %d bytes in one line; only the first %d bytes shown. Full output: %s — Next: read_file(path, pattern='<text>', context=1)]",
			s[:cut], len(s), cut, path), true
	}

	omittedFrom, omittedTo := head+1, len(lines)-tail
	omittedBytes := len(s) - len(strings.Join(lines[:head], "\n")) - len(strings.Join(lines[len(lines)-tail:], "\n"))
	var b strings.Builder
	b.WriteString(strings.Join(lines[:head], "\n"))
	fmt.Fprintf(&b, "\n[truncated: lines %d-%d omitted (%d KB of %d KB). Full output: %s — Next: read_file(path, pattern='error|fail|panic|exception', context=3) or read_file(path, ranges='%d-%d')]\n",
		omittedFrom, omittedTo, omittedBytes/1024, len(s)/1024, path, omittedFrom, min(omittedTo, omittedFrom+80))
	b.WriteString(strings.Join(lines[len(lines)-tail:], "\n"))
	return b.String(), true
}
