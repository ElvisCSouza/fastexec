package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/codescan"
)

// Graph first: reading a big source file (or a giant line range of one) returns
// its outline (every declaration with start-end lines) plus the header, instead
// of the whole text. The decision is per file and deterministic, never per
// repository (a git repo may hold only documents):
//
//  1. the extension is a language the scanner understands (codescan.LangFor);
//  2. the requested text is big (> graphMinLines lines);
//  3. the outline has real structure (>= graphMinDecls declarations in it), so a
//     linear script without functions comes back as text.
//
// Nothing is lost: repeating the exact same call within graphRepeatWindow
// returns the text, still bounded by the read cap (read_cap.go). edit_file and code_outline read through
// handleReadFileInner and are never affected.
//
// Identity rule: anything that withholds content keys its escape by the request
// only (host, path, range, search), never by caller or conversation. Several
// conversations can share one MCP channel (Claude Desktop sends no conversation
// id), and a key per identity would let one conversation consume another's
// repeat. With request keys a shared channel can only cause more full text,
// never less.

const (
	graphMinLines     = 120
	graphMinDecls     = 3
	graphHeaderMax    = 40
	graphMaxEntries   = 300
	graphRepeatWindow = 10 * time.Minute
)

// Markers seen by the telemetry (DispatchTool) to record the decision.
const (
	GraphMarkOutline = "[FastExec Graph] outline"
	GraphMarkRepeat  = "[FastExec Graph] repeat"
	GraphMarkSearch  = "[FastExec Graph] search"
	// GraphMarkRaw: the command asked for the raw search (--raw / raw:) and the graph stepped aside. It is a
	// decision of its own (the agent did not have to work around anything).
	GraphMarkRaw = "[FastExec Graph] raw"
	// GraphMarkIndex: an automatic reindex (or the fallback when the pass does not fit the time budget). It is an
	// announcement, not a decision: the answer may still be the raw search.
	GraphMarkIndex = "[FastExec Graph] index"
)

var (
	graphSeenMu sync.Mutex
	graphSeen   = map[string]time.Time{}
)

// graphRepeat records a graph-first answer for key and reports whether the same
// request was already answered that way within the window (then: deliver raw).
// It does not toggle: once repeated, every same request inside the window gets
// the full text, whoever sends it (see the identity rule above).
func graphRepeat(key string) bool {
	now := time.Now()
	graphSeenMu.Lock()
	defer graphSeenMu.Unlock()
	if len(graphSeen) > 2000 {
		for k, t := range graphSeen {
			if now.Sub(t) >= graphRepeatWindow {
				delete(graphSeen, k)
			}
		}
		if len(graphSeen) > 2000 {
			graphSeen = map[string]time.Time{}
		}
	}
	if t, ok := graphSeen[key]; ok && now.Sub(t) < graphRepeatWindow {
		return true
	}
	graphSeen[key] = now
	return false
}

// graphFirstRead decides and builds the outline answer. full is the whole file,
// from/to the requested lines (to = 0 means the whole file). ok=false: return
// the normal content (repeatNote, when set, explains why the full text came).
func graphFirstRead(args ReadFileArgs, full string, from, to int) (out string, ok bool, repeatNote string) {
	lang := codescan.LangFor(args.Path)
	if lang == nil {
		return "", false, ""
	}
	lines := splitLines(full)
	total := len(lines)
	if from <= 0 {
		from = 1
	}
	if to <= 0 || to > total {
		to = total
	}
	if to-from+1 <= graphMinLines {
		return "", false, ""
	}
	decls := outlineDecls(args.Path, []byte(full), lang)
	var in []codescan.Decl
	for _, d := range decls {
		end := d.EndLine
		if end < d.Line {
			end = d.Line
		}
		if end >= from && d.Line <= to {
			in = append(in, d)
		}
	}
	if len(in) < graphMinDecls {
		return "", false, ""
	}

	key := fmt.Sprintf("read|%s|%s|%d-%d", strings.ToLower(args.Host), strings.ToLower(args.Path), from, to)
	if graphRepeat(key) {
		return "", false, GraphMarkRepeat + ": text returned (up to the read cap) because this exact read was repeated."
	}

	var b strings.Builder
	what := "the whole file"
	if from != 1 || to != total {
		what = fmt.Sprintf("lines %d-%d", from, to)
	}
	sliceKB := len(strings.Join(lines[from-1:to], "\n")) / 1024
	fmt.Fprintf(&b, "%s: %s has %d lines and %d declarations in %s (%d KB). Showing the outline instead of the text. Next: read_file(path, start_line=A, end_line=B) for the part you need (several at once: ranges='A-B,C-D'), or repeat this exact call to get %s.\n",
		GraphMarkOutline, args.Path, total, len(in), what, sliceKB, what)

	if from == 1 {
		hdrEnd := graphHeaderMax
		if in[0].Line-1 < hdrEnd {
			hdrEnd = in[0].Line - 1
		}
		if hdrEnd > 0 {
			b.WriteString("--- header ---\n")
			b.WriteString(renderRanges(lines, []lineRange{{1, hdrEnd}}, true))
		}
	}
	b.WriteString("--- outline ---\n")
	for i, d := range in {
		if i == graphMaxEntries {
			fmt.Fprintf(&b, "... %d more declarations (use code_outline(path, symbol='name'))\n", len(in)-graphMaxEntries)
			break
		}
		if d.EndLine > d.Line {
			fmt.Fprintf(&b, "L%d-L%d: %s\n", d.Line, d.EndLine, d.Sig)
		} else {
			fmt.Fprintf(&b, "L%d: %s\n", d.Line, d.Sig)
		}
	}
	return strings.TrimRight(b.String(), "\n"), true, ""
}
