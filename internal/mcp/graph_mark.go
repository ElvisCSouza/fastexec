package mcp

import (
	"regexp"
	"strconv"
	"strings"

	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/tools"
)

var (
	// reExactHeader: the header a code_graph answer carries when a language server (csharp-ls, gopls, ...) gave it.
	reExactHeader = regexp.MustCompile(`^=== \w+ of '[^']*' \(exact, ([a-z0-9._-]+)\) ===`)
	reGraphKB     = regexp.MustCompile(`\((\d+) KB\)`)
	reRewriteKB   = regexp.MustCompile(`\(avoidable (\d+) KB\)`)
)

// graphDecision reads the graph marker that tools put in the output. An outline can also come inside an exec
// auto-remediation (wrapped file reads), after its header line. 'rewrite' marks a write_file that rewrote a file
// it mostly kept (rewrite_hint.go).
func graphDecision(out string) string {
	if strings.HasPrefix(out, "=== ") {
		// code_graph answers: which engine answered. 'exact:<server>' is the language server, 'unavailable' is a
		// server that was wanted and could not answer (the reason is in the text), 'by-name' is the index.
		if m := reExactHeader.FindStringSubmatch(out); m != nil {
			return "exact:" + m[1]
		}
		if strings.Contains(out, "\nlsp unavailable (") {
			return "unavailable"
		}
		if strings.Contains(out[:min(len(out), 120)], " of '") {
			return "by-name"
		}
	}
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

// graphBasisOf lê a linha "basis:" que o code_graph põe logo após o cabeçalho (internal/tools/basis.go). Sem a
// linha devolve o zero (Action ""), e a telemetria grava -1 nos inteiros. Só valores de vocabulário fechado entram
// (ação, linguagem, provider, grau, cache, contagens): nenhum nome de símbolo ou caminho.
func graphBasisOf(out string) telemetry.GraphBasis {
	var b telemetry.GraphBasis
	if !strings.HasPrefix(out, "=== ") {
		return b
	}
	lines := strings.SplitN(out, "\n", 3)
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "basis: ") {
		return b
	}
	b = telemetry.GraphBasis{Refs: -1, Files: -1}
	for _, kv := range strings.Fields(strings.TrimPrefix(lines[1], "basis: ")) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "action":
			b.Action = v
		case "lang":
			b.Lang = v
		case "provider":
			b.Provider = v
		case "grade":
			b.Grade = v
		case "cache":
			b.Cache = v
		case "depth":
			b.Depth, _ = strconv.Atoi(v)
		case "refs":
			b.Refs, _ = strconv.Atoi(v)
		case "files":
			b.Files, _ = strconv.Atoi(v)
		}
	}
	return b
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
