package tools

import (
	"fmt"
	"strings"
)

// nearMiss explains why an edit_file target was not found, so the model can fix
// the target without re-reading the file. Runs only on the failure path.
// Checks, in order: doubled backslashes (JSON escaping), then the closest window
// of lines (trimmed, positional), reporting the line range and the first
// difference. Returns "" when nothing useful is found.
func nearMiss(content, target string) string {
	if target == "" || len(content) > 256*1024 {
		return ""
	}
	if strings.Contains(target, `\\`) {
		un := strings.ReplaceAll(target, `\\`, `\`)
		if strings.Contains(content, un) || strings.Contains(content, strings.ReplaceAll(un, "\n", "\r\n")) {
			return "Hint: the target over-escapes backslashes (\\\\ where the file has \\). Send it with single backslashes."
		}
	}

	file := splitLines(content)
	block := splitLines(target)
	for len(block) > 0 && strings.TrimSpace(block[len(block)-1]) == "" {
		block = block[:len(block)-1]
	}
	if len(block) == 0 || len(block) > 64 || len(file) == 0 {
		return ""
	}
	trim := func(s string) string { return strings.TrimSpace(strings.TrimSuffix(s, "\r")) }
	tb := make([]string, len(block))
	for i, l := range block {
		tb[i] = trim(l)
	}

	// Uma linha só: procura a linha que contém o texto ignorando espaços repetidos.
	if len(block) == 1 {
		want := collapseSpaces(tb[0])
		if want == "" {
			return ""
		}
		for i, l := range file {
			if strings.Contains(collapseSpaces(trim(l)), want) {
				return fmt.Sprintf("Hint: line %d has this text with different spacing or indentation:\n%d\t%s\nCopy it exactly (read_file(start_line=%d, end_line=%d)).", i+1, i+1, clip(trim(l)), i+1, i+1)
			}
		}
		lw := strings.ToLower(want)
		for i, l := range file {
			if strings.Contains(strings.ToLower(collapseSpaces(trim(l))), lw) {
				return fmt.Sprintf("Hint: line %d matches only ignoring case:\n%d\t%s", i+1, i+1, clip(trim(l)))
			}
		}
		return ""
	}

	best, bestAt := 0, -1
	for at := 0; at+len(tb) <= len(file); at++ {
		score := 0
		for j := range tb {
			if trim(file[at+j]) == tb[j] {
				score++
			}
		}
		if score > best {
			best, bestAt = score, at
		}
	}
	if bestAt < 0 || best == 0 || best*2 < len(tb) {
		return ""
	}
	from, to := bestAt+1, bestAt+len(tb)
	if best == len(tb) {
		return fmt.Sprintf("Hint: lines %d-%d match the target except for whitespace/indentation. Copy those lines exactly (read_file(start_line=%d, end_line=%d)).", from, to, from, to)
	}
	for j := range tb {
		if got := trim(file[bestAt+j]); got != tb[j] {
			return fmt.Sprintf("Hint: closest match is lines %d-%d (%d/%d lines equal). First difference at line %d:\n  file:   %s\n  target: %s\nread_file(start_line=%d, end_line=%d) shows the current text.",
				from, to, best, len(tb), bestAt+j+1, clip(got), clip(tb[j]), from, to)
		}
	}
	return ""
}

func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string) string { return clipAt(s, 120) }

// clipAt corta em n runas (nunca no meio de um caractere) e marca o corte com "…".
func clipAt(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
