package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// suggestPaths lists existing local paths close to a missing one: same name
// with other case or extension, small typos, or the nearest existing parent
// folder. Used on "file not found" so the model does not have to list folders.
func suggestPaths(missing string) []string {
	dir, base := filepath.Dir(missing), filepath.Base(missing)
	// sobe até a pasta existente mais próxima
	walked := false
	for i := 0; i < 6; i++ {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		base = filepath.Base(dir)
		dir = parent
		walked = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 5000 {
		return nil
	}
	lb := strings.ToLower(base)
	stem := strings.TrimSuffix(lb, filepath.Ext(lb))
	type cand struct {
		path  string
		score int
	}
	var cs []cand
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		s := -1
		switch {
		case n == lb:
			s = 0 // só diferença de maiúsculas
		case strings.TrimSuffix(n, filepath.Ext(n)) == stem && stem != "":
			s = 1 // outra extensão
		default:
			if d := levenshtein(n, lb); d <= max(2, len(lb)/4) {
				s = 2 + d
			} else if len(stem) >= 4 && strings.Contains(n, stem) {
				s = 8
			}
		}
		if s >= 0 {
			cs = append(cs, cand{filepath.Join(dir, e.Name()), s})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].score < cs[j].score })
	var out []string
	for i, c := range cs {
		if i == 5 {
			break
		}
		out = append(out, c.path)
	}
	if len(out) == 0 && walked {
		out = append(out, dir+string(filepath.Separator)+" (nearest existing folder)")
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > 64 || len(rb) > 64 {
		return 99
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// notFoundWithSuggestions enriches a local "not found" error.
func notFoundWithSuggestions(path string, err error) error {
	if !os.IsNotExist(err) {
		return err
	}
	s := suggestPaths(path)
	if len(s) == 0 {
		return err
	}
	return &suggestErr{err: err, msg: err.Error() + "\nDid you mean:\n  " + strings.Join(s, "\n  ")}
}

type suggestErr struct {
	err error
	msg string
}

func (e *suggestErr) Error() string { return e.msg }
func (e *suggestErr) Unwrap() error { return e.err }
