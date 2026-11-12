package lsp

import (
	"fmt"
	"strconv"
	"strings"
)

// Reverse patches are written to <data_dir>/undo/<timestamp>.patch and are what
// makes an applied WorkspaceEdit undoable. The format is a unified diff, so it
// is readable by a human and by `patch(1)`, and the applier in this package can
// replay it backwards without an external tool.

const patchHeader = "# fastexec reverse patch"

// Two steps of a WorkspaceEdit are not text edits and cannot be expressed as
// hunks: a file move, and the encoding a deleted file was written in (its bytes
// are gone, so the encoding needed to restore them has to be recorded). Both
// travel as comment lines, which patch(1) and a human can skip.
const (
	renamePrefix = "# rename: "
	encPrefix    = "# encoding: "
	renameSep    = " -> "
)

// line is one line of a file: the body without its terminator, plus the
// terminator that followed it ("" for the last line of a file that does not
// end in a newline).
type line struct {
	text string
	eol  string
}

// splitLines splits text into lines, each keeping its own terminator.
func splitLines(text string) []line {
	if text == "" {
		return nil
	}
	var out []line
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		body := text[start:i]
		eol := "\n"
		if strings.HasSuffix(body, "\r") {
			body = body[:len(body)-1]
			eol = "\r\n"
		}
		out = append(out, line{text: body, eol: eol})
		start = i + 1
	}
	if start < len(text) {
		out = append(out, line{text: text[start:], eol: ""})
	}
	return out
}

func joinLines(lines []line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text)
		b.WriteString(l.eol)
	}
	return b.String()
}

