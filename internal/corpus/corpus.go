// Package corpus records real exec outputs so filters and third-party tools can be compared offline, with no model
// calls. It is opt-in (FASTEXEC_CORPUS=1), local only, redacted, size-capped and short-lived.
//
// Layers: Sample is the domain value; Store is the port (FileStore is the adapter); Redactor is a Strategy list;
// Capture ties one exec call to a Store and is nil-safe, so a disabled corpus costs the caller nothing.
package corpus

import (
	"homelab/fastexec/internal/config"
	"os"
	"strconv"
	"time"
	"unicode/utf8"
)

// Sample is one recorded exec call. Raw is what the command printed (stdout then stderr); Returned is what
// FastExec handed back to the model, kept so a candidate filter can be compared with the current one.
type Sample struct {
	At            time.Time `json:"at"`
	Host          string    `json:"host"`
	Label         string    `json:"label"`
	ExitCode      int       `json:"exit"`
	DurationMs    int64     `json:"ms"`
	RawBytes      int       `json:"raw_bytes"`
	Raw           string    `json:"raw"`
	RawTruncated  bool      `json:"raw_truncated,omitempty"`
	ReturnedBytes int       `json:"returned_bytes"`
	Returned      string    `json:"returned"`
}

// Store is the persistence port.
type Store interface {
	Save(s Sample) error
	Prune()
}

// Limits bound what is kept.
type Limits struct {
	MaxRaw   int           // bytes of Raw kept per sample
	TTL      time.Duration // small samples older than this are deleted
	BigTTL   time.Duration // same for big samples (raw output of BigRaw bytes or more)
	MaxSmall int           // small samples kept; the oldest beyond this go
	MaxTotal int64         // bytes in the store; small samples go first, then the oldest big ones
}

// DefaultLimits: 256 KB per sample; small 72 h and 500 of them; big 14 days; 50 MB in all.
func DefaultLimits() Limits {
	return Limits{MaxRaw: 256 << 10, TTL: 72 * time.Hour, BigTTL: 14 * 24 * time.Hour, MaxSmall: 500, MaxTotal: 50 << 20}
}

// Enabled reports whether recording was asked for: FASTEXEC_CORPUS=1/0 in the environment wins, otherwise the
// "corpus" setting of the config file (the daemon does not always inherit the AI client's environment).
func Enabled() bool {
	switch os.Getenv("FASTEXEC_CORPUS") {
	case "1":
		return true
	case "0":
		return false
	}
	return config.Get().Corpus
}

// LimitsFromEnv applies FASTEXEC_CORPUS_TTL_H, FASTEXEC_CORPUS_BIG_TTL_H, FASTEXEC_CORPUS_MAX_SMALL and
// FASTEXEC_CORPUS_MAX_MB over the defaults.
func LimitsFromEnv() Limits {
	l := DefaultLimits()
	if n, err := strconv.Atoi(os.Getenv("FASTEXEC_CORPUS_TTL_H")); err == nil && n > 0 {
		l.TTL = time.Duration(n) * time.Hour
	}
	if n, err := strconv.Atoi(os.Getenv("FASTEXEC_CORPUS_BIG_TTL_H")); err == nil && n > 0 {
		l.BigTTL = time.Duration(n) * time.Hour
	}
	if n, err := strconv.Atoi(os.Getenv("FASTEXEC_CORPUS_MAX_SMALL")); err == nil && n > 0 {
		l.MaxSmall = n
	}
	if n, err := strconv.Atoi(os.Getenv("FASTEXEC_CORPUS_MAX_MB")); err == nil && n > 0 {
		l.MaxTotal = int64(n) << 20
	}
	return l
}

// Capture accumulates one call. A nil *Capture is valid and does nothing.
type Capture struct {
	store    Store
	lim      Limits
	redact   []Redactor
	host     string
	label    string
	started  time.Time
	hasRaw   bool
	raw      string
	exitCode int
	durMs    int64
}

// Begin opens a capture for one exec call, or returns nil when the corpus is off.
func Begin(host, label string) *Capture {
	if !Enabled() {
		return nil
	}
	st := defaultStore()
	if st == nil {
		return nil
	}
	return newCapture(st, LimitsFromEnv(), DefaultRedactors(), host, label)
}

func newCapture(st Store, lim Limits, rs []Redactor, host, label string) *Capture {
	return &Capture{store: st, lim: lim, redact: rs, host: host, label: label, started: time.Now()}
}

// SetRaw notes what the command printed. Only calls that actually ran a command reach it.
func (c *Capture) SetRaw(stdout, stderr string, exitCode int, durationMs int64) {
	if c == nil {
		return
	}
	c.raw = stdout
	if stderr != "" {
		c.raw += "\n[stderr]\n" + stderr
	}
	c.hasRaw, c.exitCode, c.durMs = true, exitCode, durationMs
}

// Finish saves the sample if a command ran. Errors are swallowed: recording must never affect the call.
func (c *Capture) Finish(returned string) {
	if c == nil || !c.hasRaw {
		return
	}
	raw, trunc := c.raw, false
	if len(raw) > c.lim.MaxRaw {
		// The cap is in bytes, but the cut backs off to a rune boundary: a truncated
		// sample is never left with half a character, and the cap stays respected.
		cut := c.lim.MaxRaw
		for cut > 0 && !utf8.RuneStart(raw[cut]) {
			cut--
		}
		raw, trunc = raw[:cut], true
	}
	s := Sample{
		At: c.started.UTC(), Host: c.host, Label: Redact(c.label, c.redact),
		ExitCode: c.exitCode, DurationMs: c.durMs,
		RawBytes: len(c.raw), Raw: Redact(raw, c.redact), RawTruncated: trunc,
		ReturnedBytes: len(returned), Returned: Redact(returned, c.redact),
	}
	if err := c.store.Save(s); err == nil {
		pruneIfDue(c.store)
	}
}

// Redact applies every redactor in order.
func Redact(s string, rs []Redactor) string {
	for _, r := range rs {
		s = r.Apply(s)
	}
	return s
}
