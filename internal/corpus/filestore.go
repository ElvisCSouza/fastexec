package corpus

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
)

// bigSuffix marks the file of a big sample (raw output of BigRaw bytes or more). Retention reads the name, never the
// content: big outputs are the ones a filter really changes, so they live longer and are never evicted by small ones.
const bigSuffix = "-big.json"

// FileStore keeps one JSON file per sample in Dir.
type FileStore struct {
	Dir string
	Lim Limits
}

// Dir is the folder samples are written to (FASTEXEC_CORPUS_DIR overrides the data folder).
func Dir() string {
	if dir := os.Getenv("FASTEXEC_CORPUS_DIR"); dir != "" {
		return dir
	}
	return config.Get().Dir("corpus")
}

func defaultStore() Store {
	dir := Dir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &FileStore{Dir: dir, Lim: LimitsFromEnv()}
}

// Save writes the sample. The name is time plus a short hash of the label, so it sorts by age.
func (f *FileStore) Save(s Sample) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	h := sha1.Sum([]byte(s.Label))
	name := s.At.Format("20060102-150405.000") + "-" + hex.EncodeToString(h[:3])
	if s.RawBytes >= BigRaw {
		name += bigSuffix
	} else {
		name += ".json"
	}
	return os.WriteFile(filepath.Join(f.Dir, name), b, 0o600)
}

type stored struct {
	path string
	mod  time.Time
	size int64
}

// Prune applies retention: samples older than their TTL go (small: TTL, big: BigTTL); then the oldest small ones
// beyond MaxSmall; then, while the store exceeds MaxTotal, the oldest small ones and, only after them, the oldest big.
func (f *FileStore) Prune() {
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return
	}
	var small, big []stored
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := stored{path: filepath.Join(f.Dir, e.Name()), mod: info.ModTime(), size: info.Size()}
		isBig := strings.HasSuffix(e.Name(), bigSuffix)
		ttl := f.Lim.TTL
		if isBig {
			ttl = f.Lim.BigTTL
		}
		if time.Since(it.mod) > ttl {
			_ = os.Remove(it.path)
			continue
		}
		total += it.size
		if isBig {
			big = append(big, it)
		} else {
			small = append(small, it)
		}
	}
	oldestFirst := func(s []stored) {
		sort.Slice(s, func(i, j int) bool { return s[i].mod.Before(s[j].mod) })
	}
	oldestFirst(small)
	oldestFirst(big)
	drop := func(it stored) {
		if os.Remove(it.path) == nil {
			total -= it.size
		}
	}
	if f.Lim.MaxSmall > 0 && len(small) > f.Lim.MaxSmall {
		for _, it := range small[:len(small)-f.Lim.MaxSmall] {
			drop(it)
		}
		small = small[len(small)-f.Lim.MaxSmall:]
	}
	for _, it := range append(small, big...) { // small first, then big, each oldest first
		if total <= f.Lim.MaxTotal {
			break
		}
		drop(it)
	}
}

const pruneEvery = 10 * time.Minute

var (
	prunedMu sync.Mutex
	prunedAt = map[Store]time.Time{}
)

// pruneIfDue prunes a store at most every pruneEvery: the daemon lives for days, so a prune only at start would let
// samples outlive their TTL.
func pruneIfDue(s Store) {
	prunedMu.Lock()
	due := time.Since(prunedAt[s]) >= pruneEvery
	if due {
		prunedAt[s] = time.Now()
	}
	prunedMu.Unlock()
	if due {
		s.Prune()
	}
}
