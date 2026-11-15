package corpus

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Targets the corpus must reach before the comparison (docs: spec-fase3-comparacao).
const (
	MinTotal       = 200
	MinPerCategory = 20
	// BigRaw is the raw size from which an output is worth filtering: the exec soft cap (10 KB).
	BigRaw = 10 * 1024
)

// DefaultExcludes drops calls that are about the corpus itself (checks run while building it).
var DefaultExcludes = []string{"corpus"}

// Load reads every sample in dir. Unreadable files are skipped and counted.
func Load(dir string) (samples []Sample, skipped int) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		var s Sample
		if err != nil || json.Unmarshal(b, &s) != nil {
			skipped++
			continue
		}
		samples = append(samples, s)
	}
	return samples, skipped
}

// CategoryStat is the tally of one family.
type CategoryStat struct {
	Name   string
	N      int
	Errors int // exit code != 0
	Big    int // raw output of BigRaw bytes or more
	Tools  map[string]int
}

// Summary is what the report shows.
type Summary struct {
	Total      int // samples read
	Excluded   int // matched an exclusion
	Duplicates int // same command and same output as an earlier sample
	Skipped    int // unreadable files
	Used       int
	Categories []CategoryStat // by size, largest first
}

// Summarize classifies the samples. A sample is excluded when its label contains any exclusion (case-insensitive).
func Summarize(samples []Sample, skipped int, excludes []string) Summary {
	sum := Summary{Total: len(samples), Skipped: skipped}
	seen := map[string]bool{}
	cats := map[string]*CategoryStat{}
	for _, s := range samples {
		if matchesAny(s.Label, excludes) {
			sum.Excluded++
			continue
		}
		h := sha1.Sum([]byte(s.Label + "\x00" + s.Raw))
		if key := hex.EncodeToString(h[:]); seen[key] {
			sum.Duplicates++
			continue
		} else {
			seen[key] = true
		}
		tool := Tool(s.Label)
		c := cats[Category(tool)]
		if c == nil {
			c = &CategoryStat{Name: Category(tool), Tools: map[string]int{}}
			cats[c.Name] = c
		}
		c.N++
		c.Tools[tool]++
		if s.ExitCode != 0 {
			c.Errors++
		}
		if s.RawBytes >= BigRaw {
			c.Big++
		}
		sum.Used++
	}
	for _, c := range cats {
		sum.Categories = append(sum.Categories, *c)
	}
	sort.Slice(sum.Categories, func(i, j int) bool {
		if sum.Categories[i].N != sum.Categories[j].N {
			return sum.Categories[i].N > sum.Categories[j].N
		}
		return sum.Categories[i].Name < sum.Categories[j].Name
	})
	return sum
}

func matchesAny(label string, subs []string) bool {
	l := strings.ToLower(label)
	for _, s := range subs {
		if s != "" && strings.Contains(l, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// Render writes the report in Portuguese, like the measurement report.
func Render(s Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Corpus de saídas reais: %d amostras lidas\n", s.Total)
	fmt.Fprintf(&b, "  usadas %d | fora da conta %d (comandos sobre o próprio corpus) | repetidas %d | ilegíveis %d\n", s.Used, s.Excluded, s.Duplicates, s.Skipped)
	status := "ok"
	if s.Used < MinTotal {
		status = fmt.Sprintf("faltam %d", MinTotal-s.Used)
	}
	fmt.Fprintf(&b, "  meta total: %d (%s)\n\n", MinTotal, status)
	fmt.Fprintf(&b, "Por família. A meta é de %d amostras GRANDES (saída bruta de %d KB ou mais) em cada família: só nelas um filtro muda algo.\n", MinPerCategory, BigRaw/1024)
	for _, c := range s.Categories {
		st := "ok"
		if c.Big < MinPerCategory {
			st = fmt.Sprintf("faltam %d grandes", MinPerCategory-c.Big)
		}
		fmt.Fprintf(&b, "  %-11s %4d amostras | %3d grandes | %3d com erro | %s\n", c.Name, c.N, c.Big, c.Errors, st)
		fmt.Fprintf(&b, "              %s\n", topTools(c.Tools, 6))
	}
	if missing := missingFamilies(s); len(missing) > 0 {
		fmt.Fprintf(&b, "\nFamílias sem nenhuma amostra: %s\n", strings.Join(missing, ", "))
	}
	if ready := readyFamilies(s); len(ready) > 0 {
		fmt.Fprintf(&b, "Prontas para comparar: %s\n", strings.Join(ready, ", "))
	} else {
		fmt.Fprintf(&b, "Nenhuma família pronta para comparar ainda.\n")
	}
	return b.String()
}

// wanted are the families the comparison needs.
var wanted = []string{"git", "build_test", "listagem", "busca", "logs"}

func missingFamilies(s Summary) []string {
	have := map[string]bool{}
	for _, c := range s.Categories {
		have[c.Name] = true
	}
	var out []string
	for _, w := range wanted {
		if !have[w] {
			out = append(out, w)
		}
	}
	return out
}

// readyFamilies are the wanted families that reached the big-sample target.
func readyFamilies(s Summary) []string {
	var out []string
	for _, c := range s.Categories {
		if c.Big >= MinPerCategory && isWanted(c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

func isWanted(name string) bool {
	for _, w := range wanted {
		if w == name {
			return true
		}
	}
	return false
}

func topTools(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if len(all) > n {
		all = all[:n]
	}
	parts := make([]string, len(all))
	for i, e := range all {
		parts[i] = fmt.Sprintf("%s %d", e.k, e.v)
	}
	return strings.Join(parts, ", ")
}

// RenderDir loads dir and renders the report with the default exclusions plus extra.
func RenderDir(dir string, extra []string) string {
	samples, skipped := Load(dir)
	return Render(Summarize(samples, skipped, append(append([]string{}, DefaultExcludes...), extra...)))
}
