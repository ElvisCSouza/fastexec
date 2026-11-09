package mcp

import (
	"regexp"
	"strconv"
	"strings"

	"homelab/fastexec/internal/tools"
)

var reGraphKB = regexp.MustCompile(`\((\d+) KB\)`)

// graphDecision reads the graph marker that tools put in the output.
func graphDecision(out string) string {
	switch {
	case strings.HasPrefix(out, tools.GraphMarkOutline):
		return "outline"
	case strings.HasPrefix(out, tools.GraphMarkSearch):
		return "search"
	case strings.Contains(out, tools.GraphMarkRepeat):
		return "repeat"
	}
	return ""
}

// graphSavedKB: size of the text an outline replaced (from its first line).
func graphSavedKB(out string) int {
	if !strings.HasPrefix(out, tools.GraphMarkOutline) {
		return 0
	}
	first := out
	if i := strings.IndexByte(out, '\n'); i > 0 {
		first = out[:i]
	}
	if m := reGraphKB.FindStringSubmatch(first); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}
