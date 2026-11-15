package corpus

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
)

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
	name := s.At.Format("20060102-150405.000") + "-" + hex.EncodeToString(h[:3]) + ".json"
	return os.WriteFile(filepath.Join(f.Dir, name), b, 0o600)
}

// Prune deletes samples older than the TTL, then the oldest ones while the store exceeds MaxTotal.
func (f *FileStore) Prune() {
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
		size int64
	}
	var keep []item
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(f.Dir, e.Name())
		if time.Since(info.ModTime()) > f.Lim.TTL {
			_ = os.Remove(p)
			continue
		}
		keep = append(keep, item{p, info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].mod.Before(keep[j].mod) })
	for _, it := range keep {
		if total <= f.Lim.MaxTotal {
			break
		}
		if os.Remove(it.path) == nil {
			total -= it.size
		}
	}
}

var pruned sync.Map // Store -> struct{}

// pruneOnce prunes each store once per process (the first save after start).
func pruneOnce(s Store) {
	if _, loaded := pruned.LoadOrStore(s, struct{}{}); !loaded {
		s.Prune()
	}
}
