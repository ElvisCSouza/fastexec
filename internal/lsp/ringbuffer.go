package lsp

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// ringBuffer keeps the last n bytes written to it. Language servers log to
// stderr without a size limit; FastExec keeps a bounded tail so a chatty
// server cannot grow the daemon.
type ringBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		r.data = r.data[len(r.data)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.data)
}

const (
	// stderrTailLines is how far back into the captured log a degradation note
	// looks. The cause is said last: everything before it is a banner, a
	// download line or progress chatter.
	stderrTailLines = 40

	// stderrCauseMax bounds the phrase that reaches the note. The note rides on
	// an MCP answer, so it is paid for in tokens on every degraded query.
	stderrCauseMax = 160
)

// stderrHints are the words that separate a cause from a server's ordinary
// chatter. Matching is a hint, not a parse: a server whose last line looks like
// a failure is answering "why", and quoting it beats paraphrasing.
var stderrHints = []string{
	"error", "cannot", "can't", "failed", "failure", "fatal", "panic",
	"exception", "no such", "not found", "unable", "refused", "missing",
	"denied", "invalid", "unsupported", "unhandled",
}

// StderrCause distills the reason a language server gave up from its own log.
//
// It returns "" when nothing in the tail looks like a cause, and that silence is
// deliberate: a note that quotes a package-download line is worse than a note
// that says only what FastExec knows for sure.
func StderrCause(log string) string {
	lines := strings.Split(strings.TrimRight(log, "\r\n"), "\n")
	if len(lines) > stderrTailLines {
		lines = lines[len(lines)-stderrTailLines:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !looksLikeCause(line) {
			continue
		}
		return clipCause(line)
	}
	return ""
}

func looksLikeCause(line string) bool {
	low := strings.ToLower(line)
	for _, hint := range stderrHints {
		if strings.Contains(low, hint) {
			return true
		}
	}
	return false
}

// clipCause turns one log line into a phrase: no timestamp, single spaces, and
// short enough for an answer.
func clipCause(line string) string {
	s := strings.Join(strings.Fields(trimLogStamp(line)), " ")
	if len(s) <= stderrCauseMax {
		return s
	}
	cut := s[:stderrCauseMax]
	// Never end in the middle of a rune, then prefer a word boundary.
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	if i := strings.LastIndexByte(cut, ' '); i > stderrCauseMax/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ") + "..."
}

// trimLogStamp removes the timestamp a server (or Go's own log package) puts in
// front of every line, so the note is the cause and not the clock.
func trimLogStamp(s string) string {
	if s == "" {
		return s
	}
	if s[0] == '[' {
		if end := strings.IndexByte(s, ']'); end > 0 && isStamp(s[1:end]) {
			return strings.TrimSpace(s[end+1:])
		}
	}
	// "2009/11/10 23:00:00 " — Go's default log prefix, and the shape most
	// language servers copy.
	if len(s) > 20 && isStamp(s[:10]) && s[10] == ' ' && isStamp(s[11:19]) {
		return strings.TrimSpace(s[19:])
	}
	return s
}

func isStamp(s string) bool {
	if s == "" {
		return false
	}
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '/' || r == '-' || r == ':' || r == '.' || r == 'T' || r == 'Z':
		default:
			return false
		}
	}
	return digits >= 4
}
