// root_guard.go: guard de sistema do exec. Busca ou listagem recursiva sobre uma raiz ampla (raiz de disco, C:\Users,
// /, /home) varreria o PC inteiro: minutos de CPU e uma saída que estoura o contexto. É bloqueada antes de executar,
// inclusive quando o caminho é "." e o cwd é a raiz ampla. Desligar: FASTEXEC_NO_SEARCH_GUARD=1.
// Letra de unidade (C:, W:) só é raiz no host Windows; num host Linux 'W:' é um nome de pasta comum.
package tools

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"homelab/fastexec/internal/transport"
)

var broadUnixRoots = map[string]bool{"/": true, "/home": true, "/users": true, "/root": true}

// isBroadRoot: raiz Unix ampla e, no host Windows (win), raiz de disco (C:\, D:) ou pasta de perfis (C:\Users);
// com ou sem curinga final.
func isBroadRoot(p string, win bool) bool {
	p = strings.Trim(strings.TrimSpace(p), `"'`)
	if p == "" {
		return false
	}
	if i := strings.IndexAny(p, "*?"); i >= 0 {
		p = p[:i]
		if p == "" {
			return false
		}
	}
	p = strings.ReplaceAll(p, `\`, "/")
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	lower := strings.ToLower(p)
	if broadUnixRoots[lower] {
		return true
	}
	if win && len(lower) >= 2 && lower[1] == ':' && isLetter(lower[0]) {
		rest := lower[2:]
		return rest == "" || rest == "/users"
	}
	return false
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' }

var rgFlagsWithValue = map[string]bool{"-g": true, "--glob": true, "--iglob": true, "-t": true, "--type": true,
	"-T": true, "--type-not": true, "-e": true, "--regexp": true, "-f": true, "--file": true, "-A": true, "-B": true,
	"-C": true, "-m": true, "--max-count": true, "-j": true, "--threads": true, "-M": true, "--max-columns": true,
	"-d": true, "--max-depth": true}

// recursiveScanPaths diz se o trecho do comando varre recursivamente e quais caminhos ("." quando nenhum).
func recursiveScanPaths(tokens []string) ([]string, bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	prog := strings.ToLower(strings.TrimSuffix(filepath.Base(tokens[0]), ".exe"))
	args := tokens[1:]
	has := func(pred func(string) bool) bool {
		for _, a := range args {
			if pred(a) {
				return true
			}
		}
		return false
	}
	nonFlags := func(prefix string) []string {
		var out []string
		for _, a := range args {
			if !strings.HasPrefix(a, prefix) && !strings.ContainsAny(a, "<>|") {
				out = append(out, a)
			}
		}
		return out
	}
	switch prog {
	case "grep", "egrep", "fgrep":
		g, _ := parseGrepArgs(tokens)
		return g.paths, g.recursive
	case "rg":
		var pos []string
		patternGiven := false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case rgFlagsWithValue[a]:
				if a == "-e" || a == "--regexp" || a == "-f" || a == "--file" {
					patternGiven = true
				}
				i++
			case strings.HasPrefix(a, "-"):
			default:
				pos = append(pos, a)
			}
		}
		if !patternGiven && len(pos) > 0 {
			pos = pos[1:]
		}
		return pos, true
	case "find":
		var pos []string
		for _, a := range args {
			if strings.HasPrefix(a, "-") || a == "(" || a == "!" {
				break
			}
			pos = append(pos, a)
		}
		return pos, true
	case "findstr":
		if !has(func(a string) bool { return strings.EqualFold(a, "/s") }) {
			return nil, false
		}
		return nonFlags("/"), true
	case "dir":
		if !has(func(a string) bool { return strings.EqualFold(a, "/s") }) {
			return nil, false
		}
		return nonFlags("/"), true
	case "ls":
		if !has(func(a string) bool {
			return a == "--recursive" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "R"))
		}) {
			return nil, false
		}
		return nonFlags("-"), true
	case "get-childitem", "gci":
		if !has(func(a string) bool { return strings.HasPrefix(strings.ToLower(a), "-r") }) {
			return nil, false
		}
		var pos []string
		for i := 0; i < len(args); i++ {
			a := strings.ToLower(args[i])
			switch {
			case a == "-path" || a == "-literalpath":
				if i+1 < len(args) {
					pos = append(pos, args[i+1])
				}
				i++
			case strings.HasPrefix(a, "-"):
			default:
				pos = append(pos, args[i])
			}
		}
		return pos, true
	case "tree":
		return nonFlags("/"), true
	}
	return nil, false
}

// broadScan devolve a raiz ampla que o comando varreria recursivamente ("" se nenhuma).
func broadScan(args ExecArgs) string { return broadScanOn(args, hostIsWindows(args.Host)) }

// hostIsWindows: o host do comando é este computador e ele é Windows (os remotos são Linux/OpenWrt).
func hostIsWindows(host string) bool {
	t, err := transport.ResolveHost(host)
	return err == nil && t.IsLocal && runtime.GOOS == "windows"
}

func broadScanOn(args ExecArgs, win bool) string {
	if searchGuardOff() {
		return ""
	}
	var segs [][]string
	if len(args.Argv) > 0 {
		segs = [][]string{args.Argv}
	} else {
		for _, s := range shellSegments(args.Command) {
			segs = append(segs, shellWords(s))
		}
	}
	for _, tokens := range segs {
		paths, recursive := recursiveScanPaths(tokens)
		if !recursive {
			continue
		}
		if len(paths) == 0 {
			paths = []string{"."}
		}
		for _, p := range paths {
			if isDotPath(p) {
				if isBroadRoot(args.Cwd, win) {
					return args.Cwd
				}
				continue
			}
			if isBroadRoot(p, win) {
				return p
			}
		}
	}
	return ""
}

func broadScanError(root string) error {
	return fmt.Errorf("[FastExec Guard] Recursive scan of '%s' blocked: it would walk a whole disk or every user profile. "+
		"Point it at a project folder (path or cwd); inside a git repository use 'code_outline' or 'code_graph'. "+
		"Override: FASTEXEC_NO_SEARCH_GUARD=1.", root)
}
