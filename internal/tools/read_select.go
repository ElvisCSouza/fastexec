package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
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
	var sb strings.Builder
	for i, r := range rs {
		if i > 0 {
			sb.WriteString("...\n")
		}
		for n := r.from; n <= r.to; n++ {
			if numbered {
				fmt.Fprintf(&sb, "%d\t", n)
			}
			sb.WriteString(strings.TrimSuffix(lines[n-1], "\r"))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
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
		out := renderRanges(lines, rs, true)
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

// capWholeFile cuts a big whole-file read at a line boundary and says how
// to read the rest. Disabled with FASTEXEC_NO_READ_CAP=1.
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
