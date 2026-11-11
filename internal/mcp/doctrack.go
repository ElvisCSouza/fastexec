package mcp

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
)

// Markdown read map. For every successful read of a .md file (read_file, or exec 'cat x.md' on a single file) the
// telemetry records the document and, when the text of a .md read shortly before by the same identity mentions it,
// where the agent came from. The link is only recorded when the earlier text really contains the reference (a link,
// a path or the file name), so "LLM.md -> modulo_x/claude.md" means the agent read LLM.md, LLM.md names that file,
// and the agent opened it next. Only paths are stored, never content.

const (
	docWindow   = 30 * time.Minute
	docKeep     = 30  // recent documents remembered per identity
	docMaxRefs  = 300 // references kept per document
	docMaxIdent = 200
)

type docSeen struct {
	key  string            // host|lowercased normalized path
	path string            // normalized path as stored
	refs map[string]string // lowercased resolved reference -> "link" | "dir"
	base map[string]bool   // lowercased file names mentioned
	at   time.Time
}

var (
	docMu     sync.Mutex
	docRecent = map[string][]*docSeen{}

	homeRe    = regexp.MustCompile(`^/(?:home/[^/]+|root)(/|$)`)
	mdLinkRe  = regexp.MustCompile(`\]\(\s*<?([^)\s>#]+)`)
	mdPathRe  = regexp.MustCompile(`(?i)(?:~|[a-z]:)?[\w.~/\\-]*[\w-]\.md\b`)
	mdDirRe   = regexp.MustCompile("`([\\w.~/\\\\:-]+[/\\\\])`")
	catMdRe   = regexp.MustCompile(`(?i)^\s*(?:cat|type|get-content|gc)\s+(?:"([^"]+\.md)"|'([^']+\.md)'|(\S+\.md))\s*$`)
	driveRe   = regexp.MustCompile(`^[A-Za-z]:/`)
	numPrefix = regexp.MustCompile(`(?m)^\s*\d+[\t:|]`)
)

// normDocPath: forward slashes, /home/<user>/ and /root/ -> ~/, cleaned. Case is kept for display.
func normDocPath(p string) string {
	p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"'`))
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, `\`, "/")
	p = homeRe.ReplaceAllString(p, "~$1")
	return path.Clean(p)
}

func isAbsDoc(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || driveRe.MatchString(p)
}

// resolveRef turns a reference found in doc text into a normalized path, relative ones against the doc's folder.
func resolveRef(ref, dir string) string {
	ref = strings.TrimPrefix(ref, "file://")
	if strings.Contains(ref, "://") {
		return ""
	}
	r := normDocPath(ref)
	if r == "" || r == "." {
		return ""
	}
	if !isAbsDoc(r) && dir != "" {
		r = path.Clean(dir + "/" + r)
	}
	return r
}

// docTarget returns the .md path of a call that reads a markdown file, or "".
func docTarget(tool string, rawArgs json.RawMessage) string {
	var a struct {
		Path    string   `json:"path"`
		Command string   `json:"command"`
		Argv    []string `json:"argv"`
	}
	if json.Unmarshal(rawArgs, &a) != nil {
		return ""
	}
	switch tool {
	case "read_file":
		if strings.HasSuffix(strings.ToLower(a.Path), ".md") {
			return a.Path
		}
	case "exec":
		if m := catMdRe.FindStringSubmatch(a.Command); m != nil {
			return m[1] + m[2] + m[3]
		}
		if len(a.Argv) == 2 && strings.HasSuffix(strings.ToLower(a.Argv[1]), ".md") {
			switch strings.ToLower(strings.TrimSuffix(path.Base(strings.ReplaceAll(a.Argv[0], `\`, "/")), ".exe")) {
			case "cat", "type":
				return a.Argv[1]
			}
		}
	}
	return ""
}

// extractRefs lists the markdown files and folders a document's text points to.
func extractRefs(text, dir string) (refs map[string]string, base map[string]bool, links []string) {
	refs, base = map[string]string{}, map[string]bool{}
	text = numPrefix.ReplaceAllString(text, "") // numbered reads: drop "  12\t"
	add := func(raw, kind string) {
		if len(refs) >= docMaxRefs {
			return
		}
		r := resolveRef(raw, dir)
		if r == "" {
			return
		}
		k := strings.ToLower(r)
		if _, ok := refs[k]; ok {
			return
		}
		refs[k] = kind
		if kind == "link" {
			base[strings.ToLower(path.Base(r))] = true
			links = append(links, r)
		}
	}
	for _, m := range mdLinkRe.FindAllStringSubmatch(text, -1) {
		if strings.HasSuffix(strings.ToLower(m[1]), ".md") {
			add(m[1], "link")
		} else if strings.HasSuffix(m[1], "/") {
			add(m[1], "dir")
		}
	}
	for _, m := range mdPathRe.FindAllString(text, -1) {
		add(m, "link")
	}
	for _, m := range mdDirRe.FindAllStringSubmatch(text, -1) {
		add(m[1], "dir")
	}
	return
}

// trackDoc records a markdown read for identity and returns (doc, from, how, links): doc is the normalized path read,
// from the earlier document that mentions it ("" if none), how is "link", "dir", "name", "none" (other docs were read
// recently but none mentions it) or "first" (no doc read recently), links the .md files this text points to.
func trackDoc(identity, tool, host string, rawArgs json.RawMessage, out string, ok bool) (doc, from, how string, links []string) {
	defer func() { // telemetry must never break a tool response
		if recover() != nil {
			doc, from, how, links = "", "", "", nil
		}
	}()
	target := docTarget(tool, rawArgs)
	if target == "" || !ok {
		return "", "", "", nil
	}
	if host == "" {
		host = config.Get().Default() // the host the tools themselves use when the call names none
	}
	doc = normDocPath(target)
	if doc == "" {
		return "", "", "", nil
	}
	low := strings.ToLower(doc)
	key := strings.ToLower(host) + "|" + low
	dir := path.Dir(doc)
	if dir == "." {
		dir = ""
	}
	refs, base, links := extractRefs(out, dir)
	now := time.Now()

	docMu.Lock()
	defer docMu.Unlock()
	if len(docRecent) > docMaxIdent {
		docRecent = map[string][]*docSeen{}
	}
	list := docRecent[identity]
	how = "first"
	for i := len(list) - 1; i >= 0; i-- {
		d := list[i]
		if now.Sub(d.at) > docWindow || d.key == key || !strings.HasPrefix(d.key, strings.ToLower(host)+"|") {
			continue
		}
		how = "none"
		if kind, hit := d.refs[low]; hit && kind == "link" {
			from, how = d.path, "link"
			break
		}
		matched := false
		for r, kind := range d.refs {
			if kind == "dir" && strings.HasPrefix(low, strings.TrimSuffix(r, "/")+"/") {
				matched = true
				break
			}
		}
		if matched {
			from, how = d.path, "dir"
			break
		}
		if d.base[strings.ToLower(path.Base(doc))] {
			from, how = d.path, "name"
			break
		}
	}
	// keep the newest view of this document (refs from what the agent saw now)
	kept := list[:0]
	for _, d := range list {
		if d.key != key && now.Sub(d.at) <= docWindow {
			kept = append(kept, d)
		}
	}
	kept = append(kept, &docSeen{key: key, path: doc, refs: refs, base: base, at: now})
	if len(kept) > docKeep {
		kept = kept[len(kept)-docKeep:]
	}
	docRecent[identity] = kept
	return doc, from, how, links
}
