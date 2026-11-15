package corpus

import "regexp"

// Redactor masks one kind of secret. New kinds are added by appending to DefaultRedactors, never by editing Redact.
type Redactor interface{ Apply(string) string }

type regexRedactor struct {
	re   *regexp.Regexp
	repl string
}

func (r regexRedactor) Apply(s string) string { return r.re.ReplaceAllString(s, r.repl) }

// DefaultRedactors covers the usual shapes: bearer/API tokens, key=value secrets, private key blocks, URL passwords.
func DefaultRedactors() []Redactor {
	return []Redactor{
		regexRedactor{regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED:private-key]"},
		regexRedactor{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "$1 [REDACTED]"},
		regexRedactor{regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,})`), "[REDACTED:token]"},
		regexRedactor{regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|authorization)[A-Za-z0-9_.-]*)(["']?\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`), "$1$2[REDACTED]"},
		regexRedactor{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`), "${1}[REDACTED]@"},
	}
}
