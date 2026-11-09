package mcp

import (
	"regexp"
	"strconv"
	"strings"

	"homelab/fastexec/internal/tools"
)

var (
	reGraphKB   = regexp.MustCompile(`\((\d+) KB\)`)
	reRewriteKB = regexp.MustCompile(`\(avoidable (\d+) KB\)`)
)

// graphDecision reads the graph marker that tools put in the output. An outline can also come inside an exec
// auto-remediation (wrapped file reads), after its header line. 'rewrite' marks a write_file that rewrote a file
// it mostly kept (rewrite_hint.go).
func graphDecision(out string) string {
	switch {
	case strings.HasPrefix(out, tools.GraphMarkOutline) || strings.Contains(out, "\n"+tools.GraphMarkOutline):
		return "outline"
	case strings.HasPrefix(out, tools.GraphMarkSearch):
		return "search"
	case strings.Contains(out, tools.GraphMarkRepeat):
		return "repeat"
	case strings.Contains(out, tools.RewriteMark):
		return "rewrite"
	}
	return ""
}

// graphSavedKB: KB avoided. Outline: size of the text each outline replaced (sum, from each outline's first line).
// Rewrite: KB the model sent that an edit_file would not have needed (avoidable, not saved).
func graphSavedKB(out string) int {
	if i := strings.Index(out, tools.RewriteMark); i >= 0 {
		if m := reRewriteKB.FindStringSubmatch(out[i:]); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
		return 0
	}
	total := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, tools.GraphMarkOutline) {
			continue
		}
		if m := reGraphKB.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			total += n
		}
	}
	return total
}