func bodies(lines []line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// dominantEOL is the line ending a restored line gets when nothing better is
// available. Keeping it means undoing a CRLF edit does not silently convert the
// file to LF.
func dominantEOL(lines []line) string {
	crlf, lf := 0, 0
	for _, l := range lines {
		switch l.eol {
		case "\r\n":
			crlf++
		case "\n":
			lf++
		}
	}
	if crlf >= lf && crlf > 0 {
		return "\r\n"
	}
	if lf > 0 {
		return "\n"
	}
	return "\n"
}

// diffOp is one hunk line: ' ' context, '-' removed, '+' added.
type diffOp struct {
	kind byte
	text string
}

// diffLines computes a line diff with the classic LCS dynamic program, which is
// quadratic. A guard keeps a huge file from allocating a table that would dwarf
// the file itself; the fallback is "replace everything", still a correct patch,
// just less readable.
func diffLines(oldB, newB []string) []diffOp {
	const maxCells = 4 << 20
	if len(oldB)*len(newB) > maxCells {
		ops := make([]diffOp, 0, len(oldB)+len(newB))
		for _, s := range oldB {
			ops = append(ops, diffOp{'-', s})
		}
		for _, s := range newB {
			ops = append(ops, diffOp{'+', s})
		}
		return ops
	}
	n, m := len(oldB), len(newB)
	// lcs[i*(m+1)+j] = length of the LCS of oldB[i:] and newB[j:]
	lcs := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldB[i] == newB[j]:
				lcs[i*(m+1)+j] = lcs[(i+1)*(m+1)+j+1] + 1
			case lcs[(i+1)*(m+1)+j] >= lcs[i*(m+1)+j+1]:
				lcs[i*(m+1)+j] = lcs[(i+1)*(m+1)+j]
			default:
				lcs[i*(m+1)+j] = lcs[i*(m+1)+j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldB[i] == newB[j]:
			ops = append(ops, diffOp{' ', oldB[i]})
			i++
			j++
		case lcs[(i+1)*(m+1)+j] >= lcs[i*(m+1)+j+1]:
			ops = append(ops, diffOp{'-', oldB[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', newB[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', oldB[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', newB[j]})
	}
	return ops
}

// UnifiedDiff renders old -> new as a unified diff with 3 lines of context.
func UnifiedDiff(relPath string, oldLines, newLines []line) string {
	ops := diffLines(bodies(oldLines), bodies(newLines))
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n--- a/%s\n+++ b/%s\n", patchHeader, relPath, relPath)
	const ctx = 3
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i
		for start > 0 && ops[start-1].kind == ' ' && i-start < ctx {
			start--
		}
		end := i
		gap := 0
		for end < len(ops) {
			if ops[end].kind == ' ' {
				gap++
				if gap > 2*ctx {
					break
				}
			} else {
				gap = 0
			}
			end++
		}
		trailing := 0
		for end-1-trailing >= start && ops[end-1-trailing].kind == ' ' {
			trailing++
		}
		if trailing > ctx {
			end -= trailing - ctx
		}

		oldStart, newStart, oldCount, newCount := 1, 1, 0, 0
		for k := 0; k < start; k++ {
			if ops[k].kind != '+' {
				oldStart++
			}
			if ops[k].kind != '-' {
				newStart++
			}
		}
		for k := start; k < end; k++ {
			if ops[k].kind != '+' {
				oldCount++
			}
			if ops[k].kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))
		for k := start; k < end; k++ {
			b.WriteByte(ops[k].kind)
			b.WriteString(ops[k].text)
			b.WriteByte('\n')
		}
		i = end
	}
	return b.String()
}

// hunkRange formats one side of the @@ header. A zero-length range keeps the
// preceding line number, which is what patch(1) expects.
func hunkRange(start, count int) string {
	switch {
	case count == 0:
		return strconv.Itoa(start-1) + ",0"
	case count == 1:
		return strconv.Itoa(start)
	default:
		return strconv.Itoa(start) + "," + strconv.Itoa(count)
	}
}

// hunk is one @@ block: the operations to replay, anchored at a line number in
// the pre-image for error reporting.
type hunk struct {
	oldStart int
	ops      []diffOp
}

// patchSection is the part of the patch that belongs to one file. The kind tells
// Undo what to do with the result: an edited file is written back, a created one
// is removed, a deleted one is recreated from its '-' lines.
type patchSection struct {
	path  string
	kind  opKind
	enc   string // opDelete only: encoding the gone file was written in
	hunks []hunk
}

// renameRecord is a file move performed by the edit. It has no hunks, so it is
// the only trace of the step and Undo replays it as a move back.
type renameRecord struct {
	from string
	to   string
}

// parsePatch reads a patch produced by UnifiedDiff into per-file sections, plus
// the moves the edit performed.
//
// The parser is stateful on purpose: a removed line whose own text starts with
// "--- " must stay a body line, so headers are only recognised outside a hunk.
func parsePatch(patch string) ([]patchSection, []renameRecord, error) {
	var secs []patchSection
	var renames []renameRecord
	var cur *patchSection
	inBody := false
	for _, raw := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(raw, patchHeader):
			cur, inBody = nil, false
		case strings.HasPrefix(raw, encPrefix) && !inBody:
			// Only meaningful attached to the section it was written under.
			if cur != nil {
				cur.enc = strings.TrimSpace(strings.TrimPrefix(raw, encPrefix))
			}
		case strings.HasPrefix(raw, renamePrefix) && !inBody:
			// A move is not a text edit: it is recorded here and replayed by
			// Undo, which is why the patch keeps the comment instead of a hunk.
			from, to, ok := strings.Cut(strings.TrimPrefix(raw, renamePrefix), renameSep)
			if !ok {
				return nil, nil, fmt.Errorf("lsp: bad rename record %q", raw)
			}
			renames = append(renames, renameRecord{from: from, to: to})
		case strings.HasPrefix(raw, "# ") && !inBody:
			// An unknown record: skipped, not an error. A newer writer may have
			// added one, and refusing the whole patch would make the edit strict
			// forward-only for no gain.
		case strings.HasPrefix(raw, "+++ ") && !inBody:
			// The post-image header. It must not discard the section: the path
			// comes from the "--- " line, and the hunks follow this one.
			inBody = false
		case strings.HasPrefix(raw, "--- "):
			secs = append(secs, patchSection{path: strings.TrimPrefix(raw, "--- a/")})
			cur, inBody = &secs[len(secs)-1], false
		case strings.HasPrefix(raw, "@@"):
			if cur == nil {
				return nil, nil, fmt.Errorf("lsp: hunk outside a file section")
			}
			oldStart, oldCount, newCount, err := parseHunkHeader(raw)
			if err != nil {
				return nil, nil, err
			}
			// A range that is empty on one side is how UnifiedDiff encodes a
			// file that appears (create) or disappears (delete). Keeping only
			// oldStart would lose that and make Undo edit a file that is gone.
			if cur.hunks == nil {
				switch {
				case oldCount == 0:
					cur.kind = opCreate
				case newCount == 0:
					cur.kind = opDelete
				default:
					cur.kind = opEdit
				}
			}
			cur.hunks = append(cur.hunks, hunk{oldStart: oldStart})
			inBody = true
		case raw == "":
			inBody = false
		case inBody && cur != nil:
			if len(cur.hunks) == 0 {
				return nil, nil, fmt.Errorf("lsp: hunk body before a @@ header")
			}
			last := &cur.hunks[len(cur.hunks)-1]
			last.ops = append(last.ops, diffOp{raw[0], raw[1:]})
		}
	}
	if len(secs) == 0 && len(renames) == 0 {
		return nil, nil, fmt.Errorf("lsp: patch has no file sections")
	}
	return secs, renames, nil
}

// parseHunkHeader returns the line ranges from "@@ -a,b +c,d @@". An omitted
// count means 1, and an explicit ",0" means the side is absent from this hunk,
// which is what marks a create or a delete.
func parseHunkHeader(s string) (oldStart, oldCount, newCount int, err error) {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "@@"), "@@"))
	fields := strings.Fields(body)
	if len(fields) < 2 {
		return 0, 0, 0, fmt.Errorf("lsp: bad hunk header %q", s)
	}
	oldStart, oldCount, err = parseRange(fields[0])
	if err != nil {
		return 0, 0, 0, err
	}
	if _, newCount, err = parseRange(fields[1]); err != nil {
		return 0, 0, 0, err
	}
	return oldStart, oldCount, newCount, nil
}

