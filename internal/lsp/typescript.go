package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TypeScript/JavaScript specifics.
//
// typescript-language-server is only a front end: it drives a `tsserver` that
// lives inside the `typescript` package. Without one it starts and exits
// immediately with "Could not find a valid TypeScript installation", which to a
// user looks like the server is broken. So the launch has to hand it a path,
// and the path has to be a real tsserver.
//
// The version matters, not just the presence: `typescript` 7 removed
// lib/tsserver.js (the native port), so a package that is installed but has no
// lib/tsserver.js is not usable by this server at all. Every candidate below is
// checked for that file, so a broken prefix degrades to "no tsserver" instead of
// failing inside initialize.
//
// Preferring the *project's* own TypeScript is the same choice VS Code makes:
// the answer is then type-checked with the compiler version the project builds
// with, and a repository pinning an old TypeScript gets that TypeScript's
// notion of the answer.

// tsserverEnv overrides everything: the escape hatch for a TypeScript that is
// installed somewhere none of the conventions reach.
const tsserverEnv = "FASTEXEC_TSSERVER"

// tsserverRel is the entry point inside a `typescript` package.
var tsserverRel = []string{"lib", "tsserver.js"}

// projectScanDepth bounds the walk up from the repository root looking for a
// hoisted/workspace-level node_modules.
const projectScanDepth = 8

// FindTSServer resolves the tsserver to hand to the server, or "" when none of
// the candidates exists. bin is the resolved server executable, root the
// repository root the server is being started for.
func FindTSServer(bin, root string) string {
	if p := validTSServer(strings.TrimSpace(os.Getenv(tsserverEnv))); p != "" {
		return p
	}
	// 1. The project's own TypeScript, nearest node_modules first: this is what
	//    makes the answer match the project's compiler version.
	if root != "" {
		dir := root
		for i := 0; i < projectScanDepth; i++ {
			if p := validTSServer(filepath.Join(dir, "node_modules", "typescript", filepath.Join(tsserverRel...))); p != "" {
				return p
			}
			parent := filepath.Dir(dir)
			if parent == dir || parent == "" {
				break
			}
			dir = parent
		}
	}
	// 2. The TypeScript shipped next to the server itself: the flat npm prefix
	//    layout, `npm -g` on Unix, and the Windows global prefix.
	for _, dir := range serverPrefixes(bin) {
		for _, rel := range [][]string{
			{"node_modules", "typescript"},
			{"lib", "node_modules", "typescript"},
			{"typescript"},
		} {
			if p := validTSServer(filepath.Join(dir, filepath.Join(append(rel, tsserverRel...)...))); p != "" {
				return p
			}
		}
	}
	// 3. FastExec's own drop-in directory and the extra search path.
	for _, dir := range append([]string{dataLSPDir()}, searchDirs()...) {
		if p := validTSServer(filepath.Join(dir, "ts", "node_modules", "typescript", filepath.Join(tsserverRel...))); p != "" {
			return p
		}
		if p := validTSServer(filepath.Join(dir, "node_modules", "typescript", filepath.Join(tsserverRel...))); p != "" {
			return p
		}
	}
	// 4. Last: a `tsc` on PATH, which means the package is next to it.
	if p, err := exec.LookPath("tsc"); err == nil {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		if cand := validTSServer(filepath.Join(filepath.Dir(p), "..", "lib", "node_modules", "typescript", filepath.Join(tsserverRel...))); cand != "" {
			return cand
		}
		if cand := validTSServer(filepath.Join(filepath.Dir(p), "node_modules", "typescript", filepath.Join(tsserverRel...))); cand != "" {
			return cand
		}
	}
	return ""
}

// serverPrefixes lists the npm prefix directories a server executable could
// belong to, for a binary at bin.
func serverPrefixes(bin string) []string {
	if bin == "" {
		return nil
	}
	dir := filepath.Dir(bin)
	out := []string{dir, filepath.Dir(dir)}
	// <prefix>/node_modules/.bin/<server>
	if base := strings.ToLower(filepath.Base(dir)); base == ".bin" {
		out = append(out, filepath.Dir(filepath.Dir(dir)))
	}
	seen := map[string]bool{}
	uniq := out[:0]
	for _, d := range out {
		if d == "" || d == "." || seen[strings.ToLower(d)] {
			continue
		}
		seen[strings.ToLower(d)] = true
		uniq = append(uniq, d)
	}
	return uniq
}

// validTSServer returns path when it is a usable tsserver entry point, and ""
// otherwise.
//
// The result is a native path on purpose: the server splits tsserver.path on
// the platform separator, so a forward-slash path is not merely ugly on Windows,
// it fails to resolve.
func validTSServer(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	fi, err := os.Stat(clean)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return ""
	}
	return clean
}

// tsInitOptions builds the initializationOptions for one launch. Everything in
// it is there for one of two reasons: the tsserver path, without which the
// server does not run at all, and the inlay hints, which are what makes
// `lsp-tipos` show inferred types for generics and JSX props instead of nothing.
func tsInitOptions(bin, root string) map[string]any {
	opts := map[string]any{
		"hostInfo": "fastexec",
		"preferences": map[string]any{
			// The hints `lsp-tipos` renders: inferred parameter names, inferred
			// variable/property types, inferred return types, enum values.
			"includeInlayParameterNameHints":           "all",
			"includeInlayVariableTypeHints":            true,
			"includeInlayPropertyDeclarationTypeHints": true,
			"includeInlayFunctionLikeReturnTypeHints":  true,
			"includeInlayEnumMemberValueHints":         true,
		},
	}
	if p := FindTSServer(bin, root); p != "" {
		opts["tsserver"] = map[string]any{
			"path": p,
			// "never": the syntax-only server answers single-file questions
			// fast but has no project, so cross-file references and rename come
			// back incomplete. An exact answer is the whole point of this layer.
			"useSyntaxServer": "never",
			"logVerbosity":    "off",
		}
	}
	return opts
}

// TSServerFor is FindTSServer, exported for `lsp doctor`: a user whose TS
// server is installed but silent needs to see that no tsserver was found.
func TSServerFor(bin, root string) string { return FindTSServer(bin, root) }

// tsDoctorDetails tells `lsp doctor` which tsserver the server would be handed,
// or that there is none. Without a root the search skips the project and reports
// only what is installed next to the server itself, which is exactly the case
// that looks like a broken server: found, and dies in the first second.
func tsDoctorDetails(bin string) []string {
	p := FindTSServer(bin, "")
	if p == "" {
		return []string{
			"tsserver: not found — the server starts and exits immediately without one",
			"install typescript in the project (npm i -D typescript) or next to the server, or point FASTEXEC_TSSERVER at its lib/tsserver.js",
		}
	}
	return []string{"tsserver: " + p}
}

// ImplicitProject reports whether the server will load this root without a
// config file, i.e. with TypeScript's default compiler options instead of the
// project's. A language whose StrictMarkers are all absent is in that state.
func ImplicitProject(lang Language, root string) bool {
	if len(lang.StrictMarkers) == 0 {
		return false
	}
	for _, m := range lang.StrictMarkers {
		if _, err := os.Stat(filepath.Join(root, m)); err == nil {
			return false
		}
	}
	return true
}

// ProjectWarning is the honesty line for a project the server can only guess at.
// Empty when there is nothing to warn about.
func ProjectWarning(lang Language, root string) string {
	if !ImplicitProject(lang, root) {
		return ""
	}
	if lang.ID == "typescript" {
		return "no tsconfig: by name for cross-file"
	}
	return "no " + lang.StrictMarkers[0] + ": by name for cross-file"
}
