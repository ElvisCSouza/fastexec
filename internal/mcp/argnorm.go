package mcp

// argnorm.go: no parameter is ever dropped in silence. Before a tool runs, its JSON arguments are matched against the
// handler's own struct: known spellings (StartLine, file_path, cmd, target on exec...) are rewritten to the canonical
// name and reported in a footer, harmless metadata keys are ignored and reported, anything else is refused with the
// valid names. The json decoder used to discard unknown keys, so a read_file with StartLine/EndLine returned the whole
// 346 KB file and an exec with target='local' ran on the default host.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// aliasTable: canonical spelling (lower case, no '_', '-' or space) -> field name, per tool; "*" applies to every
// tool. An alias only applies when the destination field exists in the tool's arguments and was not also sent.
var aliasTable = map[string]map[string]string{
	"*": {
		"filepath": "path", "file": "path", "filename": "path", "absolutepath": "path", "targetfile": "path",
		"byteoffset": "offset", "range": "ranges", "cmd": "command",
	},
	"read_file":    {"target": "path"},
	"write_file":   {"target": "path"},
	"code_outline": {"target": "path"},
	"exec":         {"target": "host"},
	"job":          {"target": "host"},
	"file_ops":     {"op": "action", "operation": "action"},
	"edit_file":    {"texttoreplace": "target", "oldtext": "target", "newtext": "replacement"},
	"sql":          {"db": "database", "sql": "query"},
}

// ignorableKeys: metadata some clients attach to every call. They cannot change what the tool does, so they are
// ignored (and said so) instead of failing the call.
var ignorableKeys = map[string]bool{
	"description": true, "reason": true, "explanation": true, "rationale": true, "thought": true, "comment": true,
	"intent": true, "toolaction": true, "toolsummary": true, "waitmsbeforeasync": true,
}

// lenientTools keep their own decoders (custom UnmarshalJSON with extra synonyms) and are not checked.
var lenientTools = map[string]bool{"get_file": true, "put_file": true}

// extraKeys: spellings the tool's own UnmarshalJSON already understands (they must pass untouched).
var extraKeys = map[string][]string{
	"edit_file": {"old_string", "old_str", "search", "find", "new_string", "new_str", "replace", "content"},
}

type fieldSet struct {
	names map[string]bool   // exact json names
	canon map[string]string // canonical spelling -> json name
}

var (
	fieldSetsMu sync.Mutex
	fieldSets   = map[reflect.Type]*fieldSet{}
)

func canonKey(s string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(s))
}

// fieldsOf lists the json names of a struct type (cached).
func fieldsOf(t reflect.Type) *fieldSet {
	fieldSetsMu.Lock()
	defer fieldSetsMu.Unlock()
	if fs, ok := fieldSets[t]; ok {
		return fs
	}
	fs := &fieldSet{names: map[string]bool{}, canon: map[string]string{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fs.names[name] = true
		fs.canon[canonKey(name)] = name
	}
	fieldSets[t] = fs
	return fs
}

func strictArgs() bool { return os.Getenv("FASTEXEC_STRICT_ARGS") != "0" }

// normalizeArgs returns the arguments to run with and the notes to show the model. The common case (every key is
// already canonical) returns raw untouched and allocates nothing beyond the key scan.
func normalizeArgs(tool string, fs *fieldSet, raw json.RawMessage) (json.RawMessage, []string, error) {
	if lenientTools[tool] || len(raw) == 0 {
		return raw, nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return raw, nil, nil // not an object: the handler reports it as before
	}
	extra := map[string]bool{}
	for _, k := range extraKeys[tool] {
		extra[k] = true
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	clean := true
	for _, k := range keys {
		if !fs.names[k] && !extra[k] && !strings.HasPrefix(k, "_") {
			clean = false
			break
		}
	}
	if clean {
		return raw, nil, nil
	}

	var renamed, ignored, unknown []string
	for _, k := range keys {
		if fs.names[k] || extra[k] || strings.HasPrefix(k, "_") {
			continue
		}
		c := canonKey(k)
		dest := fs.canon[c]
		if dest == "" {
			if d, ok := aliasTable[tool][c]; ok && fs.names[d] {
				dest = d
			} else if d, ok := aliasTable["*"][c]; ok && fs.names[d] {
				dest = d
			}
		}
		switch {
		case dest != "":
			if _, dup := m[dest]; dup {
				ignored = append(ignored, fmt.Sprintf("'%s' (%s was already given)", k, dest))
			} else {
				m[dest] = m[k]
				renamed = append(renamed, fmt.Sprintf("%s -> %s", k, dest))
			}
			delete(m, k)
		case ignorableKeys[c]:
			ignored = append(ignored, fmt.Sprintf("'%s' (metadata)", k))
			delete(m, k)
		default:
			unknown = append(unknown, k)
		}
	}

	if len(unknown) > 0 {
		msg := fmt.Sprintf("unknown parameter(s) for %s: %s. Valid: %s.", tool, strings.Join(quoteAll(unknown), ", "), validList(fs))
		if s := suggest(unknown[0], fs); s != "" {
			msg += fmt.Sprintf(" Did you mean '%s'?", s)
		} else if tool == "job" && canonKey(unknown[0]) == canonKey("timeout_ms") {
			msg += " 'wait' blocks up to 45 s per call and takes no timeout; call it again to keep waiting."
		}
		if strictArgs() {
			return raw, nil, fmt.Errorf("%s Nothing was run.", msg)
		}
		for _, k := range unknown {
			ignored = append(ignored, fmt.Sprintf("'%s' (unknown)", k))
			delete(m, k)
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		return raw, nil, nil
	}
	var notes []string
	if len(renamed) > 0 {
		notes = append(notes, "read as: "+strings.Join(renamed, ", "))
	}
	if len(ignored) > 0 {
		notes = append(notes, "ignored: "+strings.Join(ignored, ", "))
	}
	return out, notes, nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "'" + s + "'"
	}
	return out
}

func validList(fs *fieldSet) string {
	var v []string
	for n := range fs.names {
		if !strings.HasPrefix(n, "_") {
			v = append(v, n)
		}
	}
	sort.Strings(v)
	return strings.Join(v, ", ")
}

// suggest returns the valid name closest to k (edit distance <= 2 on the canonical spelling, or containing it).
func suggest(k string, fs *fieldSet) string {
	c := canonKey(k)
	best, bestD := "", 3
	for canon, name := range fs.canon {
		if strings.HasPrefix(name, "_") {
			continue
		}
		d := editDistance(c, canon)
		if len(c) >= 4 && (strings.Contains(canon, c) || strings.Contains(c, canon)) {
			d = 1
		}
		if d < bestD || (d == bestD && name < best) {
			best, bestD = name, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// argSpecs is filled by typed() in tool_handlers.go: tool name -> its argument fields.
var argSpecs = map[string]*fieldSet{}

// normalizeCall is the entry used by DispatchTool. Tools without a typed spec (module tools) pass through.
func normalizeCall(tool string, raw json.RawMessage) (json.RawMessage, []string, error) {
	fs := argSpecs[tool]
	if fs == nil {
		return raw, nil, nil
	}
	return normalizeArgs(tool, fs, raw)
}

// withNotes appends the adjustment line to a result. sql returns plain JSON, so its notes are not appended.
func withNotes(tool string, notes []string, out string) string {
	if len(notes) == 0 || tool == "sql" {
		return out
	}
	return strings.TrimRight(out, "\r\n") + "\n[FastExec] Parameters adjusted (" + strings.Join(notes, "; ") + "). Use the canonical names next time."
}
