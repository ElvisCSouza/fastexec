package tools

// read_cap.go: the size cap shared by every read_file mode (whole file, lines, ranges, pattern, symbol, byte offset)
// and every host. Text above readWholeCap is cut at a line boundary and the footer carries the exact call that
// continues. There is no "repeat the call to get everything" escape: the only way to more text is the next page.

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"homelab/fastexec/internal/telemetry"
)

type readMode int

const (
	modeLines  readMode = iota // whole file or start_line/end_line: continues with start_line
	modeBytes                  // offset/limit: continues with offset
	modeSelect                 // ranges/pattern/symbol: no continuation, the reader narrows the selection
)

func modeOf(a ReadFileArgs) readMode {
	switch {
	case a.Pattern != "" || a.Ranges != "" || a.Symbol != "":
		return modeSelect
	case a.Offset > 0 || a.Limit > 0:
		return modeBytes
	}
	return modeLines
}

// readCall renders the start of a read_file call for the footer (host only when it is not the local one).
func readCall(a ReadFileArgs) string {
	s := fmt.Sprintf("read_file(path='%s'", a.Path)
	if a.Host != "" && a.Host != "local" {
		s += fmt.Sprintf(", host='%s'", a.Host)
	}
	return s
}

// capRead cuts data to readWholeCap bytes (at a line boundary, or at a rune boundary inside one huge line) and says
// how to read on. Disabled with FASTEXEC_NO_READ_CAP=1.
func capRead(data string, args ReadFileArgs) string {
	if len(data) <= readWholeCap || os.Getenv("FASTEXEC_NO_READ_CAP") != "" || abOff("readcap", identity(args.Caller, args.Conv)) {
		return data
	}
	cut := strings.LastIndexByte(data[:readWholeCap], '\n')
	midLine := false
	if cut <= 0 { // a single line longer than the cap (minified JSON/HTML, log line)
		cut = readWholeCap
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		midLine = true
	}
	head := strings.TrimRight(data[:cut], "\r\n")
	kbAll, kbCap := len(data)/1024, cut/1024
	shown := strings.Count(head, "\n") + 1
	moreLines := len(splitLines(data)) - shown
	call := readCall(args)

	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n\n")
	switch mode := modeOf(args); {
	case mode == modeSelect:
		fmt.Fprintf(&b, "[FastExec] Result cut at %d KB of %d KB (read cap). Narrow it: fewer lines in ranges, a tighter pattern or a smaller context.", kbCap, kbAll)
	case mode == modeBytes:
		fmt.Fprintf(&b, "[FastExec] Result cut at %d KB of %d KB (read cap). Next: %s, offset=%d) continues.", kbCap, kbAll, call, args.Offset+int64(cut))
	case midLine:
		fmt.Fprintf(&b, "[FastExec] One line is longer than the read cap (%d KB of %d KB shown). Next: %s, pattern='regex', context=1) for what you need, or %s, offset=%d, limit=%d) for the rest of it.", kbCap, kbAll, call, call, cut, readWholeCap)
	default:
		first := max(args.StartLine, 1)
		last := first + shown - 1
		if moreLines < 0 {
			moreLines = 0
		}
		fmt.Fprintf(&b, "[FastExec] Showing lines %d-%d (%d KB of %d KB, read cap; %d more lines). Next: %s, start_line=%d) continues; or ranges='a-b,c-d' / pattern='regex', context=3 for what you need.", first, last, kbCap, kbAll, moreLines, call, last+1)
		if args.StartLine == 0 && args.EndLine == 0 {
			if idx := textStructure(args.Path, data); idx != "" {
				b.WriteString("\n--- structure (line: text) ---\n")
				b.WriteString(idx)
			}
		}
	}
	out := b.String()
	noteReadCap(len(data), len(out), shown, shown+moreLines)
	return out
}

// noteReadCap anota a economia (medida) de uma leitura cortada no limite: o texto que o limite deixou de devolver.
func noteReadCap(before, after, shownLines, totalLines int) {
	telemetry.NoteSavingDetail(telemetry.CatReadCap, before, after,
		fmt.Sprintf("read cap: returned %d of %d lines, with the call that continues", shownLines, totalLines))
}
