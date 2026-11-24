package telemetry

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// CutDetail is what a cut left out: the numbers the model needs to decide whether to read the rest.
// Saving rows written before these columns existed have no detail and count as unknown, not zero.
type CutDetail struct {
	OrigLines int    `json:"orig_lines"`
	OrigBytes int    `json:"orig_bytes"`
	RetLines  int    `json:"ret_lines"`
	RetBytes  int    `json:"ret_bytes"`
	OmitLines int    `json:"omit_lines"`
	OmitBytes int    `json:"omit_bytes"`
	Reason    string `json:"reason"` // "teto"
	Filter    string `json:"filter"` // "head+tail", "head only", "byte cut (one line)"
	Cursor    string `json:"cursor"` // the call that brings back the omitted part
	Spill     string `json:"spill"`  // path of the redacted full text, "" when nothing was spilled
}

// NoteCut records a cut as a saving (before = raw length, so redaction never counts as saved bytes)
// with its detail. It records nothing when the output was not made smaller.
func NoteCut(before, after int, note string, d CutDetail) {
	if before > after {
		add(Saving{Cat: CatCut, Bytes: before - after, Exact: true, Before: before, After: after, Note: note, Cut: &d})
	}
}

// cutColumns are the nullable columns of the saving table that hold a CutDetail. They are added by
// the migration in setupAuditSchema; cut_recovered is 0 or 1 for cut rows and NULL for everything else.
var cutColumns = map[string]string{
	"cut_orig_lines": "INTEGER",
	"cut_orig_bytes": "INTEGER",
	"cut_ret_lines":  "INTEGER",
	"cut_ret_bytes":  "INTEGER",
	"cut_omit_lines": "INTEGER",
	"cut_omit_bytes": "INTEGER",
	"cut_reason":     "TEXT",
	"cut_filter":     "TEXT",
	"cut_cursor":     "TEXT",
	"cut_spill":      "TEXT",
	"cut_recovered":  "INTEGER",
}

// cutArgs returns the values for the detail columns (cutColumns minus cut_recovered) in a fixed order;
// nil yields SQL NULLs.
func cutArgs(c *CutDetail) []any {
	if c == nil {
		return make([]any, 10)
	}
	return []any{c.OrigLines, c.OrigBytes, c.RetLines, c.RetBytes, c.OmitLines, c.OmitBytes,
		c.Reason, c.Filter, c.Cursor, c.Spill}
}

// MarkCutRecovered records that the agent read a spill file back with read_file. Only cut rows whose
// spill is that file are touched. The cut's saving is left as recorded: the read_file call that brings
// the text back is its own audit row, so the recovered share is shown next to the savings instead.
func MarkCutRecovered(path string) {
	if dbInstance == nil {
		Init()
	}
	if dbInstance == nil || path == "" {
		return
	}
	_, err := dbInstance.Exec(`UPDATE saving SET cut_recovered = 1 WHERE cut_spill IS NOT NULL AND lower(cut_spill) = lower(?)`,
		filepath.Clean(path))
	noteFailure("cut recovery", err)
}

// cutSummary is the recovery view of the cut rows in a window. Recovery is only what is measured:
// a spill was opened by read_file at least once (cut_recovered). That does not say how much of the
// omitted text was read, so the omitted bytes of accessed cuts are never called recovered.
type cutSummary struct {
	cuts, accessed, unknown int
	neverAccessedOmit       int // omitted bytes of cuts whose spill was never opened
}

func queryCutSummary(db *sql.DB, where string) (cutSummary, error) {
	var s cutSummary
	err := db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN cut_recovered = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cut_omit_bytes IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(cut_recovered, 0) = 0 THEN cut_omit_bytes ELSE 0 END), 0)
		FROM saving WHERE cat = ? AND (`+where+`)`, CatCut).
		Scan(&s.cuts, &s.accessed, &s.unknown, &s.neverAccessedOmit)
	return s, err
}

// renderCutRecovery is the block printed under stats --measure. Redacted text is not in the omitted
// bytes (the returned text and the counts come from the raw output), so it is never counted as loss.
// Reads never change the savings.
func renderCutRecovery(s cutSummary) string {
	if s.cuts == 0 {
		return ""
	}
	known := s.cuts - s.unknown
	var b strings.Builder
	fmt.Fprintf(&b, "\nCut recovery (%d cuts; %d with detail, %d older rows unknown)\n", s.cuts, known, s.unknown)
	fmt.Fprintf(&b, "  spill opened with read_file at least once: %d cuts\n", s.accessed)
	fmt.Fprintf(&b, "  omitted bytes of cuts never opened: %d\n", s.neverAccessedOmit)
	fmt.Fprintf(&b, "  an open is not full recovery: a partial read (offset, limit, pattern, ranges) may leave\n")
	fmt.Fprintf(&b, "  part of the omitted text unseen, and the bytes actually read are not measured\n")
	fmt.Fprintf(&b, "  the spill is redacted: masked secrets are not counted as omitted or as loss\n")
	return b.String()
}
