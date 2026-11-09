package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/transport"
)

// Graph search: a recursive text search for a plain identifier inside a git
// repository (grep -rn Name, rg Name, git grep Name, findstr /s Name) is answered
// from the code graph (where it is declared, with start-end lines, and every call
// site), which is what the search is for in code and costs a fraction of the raw
// output. Deterministic and lossless:
//   - only on host 'local', only inside a git repository, only for a pure
//     identifier, and only when the graph finds a declaration of it; anything
//     else (text, config keys, -v/-c/-l/-i searches, pipes) runs as usual;
//   - repeating the exact same command (same identity, within graphRepeatWindow)
//     runs the raw search.

var (
	reIdent      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
	reDeclPrefix = regexp.MustCompile(`^(?:func|def|class|struct|interface|type|fun|void|enum|record|object)\s+`)
)

type searchReq struct {
	pattern string
	paths   []string
}

// cleanSearchPattern reduces "\bFoo\b", "Foo\(", "func Foo" etc. to Foo; "" when it is not an identifier.
func cleanSearchPattern(p string) string {
	p = strings.TrimSpace(p)
	p = reDeclPrefix.ReplaceAllString(p, "")
	for _, s := range []string{`\b`, `\<`, `\>`, `\s*`, `\(`, `(`, `\s`} {
		p = strings.ReplaceAll(p, s, "")
	}
	p = strings.Trim(p, "^$ ")
	if !reIdent.MatchString(p) {
		return ""
	}
	return p
}

