package corpus

import (
	"regexp"
	"strings"

	"homelab/fastexec/internal/textenc"
)

// Redactor masks one kind of secret in a string.
type Redactor interface{ Apply(string) string }

type regexRedactor struct {
	re   *regexp.Regexp
	repl string
}

func (r regexRedactor) Apply(s string) string { return r.re.ReplaceAllString(s, r.repl) }

// applyKeepLines masks like Apply but keeps the number of newlines of every match,
// so a line number in the redacted text still points at the same line of the original.
// Newlines that a replacement would add are turned into spaces, then the match's own
// newlines are restored after it.
func (r regexRedactor) applyKeepLines(s string) string {
	idx := r.re.FindAllStringSubmatchIndex(s, -1)
	if len(idx) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		b.WriteString(s[last:m[0]])
		rep := string(r.re.ExpandString(nil, r.repl, s, m))
		b.WriteString(strings.ReplaceAll(rep, "\n", " "))
		b.WriteString(strings.Repeat("\n", strings.Count(s[m[0]:m[1]], "\n")))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// RedactKeepLines applies rs like Redact but preserves the line structure of s:
// for every line of the input there is still exactly one line in the output.
// Redactors that are not regex based fall back to Apply and may change line count.
func RedactKeepLines(s string, rs []Redactor) string {
	for _, r := range rs {
		if rr, ok := r.(regexRedactor); ok {
			s = rr.applyKeepLines(s)
			continue
		}
		s = r.Apply(s)
	}
	return s
}

// DefaultRedactors is the masking used by the corpus and, through Sanitize, by telemetry.
// It is defense-in-depth: it recognizes common shapes, not every secret.
func DefaultRedactors() []Redactor {
	return []Redactor{
		regexRedactor{regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED:private-key]"},
		// PEM block cut before its END line (for example by a truncated command): mask the rest.
		regexRedactor{regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*`), "[REDACTED:private-key]"},
		regexRedactor{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "$1 [REDACTED]"},
		regexRedactor{regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|(?:sk|rk)_live_[A-Za-z0-9]{16,}|npm_[A-Za-z0-9]{36}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,})`), "[REDACTED:token]"},
		// key=value, key: value and JSON "key": "value" where the key names a credential.
		regexRedactor{regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|token|credential|cookie|api[_-]?key|authorization|[_.-]key)[A-Za-z0-9_.-]*)(["']?\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`), "$1$2[REDACTED]"},
		// Device constructors with a positional local key, as in the TinyTuya family:
		// Device(id, address, "local_key"), BulbDevice(...), OutletDevice(...). Only the
		// third positional argument is masked, and only when it is a quoted literal of
		// 8+ characters. Go regexp has no backreferences, so each quote style has a rule.
		regexRedactor{regexp.MustCompile(`(\b(?:Bulb|Outlet)?Device\(\s*[^,()\n]+,\s*[^,()\n]+,\s*)"[^"\n]{8,}"`), `${1}"[REDACTED]"`},
		regexRedactor{regexp.MustCompile(`(\b(?:Bulb|Outlet)?Device\(\s*[^,()\n]+,\s*[^,()\n]+,\s*)'[^'\n]{8,}'`), `${1}'[REDACTED]'`},
		// CLI flags: --password x, --token=x, --api-key x (the value is the next word or quoted text).
		regexRedactor{regexp.MustCompile(`(?i)(--?[A-Za-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key|apikey|auth)[A-Za-z0-9_-]*(?:=|\s+))("[^"\n]*"|'[^'\n]*'|[^\s"']+)`), "${1}[REDACTED]"},
		// URL credentials: scheme://user:password@host, and scheme://token@host.
		regexRedactor{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`), "${1}[REDACTED]@"},
		regexRedactor{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[A-Za-z0-9_-]{20,}@`), "${1}[REDACTED]@"},
	}
}

var defaultRedactors = DefaultRedactors()

// Sanitize makes free text safe to persist: it converts legacy encodings to UTF-8,
// replaces invalid UTF-8 bytes, and masks recognizable credentials. Legitimate text
// is returned unchanged.
func Sanitize(s string) string {
	if d, enc := textenc.Decode(s); enc != "" {
		s = d
	}
	s = strings.ToValidUTF8(s, "�")
	return Redact(s, defaultRedactors)
}
