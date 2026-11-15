package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAged(t *testing.T, dir, name string, size int, age time.Duration) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	_ = os.Chtimes(p, old, old)
}

func survivors(dir string) map[string]bool {
	left := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		left[filepath.Base(f)] = true
	}
	return left
}

func TestBigSamplesLiveLongerThanSmall(t *testing.T) {
	st := newStore(t, Limits{TTL: time.Hour, BigTTL: 48 * time.Hour, MaxTotal: 1 << 20})
	writeAged(t, st.Dir, "small-old.json", 10, 2*time.Hour)
	writeAged(t, st.Dir, "a-big.json", 10, 24*time.Hour)
	writeAged(t, st.Dir, "b-big.json", 10, 72*time.Hour)
	st.Prune()
	left := survivors(st.Dir)
	if left["small-old.json"] || !left["a-big.json"] || left["b-big.json"] {
		t.Fatalf("unexpected survivors: %v", left)
	}
}

func TestSmallCapKeepsNewestAndSparesBig(t *testing.T) {
	st := newStore(t, Limits{TTL: 24 * time.Hour, BigTTL: 24 * time.Hour, MaxSmall: 2, MaxTotal: 1 << 20})
	writeAged(t, st.Dir, "s1.json", 10, 5*time.Hour)
	writeAged(t, st.Dir, "s2.json", 10, 4*time.Hour)
	writeAged(t, st.Dir, "s3.json", 10, 3*time.Hour)
	writeAged(t, st.Dir, "x-big.json", 10, 6*time.Hour)
	st.Prune()
	left := survivors(st.Dir)
	if left["s1.json"] || !left["s2.json"] || !left["s3.json"] || !left["x-big.json"] {
		t.Fatalf("unexpected survivors: %v", left)
	}
}

func TestSizeCapEvictsSmallBeforeBig(t *testing.T) {
	st := newStore(t, Limits{TTL: 24 * time.Hour, BigTTL: 24 * time.Hour, MaxTotal: 150})
	writeAged(t, st.Dir, "old-big.json", 100, 5*time.Hour) // oldest, but big
	writeAged(t, st.Dir, "new-small.json", 100, time.Hour)
	st.Prune()
	left := survivors(st.Dir)
	if !left["old-big.json"] || left["new-small.json"] {
		t.Fatalf("small must go before big: %v", left)
	}
}

func TestSaveMarksBigByName(t *testing.T) {
	st := newStore(t, DefaultLimits())
	_ = st.Save(Sample{At: time.Now(), Label: "a", RawBytes: BigRaw})
	_ = st.Save(Sample{At: time.Now().Add(time.Millisecond), Label: "b", RawBytes: BigRaw - 1})
	n := 0
	for name := range survivors(st.Dir) {
		if strings.HasSuffix(name, "-big.json") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 big file, got %d", n)
	}
}