// parseIdentSearch reads a single-command recursive search. ok=false when the
// command is anything else or uses a flag whose output the graph cannot give.
func parseIdentSearch(cmd string) (searchReq, bool) {
	segs := shellSegments(cmd)
	if len(segs) != 1 {
		return searchReq{}, false
	}
	w := shellWords(segs[0])
	if len(w) < 2 {
		return searchReq{}, false
	}
	prog := strings.ToLower(strings.TrimSuffix(filepath.Base(w[0]), ".exe"))
	var rest []string
	recursive := false
	switch {
	case prog == "grep" || prog == "egrep" || prog == "fgrep":
		rest = w[1:]
	case prog == "rg":
		rest, recursive = w[1:], true
	case prog == "git" && len(w) > 2 && w[1] == "grep":
		rest, recursive = w[2:], true
	case prog == "findstr":
		rest = w[1:]
	default:
		return searchReq{}, false
	}
	var pos []string
	pattern := ""
	for i := 0; i < len(rest); i++ {
		t := rest[i]
		lt := strings.ToLower(t)
		switch {
		case t == "--":
			pos = append(pos, rest[i+1:]...)
			i = len(rest)
		case prog == "findstr" && strings.HasPrefix(t, "/"):
			switch lt {
			case "/s":
				recursive = true
			case "/n", "/r", "/c", "/l", "/p":
			default:
				if strings.HasPrefix(lt, "/c:") {
					pattern = t[3:]
				} else {
					return searchReq{}, false // /v, /i, /m, /x ...: saída diferente
				}
			}
		case strings.HasPrefix(t, "--"):
			name := strings.SplitN(lt, "=", 2)[0]
			switch name {
			case "--recursive", "--dereference-recursive":
				recursive = true
			case "--line-number", "--with-filename", "--no-heading", "--color", "--colour", "--word-regexp", "--extended-regexp", "--fixed-strings", "--perl-regexp", "--exclude-dir", "--exclude", "--hidden", "--no-ignore", "--glob", "--type":
				if !strings.Contains(t, "=") && (name == "--exclude-dir" || name == "--exclude" || name == "--glob" || name == "--type") {
					i++
				}
			case "--include":
				v := ""
				if j := strings.Index(t, "="); j >= 0 {
					v = t[j+1:]
				} else if i+1 < len(rest) {
					i++
					v = rest[i]
				}
				if codescan.LangFor("x"+strings.TrimPrefix(v, "*")) == nil {
					return searchReq{}, false // filtro só para arquivos que o grafo não indexa
				}
			default:
				return searchReq{}, false
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			flags := t[1:]
			for k, f := range flags {
				switch f {
				case 'r', 'R':
					recursive = true
				case 'n', 'H', 'w', 'E', 'F', 'P', 's', 'I':
				case 'e':
					if k == len(flags)-1 && i+1 < len(rest) {
						i++
						pattern = rest[i]
					}
				case 'g', 't', 'm':
					if k == len(flags)-1 {
						i++ // valor da flag
					}
				default:
					return searchReq{}, false // -v, -c, -l, -i, -o, -A/-B/-C ...: o grafo não dá essa saída
				}
			}
		default:
			pos = append(pos, t)
		}
	}
	if pattern == "" && len(pos) > 0 {
		pattern, pos = pos[0], pos[1:]
	}
	if !recursive {
		return searchReq{}, false
	}
	p := cleanSearchPattern(pattern)
	if p == "" {
		return searchReq{}, false
	}
	return searchReq{pattern: p, paths: pos}, true
}

// tryGraphSearch answers an identifier search from the graph. handled=false:
// run the command; note (when set) explains why the raw output comes.
func tryGraphSearch(args ExecArgs, label string) (out string, handled bool, note string) {
	if searchGuardOff() || os.Getenv("FASTEXEC_NO_GRAPH_SEARCH") != "" || len(args.Argv) > 0 {
		return "", false, ""
	}
	if t, err := transport.ResolveHost(args.Host); err != nil || !t.IsLocal {
		return "", false, "" // o grafo indexa só repositórios locais
	}
	req, ok := parseIdentSearch(label)
	if !ok {
		return "", false, ""
	}
	base := args.Cwd
	target := base
	if len(req.paths) > 1 {
		return "", false, ""
	}
	if len(req.paths) == 1 && !strings.ContainsAny(req.paths[0], "*?") {
		target = req.paths[0]
		if !filepath.IsAbs(target) {
			if base == "" {
				return "", false, ""
			}
			target = filepath.Join(base, target)
		}
	}
	if target == "" {
		return "", false, ""
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return "", false, ""
	}
	root, ok := graph.ResolveRepoRoot(target)
	if !ok {
		return "", false, ""
	}
	sub, _ := filepath.Rel(root, target)
	sub = filepath.ToSlash(sub)
	if sub == "." {
		sub = ""
	}

	def, err := graph.Definition(root, req.pattern)
	if err != nil || strings.HasPrefix(def, "(no definition") {
		return "", false, ""
	}
	def = keepUnder(def, sub)
	if !strings.Contains(def, "\n- ") {
		return "", false, ""
	}
	key := fmt.Sprintf("search|%s|%s", strings.ToLower(args.Cwd), label) // sem identidade: ver graph_first.go
	if graphRepeat(key) {
		return "", false, GraphMarkRepeat + ": raw search output because this exact command was repeated."
	}
	callers, _ := graph.Callers(root, req.pattern, 60)
	callers = keepUnder(callers, sub)

	where := filepath.Base(root)
	if sub != "" {
		where += "/" + sub
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: '%s' answered from the code graph of %s (declarations with start-end lines, then call sites). Text matches in comments, strings, docs and non-code files are not listed: repeat this exact command for the raw search.\n", GraphMarkSearch, req.pattern, where)
	b.WriteString(strings.TrimSpace(def))
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(callers))
	return b.String(), true, ""
}

// keepUnder keeps only the "- path..." entries (and their indented detail lines)
// under sub ("" keeps all); header and footer lines stay.
func keepUnder(s, sub string) string {
	if sub == "" {
		return s
	}
	var out []string
	keep := true
	for _, l := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(l, "- "):
			keep = strings.HasPrefix(l[2:], sub+"/")
			if keep {
				out = append(out, l)
			}
		case strings.HasPrefix(l, "  "):
			if keep {
				out = append(out, l)
			}
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
