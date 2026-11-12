package tools

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// readWholeCap is the largest whole-file read returned in one call.
// Bigger files come back cut at a line boundary with a pointer to the
// cheaper ways to read them (ranges, pattern, code_outline).
const readWholeCap = 32 * 1024

const maxPatternHits = 60

type lineRange struct{ from, to int }

// parseRanges parses "10-20,40,55-" into line ranges (1-based, inclusive).
func parseRanges(spec string, total int) ([]lineRange, error) {
	var out []lineRange
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil || from < 1 {
			return nil, fmt.Errorf("invalid range %q: use 'start-end', e.g. ranges='10-40,90-120'", part)
		}
		to := from
		if isRange {
			if strings.TrimSpace(b) == "" {
				to = total
			} else if to, err = strconv.Atoi(strings.TrimSpace(b)); err != nil || to < from {
				return nil, fmt.Errorf("invalid range %q: use 'start-end', e.g. ranges='10-40,90-120'", part)
			}
		}
		if to > total {
			to = total
		}
		if from <= to {
			out = append(out, lineRange{from, to})
		}
	}
	return out, nil
}

func splitLines(data string) []string {
	lines := strings.Split(data, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

func renderRanges(lines []string, rs []lineRange, numbered bool) string {
	return renderRangesWith(lines, rs, numbered, nil)
}

// renderRangesWith is renderRanges with an optional per-line formatter (used by pattern mode to window giant lines).
func renderRangesWith(lines []string, rs []lineRange, numbered bool, format func(string) string) string {
	var sb strings.Builder
	for i, r := range rs {
		if i > 0 {
			sb.WriteString("...\n")
		}
		for n := r.from; n <= r.to; n++ {
			if numbered {
				fmt.Fprintf(&sb, "%d\t", n)
			}
			l := strings.TrimSuffix(lines[n-1], "\r")
			if format != nil {
				l = format(l)
			}
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

const (
	digestMinHits   = 60 // abaixo disso as linhas casadas vão por inteiro
	digestMinRepeat = 3  // média de repetições por mensagem para valer o resumo (código casa linhas distintas: não resume)
	digestMaxGroups = 20
)

var reDigits = regexp.MustCompile(`\d+`)

// digestMatches: padrão que casa muitas linhas repetidas (log: o mesmo erro milhares de vezes) devolve o resumo por mensagem
// distinta (dígitos ignorados: timestamps, ids): contagem, primeira e última linha e a primeira ocorrência inteira.
// Sem perda de acesso: a primeira ocorrência vem completa e as linhas se leem com ranges ou com um padrão mais estreito.
// ok=false (não resume) quando casa pouco ou as linhas são quase todas distintas.
func digestMatches(lines []string, re *regexp.Regexp) (string, bool) {
	type group struct {
		count, first, last int
		example            string
	}
	groups := map[string]*group{}
	var order []string
	hits := 0
	for i, l := range lines {
		if !re.MatchString(l) {
			continue
		}
		hits++
		key := reDigits.ReplaceAllString(l, "#")
		if g, ok := groups[key]; ok {
			g.count++
			g.last = i + 1
			continue
		}
		groups[key] = &group{count: 1, first: i + 1, last: i + 1, example: l}
		order = append(order, key)
	}
	if hits < digestMinHits || hits < digestMinRepeat*len(groups) {
		return "", false
	}
	sort.SliceStable(order, func(a, b int) bool { return groups[order[a]].count > groups[order[b]].count })
	var b strings.Builder
	fmt.Fprintf(&b, "[FastExec] %d lines match in %d distinct messages (digits ignored), most frequent first:\n", hits, len(groups))
	for i, k := range order {
		if i == digestMaxGroups {
			fmt.Fprintf(&b, "... %d more messages\n", len(order)-digestMaxGroups)
			break
		}
		g := groups[k]
		ex := strings.TrimSpace(g.example)
		if len(ex) > 240 {
			ex = ex[:240] + "..."
		}
		fmt.Fprintf(&b, "%dx lines %d..%d | first: %s\n", g.count, g.first, g.last, ex)
	}
	b.WriteString("Counts, first lines and line ranges above are complete; use ranges='a-b' or a narrower pattern only to see other lines.\n")
	return b.String(), true
}

// selectLines applies ranges/pattern/numbered. handled=false means the
// caller should fall back to its default behaviour.
func selectLines(data string, args ReadFileArgs) (string, bool, error) {
	if args.Ranges == "" && args.Pattern == "" && !args.Numbered {
		return "", false, nil
	}
	lines := splitLines(data)
	total := len(lines)

	if args.Pattern != "" {
		re, err := regexp.Compile("(?i)" + args.Pattern)
		if err != nil {
			return "", true, fmt.Errorf("invalid pattern: %v", err)
		}
		ctx := args.Context
		if ctx <= 0 {
			ctx = 2
		}
		if d, ok := digestMatches(lines, re); ok {
			return d, true, nil
		}
		var rs []lineRange
		hits := 0
		for i, l := range lines {
			if !re.MatchString(l) {
				continue
			}
			if hits++; hits > maxPatternHits {
				break
			}
			from, to := i+1-ctx, i+1+ctx
			if from < 1 {
				from = 1
			}
			if to > total {
				to = total
			}
			if k := len(rs); k > 0 && from <= rs[k-1].to+1 {
				if to > rs[k-1].to {
					rs[k-1].to = to
				}
				continue
			}
			rs = append(rs, lineRange{from, to})
		}
		if hits == 0 {
			return fmt.Sprintf("no line matches %q (%d lines in file)", args.Pattern, total), true, nil
		}
		out := renderRangesWith(lines, rs, true, func(l string) string { return windowLongLine(l, re) })
		if hits > maxPatternHits {
			out += fmt.Sprintf("[FastExec] Stopped at %d matches; narrow the pattern or use ranges.\n", maxPatternHits)
		}
		return out, true, nil
	}

	if args.Ranges != "" {
		rs, err := parseRanges(args.Ranges, total)
		if err != nil {
			return "", true, err
		}
		if len(rs) == 0 {
			return fmt.Sprintf("no lines in those ranges (file has %d lines)", total), true, nil
		}
		return renderRanges(lines, rs, args.Numbered || len(rs) > 1), true, nil
	}

	// numbered only, optionally with start_line/end_line
	from, to := args.StartLine, args.EndLine
	if from <= 0 {
		from = 1
	}
	if to <= 0 || to > total {
		to = total
	}
	if from > to {
		return "", true, nil
	}
	return renderRanges(lines, []lineRange{{from, to}}, true), true, nil
}

// capWholeFile is the whole-file cap without the repeat escape (kept for callers and tests).
// Lines longer than longLineBytes (minified JSON/HTML, one-line logs) are not returned whole in pattern mode: a
// matching line is shown as a window around its first match, any other line as its head. The note says how long the
// line is and where the other matches are, so the reader can continue with offset/limit.
const (
	longLineBytes = 2000
	winBefore     = 300
	winAfter      = 700
	maxOtherHits  = 8
)

func windowLongLine(l string, re *regexp.Regexp) string {
	if len(l) <= longLineBytes {
		return l
	}
	locs := re.FindAllStringIndex(l, maxOtherHits+1)
	if len(locs) == 0 {
		return runeSafe(l, 0, winBefore+winAfter) + fmt.Sprintf(" [FastExec: line is %d bytes, head shown; offset/limit reads the rest]", len(l))
	}
	from, to, kind := matchWindow(l, locs[0][0], locs[0][1])
	note := fmt.Sprintf(" [FastExec: line is %d bytes; bytes %d-%d shown (%s) around the match at byte %d", len(l), from, to, kind, locs[0][0])
	if len(locs) > 1 {
		offs := make([]string, 0, len(locs)-1)
		for _, m := range locs[1:] {
			offs = append(offs, strconv.Itoa(m[0]))
		}
		more := ""
		if len(locs) > maxOtherHits {
			offs = offs[:maxOtherHits-1]
			more = ", ..."
		}
		note += "; other matches at bytes " + strings.Join(offs, ", ") + more
	}
	note += "; offset/limit reads more]"
	pre, post := "", ""
	if from > 0 {
		pre = "…"
	}
	if to < len(l) {
		post = "…"
	}
	return pre + runeSafe(l, from, to) + post + note
}

// maxRecordBytes is the largest enclosing {...} record returned whole around a match.
const maxRecordBytes = 4096

// matchWindow picks the bytes to show around a match at [ms,me): the outermost balanced {...} record that contains it
// and fits maxRecordBytes (so a hit inside a minified JSON record comes back as the whole record, never half of it),
// else a fixed window. kind says which one was used.
func matchWindow(l string, ms, me int) (from, to int, kind string) {
	from, to, kind = max(ms-winBefore, 0), min(me+winAfter, len(l)), "fixed window"
	bestFrom, bestTo := -1, -1
	depth := 0
scan:
	for i := ms - 1; i >= 0 && ms-i <= maxRecordBytes; i-- {
		switch l[i] {
		case '}', ']':
			depth++
		case '{', '[':
			if depth > 0 {
				depth--
				continue
			}
			end := closeOf(l, i, maxRecordBytes)
			switch {
			case end < me: // too big (or unbalanced): nothing wider can be returned
				break scan
			case l[i] == '{':
				bestFrom, bestTo = i, end
			}
		}
	}
	switch {
	case bestFrom < 0:
		return
	case bestTo-bestFrom < winBefore+winAfter && !(bestFrom > 0 && (l[bestFrom-1] == ',' || l[bestFrom-1] == '[')):
		// (an object that follows ',' or '[' is an array element, i.e. a whole record, and is returned exactly)
		// only an object nested inside a record bigger than maxRecordBytes fit: keep it and add the fixed window
		return min(bestFrom, from), max(bestTo, to), "inner object plus fixed window"
	}
	return bestFrom, bestTo, "enclosing record"
}

// closeOf returns the end (exclusive) of the balanced {...} or [...] starting at l[open], or -1 when it is longer
// than limit bytes or unbalanced. Braces inside JSON strings are skipped.
func closeOf(l string, open, limit int) int {
	depth, inStr := 0, false
	for i := open; i < len(l) && i-open <= limit; i++ {
		c := l[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// runeSafe returns l[from:to] moved inward to rune boundaries.
func runeSafe(l string, from, to int) string {
	for from < len(l) && !utf8.RuneStart(l[from]) {
		from++
	}
	for to > from && to < len(l) && !utf8.RuneStart(l[to]) {
		to--
	}
	if to > len(l) {
		to = len(l)
	}
	return l[from:to]
}

func capWholeFile(data string, path string) string {
	if len(data) <= readWholeCap || os.Getenv("FASTEXEC_NO_READ_CAP") != "" {
		return data
	}
	cut := strings.LastIndexByte(data[:readWholeCap], '\n')
	if cut < 0 {
		cut = readWholeCap
	}
	shown := strings.Count(data[:cut], "\n") + 1
	total := len(splitLines(data))
	return data[:cut] + fmt.Sprintf(
		"\n\n[FastExec] File has %d lines (%d KB); showing lines 1-%d. Next: code_outline(path) to locate a symbol, then read_file(start_line, end_line) or read_file(ranges='a-b,c-d') or read_file(pattern='regex', context=3).",
		total, len(data)/1024, shown)
}