func parseRange(f string) (start, count int, err error) {
	f = strings.TrimLeft(f, "-+")
	parts := strings.SplitN(f, ",", 2)
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("lsp: bad hunk range %q", f)
	}
	count = 1
	if len(parts) == 2 {
		if count, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, fmt.Errorf("lsp: bad hunk range %q", f)
		}
	}
	return start, count, nil
}

// applyReversePatch turns the post-image text back into the pre-image: every
// '+' line is dropped and every '-' line restored. Context must match, so an
// edited-since file is reported instead of being mangled.
func applyReversePatch(text string, hunks []hunk) (string, error) {
	lines := splitLines(text)
	dom := dominantEOL(lines)
	// Last hunk first, so earlier line numbers stay valid.
	for hi := len(hunks) - 1; hi >= 0; hi-- {
		h := hunks[hi]
		at, ok := findHunk(lines, h)
		if !ok {
			return "", fmt.Errorf("lsp: cannot undo: hunk at old line %d no longer matches", h.oldStart)
		}
		var repl []line
		k := at
		for _, op := range h.ops {
			switch op.kind {
			case ' ':
				repl = append(repl, lines[k])
				k++
			case '+':
				k++ // added by the edit being undone
			case '-':
				repl = append(repl, line{text: op.text, eol: eolFor(lines, k, dom)})
			}
		}
		merged := make([]line, 0, len(lines)-(k-at)+len(repl))
		merged = append(merged, lines[:at]...)
		merged = append(merged, repl...)
		merged = append(merged, lines[k:]...)
		lines = merged
	}
	return joinLines(lines), nil
}

// findHunk locates the lines a hunk expects to see in the current text: the
// context and the lines the edit added.
func findHunk(lines []line, h hunk) (int, bool) {
	pat := make([]string, 0, len(h.ops))
	for _, op := range h.ops {
		if op.kind == ' ' || op.kind == '+' {
			pat = append(pat, op.text)
		}
	}
	if len(pat) == 0 {
		return 0, false
	}
	for i := 0; i+len(pat) <= len(lines); i++ {
		match := true
		for k, p := range pat {
			if lines[i+k].text != p {
				match = false
				break
			}
		}
		if match {
			return i, true
		}
	}
	return 0, false
}

// eolFor picks the terminator for a restored line: whatever followed at that
// position, the file's convention otherwise.
func eolFor(lines []line, k int, dom string) string {
	if k < len(lines) && lines[k].eol != "" {
		return lines[k].eol
	}
	if k >= len(lines) && len(lines) > 0 && lines[len(lines)-1].eol == "" {
		return "" // file still ends without a newline
	}
	return dom
}
