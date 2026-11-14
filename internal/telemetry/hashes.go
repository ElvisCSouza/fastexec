package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// Hashes and marks let the telemetry measure repetition and what the agent was told, without storing any content:
// only 16-hex digests and short marker ids are kept.

// ShortHash is the first 16 hex chars of sha256(s); "" for "".
func ShortHash(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// ArgsHash hashes the complete arguments of a call (command, argv, stdin input, file content...) in canonical form
// (sorted keys), leaving out the fields injected by the proxy so the same call from two channels hashes the same.
func ArgsHash(rawArgs []byte) string {
	var m map[string]any
	if err := json.Unmarshal(rawArgs, &m); err != nil || len(m) == 0 {
		return ShortHash(string(rawArgs))
	}
	delete(m, "_caller")
	delete(m, "_conversation")
	b, err := json.Marshal(m) // encoding/json sorts map keys
	if err != nil {
		return ""
	}
	return ShortHash(string(b))
}

var (
	markRe     = regexp.MustCompile(`\[FastExec(?: ([A-Za-z-]+(?: [A-Za-z-]+)?))?\]`)
	markVolRe  = regexp.MustCompile(`'[^']*'|"[^"]*"|\S*[/\\]\S*|\b[0-9a-f]{6,}\b|\d+`)
	maxMarks   = 6
	markLineMx = 160
)

// Marks lists the FastExec markers present in a response as kind:template-id, e.g. "Hint:3f2a,Guard:9c01".
// The id hashes the marker's line with quoted text and numbers removed, so the same message template always gets the
// same id whatever file or count it mentions. adjusted adds "args" (parameter names were rewritten by argnorm).
func Marks(out string, adjusted bool) (res string) {
	defer func() { // telemetry must never break a tool response
		if recover() != nil {
			res = "marks-error"
		}
	}()
	var marks []string
	seen := map[string]bool{}
	add := func(m string) {
		if !seen[m] && len(marks) < maxMarks {
			seen[m] = true
			marks = append(marks, m)
		}
	}
	if adjusted {
		add("args")
	}
	if !strings.Contains(out, "[FastExec") {
		return strings.Join(marks, ",")
	}
	for _, m := range scanMarks(out) {
		add(m.Mark)
	}
	return strings.Join(marks, ",")
}

// MarkDef is a marker found in a response: its id ("Hint:3f2a") and the template text the id hashes
// (quoted text, paths, hashes and numbers removed, so it holds no content from the call).
type MarkDef struct {
	Mark     string
	Template string
}

// MarkDefs lists the distinct markers of a response with their template text. Recorded once per id in
// `mark_catalog`, so a stored id can be traced back to the rule that said it.
func MarkDefs(out string) (defs []MarkDef) {
	defer func() {
		if recover() != nil {
			defs = nil
		}
	}()
	if !strings.Contains(out, "[FastExec") {
		return nil
	}
	seen := map[string]bool{}
	for _, m := range scanMarks(out) {
		if !seen[m.Mark] {
			seen[m.Mark] = true
			defs = append(defs, m)
		}
	}
	return defs
}

// scanMarks finds every marker line of a response, in order.
func scanMarks(out string) []MarkDef {
	var res []MarkDef
	for _, line := range strings.Split(out, "\n") {
		loc := markRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		kind := "FastExec"
		if loc[2] >= 0 {
			kind = strings.ReplaceAll(line[loc[2]:loc[3]], " ", "")
		}
		rest := line[loc[1]:]
		if len(rest) > markLineMx {
			rest = rest[:markLineMx]
		}
		template := strings.TrimSpace(markVolRe.ReplaceAllString(rest, ""))
		id := "0000" // marker with nothing after it
		if h := ShortHash(template); len(h) >= 4 {
			id = h[:4]
		}
		res = append(res, MarkDef{Mark: kind + ":" + id, Template: template})
	}
	return res
}
