package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func newStore(t *testing.T, lim Limits) *FileStore {
	t.Helper()
	return &FileStore{Dir: t.TempDir(), Lim: lim}
}

func readAll(t *testing.T, dir string) []Sample {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []Sample
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s Sample
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestNilCaptureIsHarmless(t *testing.T) {
	var c *Capture
	c.SetRaw("a", "b", 0, 1)
	c.Finish("x") // must not panic
}

func TestEnvZeroTurnsItOffEvenIfConfigAsksForIt(t *testing.T) {
	t.Setenv("FASTEXEC_CORPUS", "0")
	if Enabled() || Begin("local", "ls") != nil {
		t.Fatal("FASTEXEC_CORPUS=0 must win over the config file")
	}
}

func TestEnvOneTurnsItOn(t *testing.T) {
	t.Setenv("FASTEXEC_CORPUS", "1")
	t.Setenv("FASTEXEC_CORPUS_DIR", t.TempDir())
	if !Enabled() || Begin("local", "ls") == nil {
		t.Fatal("FASTEXEC_CORPUS=1 must turn it on")
	}
}

func TestCaptureSavesRawAndReturned(t *testing.T) {
	st := newStore(t, DefaultLimits())
	c := newCapture(st, DefaultLimits(), DefaultRedactors(), "local", "git log")
	c.SetRaw("out", "err", 2, 40)
	c.Finish("returned text")
	got := readAll(t, st.Dir)
	if len(got) != 1 {
		t.Fatalf("want 1 sample, got %d", len(got))
	}
	s := got[0]
	if s.Label != "git log" || s.ExitCode != 2 || s.DurationMs != 40 || s.Returned != "returned text" {
		t.Fatalf("unexpected sample %+v", s)
	}
	if !strings.Contains(s.Raw, "out") || !strings.Contains(s.Raw, "[stderr]\nerr") {
		t.Fatalf("raw lost stdout or stderr: %q", s.Raw)
	}
}

func TestCaptureWithoutRawSavesNothing(t *testing.T) {
	st := newStore(t, DefaultLimits())
	c := newCapture(st, DefaultLimits(), nil, "local", "cat x")
	c.Finish("remediated before running") // no SetRaw: no command ran
	if n := len(readAll(t, st.Dir)); n != 0 {
		t.Fatalf("want 0 samples, got %d", n)
	}
}

func TestRawTruncatedAtLimit(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxRaw = 100
	st := newStore(t, lim)
	c := newCapture(st, lim, nil, "local", "big")
	c.SetRaw(strings.Repeat("x", 500), "", 0, 1)
	c.Finish("r")
	s := readAll(t, st.Dir)[0]
	if len(s.Raw) != 100 || !s.RawTruncated || s.RawBytes != 500 {
		t.Fatalf("raw=%d truncated=%v raw_bytes=%d", len(s.Raw), s.RawTruncated, s.RawBytes)
	}
}

// Um rune multibyte posicionado para cair exatamente na fronteira do teto de 256 KB: o corte em
// bytes pegaria só parte dele e deixaria UTF-8 inválido. O corte recua até o início do rune.
func TestRawCutAtLimitNeverSplitsRune(t *testing.T) {
	lim := DefaultLimits()                 // 256 KB
	for _, r := range []string{"日", "😀"} { // 3 e 4 bytes
		raw := strings.Repeat("a", lim.MaxRaw-len(r)+1) + r + "tail"
		st := newStore(t, lim)
		c := newCapture(st, lim, nil, "local", "big")
		c.SetRaw(raw, "", 0, 1)
		c.Finish("r")
		s := readAll(t, st.Dir)[0]
		if !s.RawTruncated {
			t.Fatalf("%q: sample deveria estar truncada", r)
		}
		if !utf8.ValidString(s.Raw) {
			t.Errorf("%q: raw truncado não é UTF-8 válido (últimos bytes: %x)", r, s.Raw[len(s.Raw)-4:])
		}
		if len(s.Raw) > lim.MaxRaw {
			t.Errorf("%q: teto violado: raw=%d max=%d", r, len(s.Raw), lim.MaxRaw)
		}
		want := strings.Repeat("a", lim.MaxRaw-len(r)+1)
		if s.Raw != want {
			t.Errorf("%q: corte não recuou até a fronteira do rune: raw=%d want=%d", r, len(s.Raw), len(want))
		}
		if s.RawBytes != len(raw) {
			t.Errorf("%q: raw_bytes=%d, esperado %d", r, s.RawBytes, len(raw))
		}
	}
}

func TestRedaction(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdefghijklmnop1234":                               "abcdefghijklmnop1234",
		"token=ghp_abcdefghijklmnopqrstuvwxyz0123":                                 "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"DB_PASSWORD=hunter2hunter2":                                               "hunter2hunter2",
		"{\n  \"password\": \"Zq9xLm2Vb7Nc4Rt8\",\n  \"hostname\": \"0.0.0.0\"\n}": "Zq9xLm2Vb7Nc4Rt8",
		"{\"api_key\":\"abc123def456ghi789\"}":                                     "abc123def456ghi789",
		"url postgres://user:s3cretpw@host/db":                                     "s3cretpw",
		"key sk-abcdefghijklmnopqrstuvwx":                                          "sk-abcdefghijklmnopqrstuvwx",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----":   "MIIabc",
	}
	for in, secret := range cases {
		out := Redact(in, DefaultRedactors())
		if strings.Contains(out, secret) {
			t.Errorf("secret %q survived in %q", secret, out)
		}
	}
	plain := "file main.go changed, 3 tests passed"
	if Redact(plain, DefaultRedactors()) != plain {
		t.Error("ordinary text must pass untouched")
	}
}

func TestPruneTTLAndTotal(t *testing.T) {
	lim := Limits{MaxRaw: 1 << 20, TTL: time.Hour, MaxTotal: 250}
	st := newStore(t, lim)
	write := func(name string, size int, age time.Duration) {
		p := filepath.Join(st.Dir, name)
		if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-age)
		_ = os.Chtimes(p, old, old)
	}
	write("expired.json", 10, 2*time.Hour)
	write("old.json", 200, 30*time.Minute)
	write("new.json", 200, time.Minute)
	st.Prune()
	left := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(st.Dir, "*.json"))
	for _, f := range files {
		left[filepath.Base(f)] = true
	}
	if left["expired.json"] || left["old.json"] || !left["new.json"] {
		t.Fatalf("unexpected survivors: %v", left)
	}
}
