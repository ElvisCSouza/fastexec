package telemetry

import (
	"unicode/utf8"

	"homelab/fastexec/internal/corpus"
)

// sanitizeEntry returns a copy of e with every free-text field masked and
// converted to valid UTF-8 (see corpus.Sanitize). It is the single point where
// telemetry text is prepared for the database, so every write goes through it.
// Numbers, flags, hashes and session fields are not text and are left alone.
func sanitizeEntry(e AuditEntry) AuditEntry {
	e.Host = corpus.Sanitize(e.Host)
	e.Tool = corpus.Sanitize(e.Tool)
	e.ErrorMessage = corpus.Sanitize(e.ErrorMessage)
	e.CommandSummary = corpus.Sanitize(e.CommandSummary)
	e.Client = corpus.Sanitize(e.Client)
	e.Conversation = corpus.Sanitize(e.Conversation)
	e.Marks = corpus.Sanitize(e.Marks)
	e.Doc = corpus.Sanitize(e.Doc)
	e.DocFrom = corpus.Sanitize(e.DocFrom)

	if e.DocLinks != nil {
		links := make([]string, len(e.DocLinks))
		for i, l := range e.DocLinks {
			links[i] = corpus.Sanitize(l)
		}
		e.DocLinks = links
	}
	if e.MarkDefs != nil {
		defs := make([]MarkDef, len(e.MarkDefs))
		for i, d := range e.MarkDefs {
			defs[i] = MarkDef{Mark: corpus.Sanitize(d.Mark), Template: corpus.Sanitize(d.Template)}
		}
		e.MarkDefs = defs
	}
	if e.Savings != nil {
		items := make([]Saving, len(e.Savings))
		for i, s := range e.Savings {
			s.Note = corpus.Sanitize(s.Note)
			items[i] = s
		}
		e.Savings = items
	}
	return e
}

// truncate masks the text first and then cuts it at maxLen runes (the last three
// become "..."). Masking before cutting keeps a credential from surviving as a
// fragment that no longer matches a rule; cutting by runes keeps UTF-8 valid.
func truncate(s string, maxLen int) string {
	s = corpus.Sanitize(s)
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	r := []rune(s)
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	return string(r[:maxLen-3]) + "..."
}

// cutRunes returns s unchanged when it has at most n runes; otherwise it keeps
// the first n runes and appends suffix. It never splits a UTF-8 sequence. The
// caller masks s first (see corpus.Sanitize).
func cutRunes(s string, n int, suffix string) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + suffix
}
