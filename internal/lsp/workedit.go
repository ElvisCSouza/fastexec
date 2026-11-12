package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/textenc"
)

// A WorkspaceEdit is what a server returns for rename, quickfix and
// organize-imports. FastExec never lets the server write: the edit comes back
// here and this package is the only thing that touches the filesystem.
//
// Two shapes exist in the protocol. `changes` is a bare map and carries no
// order; `documentChanges` is an ordered array that can also create, rename and
// delete files. Both are accepted, and `documentChanges` wins when a server
// (incorrectly) sends both.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []DocumentChange      `json:"documentChanges,omitempty"`
}

// DocumentChange is one entry of documentChanges. The protocol makes this a
// union discriminated by `kind`; every field is optional and the discriminator
// decides which ones are meaningful, so a plain struct decodes both arms.
type DocumentChange struct {
	// Text edit arm.
	TextDocument *VersionedTextDocumentIdentifier `json:"textDocument,omitempty"`
	Edits        []TextEdit                       `json:"edits,omitempty"`

	// Resource operation arm.
	Kind    string          `json:"kind,omitempty"`
	URI     string          `json:"uri,omitempty"`
	OldURI  string          `json:"oldUri,omitempty"`
	NewURI  string          `json:"newUri,omitempty"`
	Options json.RawMessage `json:"options,omitempty"`
}

// VersionedTextDocumentIdentifier names a document and, optionally, the version
// the edit was computed against. A nil version means "any".
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version,omitempty"`
}

type resourceOptions struct {
	Overwrite         bool `json:"overwrite"`
	IgnoreIfNotExists bool `json:"ignoreIfNotExists"`
	Recursive         bool `json:"recursive"`
}

func (we *WorkspaceEdit) empty() bool {
	return len(we.Changes) == 0 && len(we.DocumentChanges) == 0
}

// Files lists every path the edit touches, in no particular order. Used to
// report the impact of an edit before applying it.
func (we *WorkspaceEdit) Files() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for uri := range we.Changes {
		add(URItoPath(uri))
	}
	for _, dc := range we.DocumentChanges {
		if dc.TextDocument != nil {
			add(URItoPath(dc.TextDocument.URI))
		}
		add(URItoPath(dc.URI))
		add(URItoPath(dc.OldURI))
		add(URItoPath(dc.NewURI))
	}
	sort.Strings(out)
	return out
}

// Sizes counts, per absolute path, how many text edits the edit carries (a
// create, rename or delete counts as one). It answers "how big is this rename"
// before anything is written, which is what the caller needs to decide whether
// to apply it at all.
func (we *WorkspaceEdit) Sizes() (map[string]int, error) {
	ops, err := we.operations()
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(ops))
	for _, op := range ops {
		n := len(op.edits)
		if op.kind != opEdit {
			n = 1
		}
		if n == 0 {
			n = 1
		}
		out[op.path] += n
	}
	return out, nil
}

// Spots lists where the edit changes text: one location per text edit, at the start of the range it
// replaces; a create, rename or delete is reported once, at the file. Callers use it to tell the model
// which file:line moved, not just which files — the same job Sites does for an exact answer.
func (we *WorkspaceEdit) Spots() ([]Location, error) {
	ops, err := we.operations()
	if err != nil {
		return nil, err
	}
	var out []Location
	for _, op := range ops {
		if op.kind != opEdit {
			out = append(out, Location{URI: PathToURI(op.path)})
			continue
		}
		for _, e := range op.edits {
			out = append(out, Location{URI: PathToURI(op.path), Range: e.Range})
		}
	}
	return out, nil
}

// ResultFor returns what this edit would make of text, for one file, without
// touching the disk. ok is false when the edit has nothing for that path.
//
// It exists so a caller can check an edit before applying it: the file the user
// is editing already has a known post-image, and the server's version of that
// same file must agree with it, or the edit is about something else and must
// not be applied.
func (we *WorkspaceEdit) ResultFor(path, text string) (result string, ok bool, err error) {
	abs, err := absPath(path)
	if err != nil {
		return "", false, err
	}
	ops, err := we.operations()
	if err != nil {
		return "", false, err
	}
	for _, op := range ops {
		if op.kind != opEdit || op.path != abs {
			continue
		}
		eol := textenc.LineEnding(text)
		if eol == "" {
			eol = "\n"
		}
		out, err := ApplyTextEdits(text, op.edits, eol)
		if err != nil {
			return "", false, err
		}
		return out, true, nil
	}
	return "", false, nil
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

type opKind int

const (
	opEdit opKind = iota
	opCreate
	opRename
	opDelete
)

type editOp struct {
	kind    opKind
	path    string // absolute; for opRename this is the source
	newPath string // opRename only
	version *int
	edits   []TextEdit
	res     resourceOptions
}

// operations normalises both edit shapes into one ordered list. Order matters:
// documentChanges is applied as sent, and `changes` (unordered by definition) is
// applied first in path order so the result is deterministic.
func (we *WorkspaceEdit) operations() ([]editOp, error) {
	var ops []editOp
	if len(we.DocumentChanges) == 0 {
		uris := make([]string, 0, len(we.Changes))
		for uri := range we.Changes {
			uris = append(uris, uri)
		}
		sort.Strings(uris)
		for _, uri := range uris {
			op, err := editOpFor(uri, nil, we.Changes[uri])
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		}
		return ops, nil
	}
	for _, dc := range we.DocumentChanges {
		var opts resourceOptions
		if len(dc.Options) > 0 {
			_ = json.Unmarshal(dc.Options, &opts)
		}
		switch dc.Kind {
		case "":
			if dc.TextDocument == nil {
				return nil, errors.New("lsp: documentChanges entry has neither kind nor textDocument")
			}
			op, err := editOpFor(dc.TextDocument.URI, dc.TextDocument.Version, dc.Edits)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
		case "create":
			p, err := fileOf(dc.URI)
			if err != nil {
				return nil, err
			}
			ops = append(ops, editOp{kind: opCreate, path: p, res: opts})
		case "rename":
			from, err := fileOf(dc.OldURI)
			if err != nil {
				return nil, err
			}
			to, err := fileOf(dc.NewURI)
			if err != nil {
				return nil, err
			}
			ops = append(ops, editOp{kind: opRename, path: from, newPath: to, res: opts})
		case "delete":
			p, err := fileOf(dc.URI)
			if err != nil {
				return nil, err
			}
			ops = append(ops, editOp{kind: opDelete, path: p, res: opts})
		default:
			return nil, fmt.Errorf("lsp: unknown documentChanges kind %q", dc.Kind)
		}
	}
	return ops, nil
}

func editOpFor(uri string, version *int, edits []TextEdit) (editOp, error) {
	p, err := fileOf(uri)
	if err != nil {
		return editOp{}, err
	}
	return editOp{kind: opEdit, path: p, version: version, edits: edits}, nil
}

// fileOf rejects anything that is not a local file. A server asking FastExec to
// edit an http:// or untitled: document is refused rather than silently skipped.
func fileOf(uri string) (string, error) {
	if !strings.HasPrefix(uri, "file:") {
		return "", fmt.Errorf("lsp: refusing non-file URI %q", uri)
	}
	p := URItoPath(uri)
	if p == "" {
		return "", fmt.Errorf("lsp: empty path in URI %q", uri)
	}
	return absPath(p)
}

// ---------------------------------------------------------------------------
// Applying
// ---------------------------------------------------------------------------

// ApplyOptions controls how an edit is written.
type ApplyOptions struct {
	// Root is the repository root. Paths inside the patch are stored relative to
	// it so undo works from any working directory.
	Root string
	// Versions maps an absolute path to the document version FastExec last sent
	// to the server. A mismatch means the server computed the edit against bytes
	// that are no longer on disk, and the whole edit is refused: applying half a
	// rename is worse than applying none.
	Versions map[string]int
	// UndoDir receives the reverse patch. Empty disables the patch (tests, dry
	// runs).
	UndoDir string
	// Skip lists absolute paths this call must not write, even though the edit
	// touches them. A caller that is writing one file itself (the file the user
	// asked to edit) passes it here so the server's edit for that file is
	// computed, checked and then left alone: writing it twice would either
	// duplicate the change or undo it.
	Skip map[string]bool
	// DryRun computes everything and writes nothing.
	DryRun bool
	// Logf, when set, receives one line per touched file.
	Logf func(format string, args ...any)
}

// ApplyResult reports what an applied edit did.
type ApplyResult struct {
	Edited  []string
	Created []string
	Renamed []string // "old -> new"
	Deleted []string
	// PatchPath is the reverse patch, empty when none was written.
	PatchPath string
	// Undone is true when the call was an undo rather than an apply.
	Undone bool
}

// Summary is a one-line description for the caller's report.
func (r *ApplyResult) Summary(verb string) string {
	parts := []string{}
	if n := len(r.Edited); n > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) edited", n))
	}
	if n := len(r.Created); n > 0 {
		parts = append(parts, fmt.Sprintf("%d created", n))
	}
	if n := len(r.Renamed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d renamed", n))
	}
	if n := len(r.Deleted); n > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", n))
	}
	if len(parts) == 0 {
		return verb + ": nothing to change"
	}
	return verb + ": " + strings.Join(parts, ", ")
}

// ApplyWorkspaceEdit writes a WorkspaceEdit to disk.
//
// All or nothing: every target is read and every new content computed in memory
// before the first byte is written, and a failure half way rolls the already
// written files back from the snapshots taken up front.
func ApplyWorkspaceEdit(we *WorkspaceEdit, opts ApplyOptions) (*ApplyResult, error) {
	if we == nil || we.empty() {
		return &ApplyResult{}, nil
	}
	ops, err := we.operations()
	if err != nil {
		return nil, err
	}
	if len(opts.Skip) > 0 {
		kept := make([]editOp, 0, len(ops))
		for _, op := range ops {
			if opts.Skip[op.path] {
				continue
			}
			kept = append(kept, op)
		}
		ops = kept
		if len(ops) == 0 {
			return &ApplyResult{}, nil
		}
	}
	if err := checkNoVendored(ops); err != nil {
		return nil, err
	}
	if err := checkVersions(ops, opts.Versions); err != nil {
		return nil, err
	}

	// Snapshot + compute, in memory, before touching anything.
	plan := make([]pendingEdit, 0, len(ops))
	for _, op := range ops {
		p := pendingEdit{op: op}
		if op.kind == opEdit || op.kind == opRename || op.kind == opDelete {
			data, err := os.ReadFile(op.path)
			if err != nil {
				if op.res.IgnoreIfNotExists && os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("lsp: %s: %w", op.path, err)
			}
			p.before, p.hadFile = data, true
			if fi, err := os.Stat(op.path); err == nil {
				p.mode = fi.Mode().Perm()
			}
		}
		if p.mode == 0 {
			p.mode = 0o644
		}
		if op.kind == opCreate {
			if _, err := os.Stat(op.path); err == nil && !op.res.Overwrite {
				return nil, fmt.Errorf("lsp: %s already exists (overwrite not allowed)", op.path)
			}
		}
		if op.kind == opEdit {
			text, enc := textenc.Decode(string(p.before))
			eol := textenc.LineEnding(text)
			if eol == "" {
				eol = "\n"
			}
			newText, err := applyTextEdits(text, op.edits, eol)
			if err != nil {
				return nil, fmt.Errorf("lsp: %s: %w", op.path, err)
			}
			if newText == text {
				continue // server sent a no-op; do not rewrite the file
			}
			encoded, ok := textenc.Encode(newText, enc)
			if !ok {
				return nil, fmt.Errorf("lsp: %s: new text does not fit %s; edit refused", op.path, enc)
			}
			p.after = []byte(encoded)
			p.oldLines, p.newLines = splitLines(text), splitLines(newText)
		}
		if op.kind == opRename {
			// A rename carries the bytes over unchanged; keep the mode too.
			p.after = p.before
		}
		p.relPath = relTo(opts.Root, op.path)
		plan = append(plan, p)
	}

	res := &ApplyResult{}
	if len(plan) == 0 {
		return res, nil
	}

	// Reverse patch first: it is derived from the plan and an unwritable undo
	// directory must stop the edit before it is applied, not after.
	var patch strings.Builder
	for i := range plan {
		p := &plan[i]
		switch p.op.kind {
		case opEdit:
			patch.WriteString(UnifiedDiff(p.relPath, p.oldLines, p.newLines))
		case opCreate:
			fmt.Fprintf(&patch, "%s\n--- a/%s\n+++ b/%s\n@@ -0,0 +1,%d @@\n",
				patchHeader, p.relPath, p.relPath, len(splitLines(string(p.after))))
			for _, l := range splitLines(string(p.after)) {
				patch.WriteString("+" + l.text + "\n")
			}
		case opDelete:
			// The file is about to be gone, so the encoding it was written in
			// cannot be re-detected at undo time and travels with the patch.
			fmt.Fprintf(&patch, "%s\n--- a/%s\n%s%s\n+++ b/%s\n@@ -1,%d +0,0 @@\n",
				patchHeader, p.relPath, encPrefix, encodingOf(string(p.before)),
				p.relPath, len(splitLines(string(p.before))))
			for _, l := range splitLines(string(p.before)) {
				patch.WriteString("-" + l.text + "\n")
			}
		case opRename:
			// Undo of a rename is a rename back; recorded as a comment so the
			// patch file stays a faithful record of the transaction.
			fmt.Fprintf(&patch, "%s%s%s%s\n", renamePrefix, p.relPath, renameSep, relTo(opts.Root, p.op.newPath))
		}
	}
	if opts.DryRun {
		return planToResult(plan, opts.Root), nil
	}
	if opts.UndoDir != "" {
		path, err := writePatch(opts.UndoDir, patch.String())
		if err != nil {
			return nil, err
		}
		res.PatchPath = path
	}
	patchPath := res.PatchPath

	// Execute, remembering how to undo each step.
	type rollback func()
	var rollbacks []rollback
	fail := func(err error) (*ApplyResult, error) {
		for i := len(rollbacks) - 1; i >= 0; i-- {
			rollbacks[i]()
		}
		return nil, err
	}
	for i := range plan {
		p := &plan[i]
		switch p.op.kind {
		case opEdit, opCreate:
			mode, path, data := p.mode, p.op.path, p.after
			hadBefore, before := p.hadFile, p.before
			if err := writeFileAtomic(path, data, mode); err != nil {
				return fail(err)
			}
			rollbacks = append(rollbacks, func() {
				if hadBefore {
					_ = writeFileAtomic(path, before, mode)
				} else {
					_ = os.Remove(path)
				}
			})
			if opts.Logf != nil {
				opts.Logf("lsp: wrote %s", path)
			}
		case opRename:
			if err := os.MkdirAll(filepath.Dir(p.op.newPath), 0o755); err != nil {
				return fail(err)
			}
			if !p.op.res.Overwrite {
				if _, err := os.Stat(p.op.newPath); err == nil {
					return fail(fmt.Errorf("lsp: %s already exists", p.op.newPath))
				}
			}
			if err := os.Rename(p.op.path, p.op.newPath); err != nil {
				return fail(err)
			}
			rollbacks = append(rollbacks, func() { _ = os.Rename(p.op.newPath, p.op.path) })
		case opDelete:
			path, before, mode := p.op.path, p.before, p.mode
			if err := os.Remove(path); err != nil {
				return fail(err)
			}
			rollbacks = append(rollbacks, func() { _ = writeFileAtomic(path, before, mode) })
		}
	}
	// planToResult describes the plan; the reverse patch was decided before the
	// first write, so it has to be carried over to the returned result — that
	// path is the caller's only way to undo the edit.
	out := planToResult(plan, opts.Root)
	out.PatchPath = patchPath
	return out, nil
}

// pendingEdit is one operation with everything needed to execute it: the bytes
// before, the bytes to write, and the diff between them for the undo patch.
type pendingEdit struct {
	op       editOp
	before   []byte
	after    []byte
	mode     os.FileMode
	hadFile  bool
	relPath  string
	oldLines []line
	newLines []line
}

func planToResult(plan []pendingEdit, root string) *ApplyResult {
	res := &ApplyResult{}
	for _, p := range plan {
		switch p.op.kind {
		case opEdit:
			res.Edited = append(res.Edited, p.relPath)
		case opCreate:
			res.Created = append(res.Created, p.relPath)
		case opRename:
			res.Renamed = append(res.Renamed, p.relPath+" -> "+relTo(root, p.op.newPath))
		case opDelete:
			res.Deleted = append(res.Deleted, p.relPath)
		}
	}
	return res
}

func checkVersions(ops []editOp, known map[string]int) error {
	for _, op := range ops {
		if op.kind != opEdit || op.version == nil {
			continue
		}
		want, ok := known[op.path]
		if !ok {
			// The document is not open here, so there is nothing to compare
			// against. The reverse patch context still catches real drift.
			continue
		}
		if want != *op.version {
			return fmt.Errorf("lsp: %s: edit was computed for version %d, disk state is version %d; reopen and retry",
				op.path, *op.version, want)
		}
	}
	return nil
}

// vendoredDir is the one directory a language server is allowed to *read* and
// never to write: a rename that updates imports can be talked into proposing
// edits inside an installed dependency, and those are not the user's files —
// they are replaced by the next `npm install`, and the fix is a version bump,
// not a silent rewrite of a package.
const vendoredDir = "node_modules"

// checkNoVendored refuses a whole WorkspaceEdit when any target — the file being
// edited, created, renamed or deleted, at either end of a rename — lives under a
// node_modules directory. Whole edit, not the offending file: the operation is
// the unit that has to be consistent, and applying the half that is not vendored
// would leave a rename pointing at a path that changed.
func checkNoVendored(ops []editOp) error {
	for _, op := range ops {
		for _, p := range []string{op.path, op.newPath} {
			if p == "" || !isVendored(p) {
				continue
			}
			return fmt.Errorf("lsp: refusing to write inside %s: %s (vendored dependencies are never edited)", vendoredDir, p)
		}
	}
	return nil
}

// isVendored reports whether any component of path is a node_modules directory.
// Case-insensitive on every platform on purpose: the same package tree is
// checked out on Windows and on Linux in this workspace, and a renamed
// Node_Modules would be the same mistake.
func isVendored(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(part, vendoredDir) {
			return true
		}
	}
	return false
}

func relTo(root, path string) string {
	if root == "" {
		return filepath.ToSlash(path)
	}
	absRoot, err := absPath(root)
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(absRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// writeFileAtomic writes via a temp file in the target directory and renames it
// over the original, so a reader never sees a half-written file and a crash
// leaves either the old file or the new one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func writePatch(dir, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := time.Now().Format("20060102-150405.000") + ".patch"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// Text edit application
// ---------------------------------------------------------------------------

// ApplyTextEdits applies the edits of one document. eol is the file's existing
// line ending: servers always send "\n", and inserting LF into a CRLF file
// would leave it with mixed endings. Exported so a caller can preview or apply
// a plain list of edits (a quickfix, a format run) with the same rules the
// WorkspaceEdit applier uses.
func ApplyTextEdits(text string, edits []TextEdit, eol string) (string, error) {
	return applyTextEdits(text, edits, eol)
}

func applyTextEdits(text string, edits []TextEdit, eol string) (string, error) {
	type span struct {
		start, end int
		newText    string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		s := offsetAt(text, e.Range.Start)
		en := offsetAt(text, e.Range.End)
		if s > en {
			s, en = en, s
		}
		if en > len(text) {
			en = len(text)
		}
		spans = append(spans, span{s, en, normalizeEOL(e.NewText, eol)})
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		if sp.start < last {
			return "", errors.New("overlapping text edits")
		}
		b.WriteString(text[last:sp.start])
		b.WriteString(sp.newText)
		last = sp.end
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// offsetAt converts an LSP position into a byte offset. LSP counts characters
// in UTF-16 code units, so an emoji occupies two of them; a position landing
// inside a surrogate pair is clamped to the start of the character.
func offsetAt(text string, pos Position) int {
	if pos.Line < 0 {
		return 0
	}
	if pos.Character <= 0 && pos.Line == 0 {
		return 0
	}
	line, char := 0, 0
	for i, r := range text {
		if r == '\n' {
			if line == pos.Line {
				return i
			}
			line++
			char = 0
			continue
		}
		if line != pos.Line {
			continue
		}
		w := utf16.RuneLen(r)
		if w < 1 {
			w = 1
		}
		switch {
		case char+w > pos.Character:
			return i
		case char+w == pos.Character:
			return i + utf8.RuneLen(r)
		}
		char += w
	}
	return len(text)
}

func normalizeEOL(s, eol string) string {
	if eol != "\r\n" || !strings.Contains(s, "\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			b.WriteString("\r\n")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Undo
// ---------------------------------------------------------------------------

// UndoOptions controls an undo.
type UndoOptions struct {
	// Root resolves the relative paths inside the patch. Defaults to the
	// current working directory.
	Root string
}

// Undo reverses a patch written by ApplyWorkspaceEdit. It is refused when the
// files have moved on since the edit: undoing over newer changes would destroy
// them, and a silent partial undo is worse than an error.
//
// A WorkspaceEdit is not only text edits, so neither is its undo: a file the
// edit created is removed, a file it deleted is recreated from the patch, and a
// file it moved is moved back. Everything is computed and checked before the
// first byte changes, and a failure half way restores what was already done.
func Undo(patchPath string, opts UndoOptions) (*ApplyResult, error) {
	body, err := os.ReadFile(patchPath)
	if err != nil {
		return nil, err
	}
	secs, renames, err := parsePatch(string(body))
	if err != nil {
		return nil, err
	}
	root := opts.Root
	if root == "" {
		root, _ = os.Getwd()
	}
	res := &ApplyResult{Undone: true, PatchPath: patchPath}

	// undoStep is one filesystem action paired with the closure that takes it
	// back. Planning runs first and touches nothing, so a refusal leaves the
	// tree exactly as it was found.
	type undoStep struct {
		do   func() error
		back func()
	}
	var steps []undoStep

	resolve := func(rel string) (string, error) {
		if filepath.IsAbs(rel) {
			return filepath.Clean(rel), nil
		}
		return absPath(filepath.Join(root, filepath.FromSlash(rel)))
	}

	for _, sec := range secs {
		path, err := resolve(sec.path)
		if err != nil {
			return nil, err
		}
		switch sec.kind {
		case opCreate:
			// The edit brought this file into being; every line of it is a '+'
			// in the patch, so undoing means removing it again.
			current, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("lsp: undo %s: %w", sec.path, err)
			}
			if got, want := bodies(splitLines(string(current))), sectionAdded(sec); !sameLines(got, want) {
				return nil, fmt.Errorf("lsp: cannot undo %s: it changed since it was created", sec.path)
			}
			mode := fileModeOf(path)
			steps = append(steps, undoStep{
				do:   func() error { return os.Remove(path) },
				back: func() { _ = writeFileAtomic(path, current, mode) },
			})
			res.Deleted = append(res.Deleted, relTo(root, path))
		case opDelete:
			// The edit removed this file; the patch kept its bytes as '-' lines,
			// so undoing writes them back in the encoding recorded beside them.
			if _, err := os.Stat(path); err == nil {
				return nil, fmt.Errorf("lsp: cannot undo %s: it exists again", sec.path)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("lsp: undo %s: %w", sec.path, err)
			}
			enc := sec.enc
			if enc == "" {
				enc = "utf-8"
			}
			restored := joinLines(restoredLines(sec))
			out, ok := textenc.Encode(restored, enc)
			if !ok {
				return nil, fmt.Errorf("lsp: undo %s: text does not fit %s", sec.path, enc)
			}
			data := []byte(out)
			mode := fileModeOf(path)
			steps = append(steps, undoStep{
				do:   func() error { return writeFileAtomic(path, data, mode) },
				back: func() { _ = os.Remove(path) },
			})
			res.Created = append(res.Created, relTo(root, path))
		default:
			current, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("lsp: undo %s: %w", sec.path, err)
			}
			restored, err := applyReversePatch(string(current), sec.hunks)
			if err != nil {
				return nil, fmt.Errorf("lsp: undo %s: %w", sec.path, err)
			}
			// The post-image on disk still carries the file's own encoding, so
			// it can be detected rather than recorded.
			enc := encodingOf(string(current))
			out, ok := textenc.Encode(restored, enc)
			if !ok {
				return nil, fmt.Errorf("lsp: undo %s: text does not fit %s", sec.path, enc)
			}
			data := []byte(out)
			mode := fileModeOf(path)
			steps = append(steps, undoStep{
				do:   func() error { return writeFileAtomic(path, data, mode) },
				back: func() { _ = writeFileAtomic(path, current, mode) },
			})
			res.Edited = append(res.Edited, relTo(root, path))
		}
	}

	for _, rn := range renames {
		from, err := resolve(rn.from)
		if err != nil {
			return nil, err
		}
		to, err := resolve(rn.to)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(to); err != nil {
			return nil, fmt.Errorf("lsp: cannot undo rename: %s: %w", rn.to, err)
		}
		if _, err := os.Stat(from); err == nil {
			return nil, fmt.Errorf("lsp: cannot undo rename: %s already exists", rn.from)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("lsp: undo rename: %s: %w", rn.from, err)
		}
		steps = append(steps, undoStep{
			do:   func() error { return moveFile(to, from) },
			back: func() { _ = moveFile(from, to) },
		})
		res.Renamed = append(res.Renamed, relTo(root, to)+" -> "+relTo(root, from))
	}

	// Everything is checked; now execute, rolling back on the first failure.
	for i, st := range steps {
		if err := st.do(); err != nil {
			for j := i - 1; j >= 0; j-- {
				steps[j].back()
			}
			return nil, err
		}
	}
	return res, nil
}

// sectionAdded returns the lines a section's '+' ops put into the post-image.
func sectionAdded(sec patchSection) []string {
	var out []string
	for _, h := range sec.hunks {
		for _, op := range h.ops {
			if op.kind == '+' {
				out = append(out, op.text)
			}
		}
	}
	return out
}

// restoredLines returns the lines a section's '-' ops removed, in file order.
// They are what a deleted file is recreated from.
func restoredLines(sec patchSection) []line {
	var out []line
	for _, h := range sec.hunks {
		for _, op := range h.ops {
			if op.kind == '-' {
				out = append(out, line{text: op.text, eol: "\n"})
			}
		}
	}
	return out
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fileModeOf(path string) os.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return 0o644
}

// moveFile renames a file, creating the destination's parent if the move needs
// it (a rename back into a directory the edit removed).
func moveFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// encodingOf re-detects the encoding of bytes already on disk, so an undo
// writes back in the same encoding the file uses.
func encodingOf(data string) string {
	_, enc := textenc.Decode(data)
	return enc
}

// ---------------------------------------------------------------------------
// Client entry point
// ---------------------------------------------------------------------------

// ApplyEdit applies a WorkspaceEdit returned by this server, validating the
// versions of the documents it has open. Fields left empty in opts are filled
// from the client: Root, Versions, UndoDir (<data_dir>/undo) and Logf.
//
// When the edit lands, the documents it wrote are re-synced: the server would
// otherwise keep the bytes it had before, and the next query would answer about
// a file that no longer exists on disk.
func (c *Client) ApplyEdit(we *WorkspaceEdit, opts ApplyOptions) (*ApplyResult, error) {
	if opts.Root == "" {
		opts.Root = c.root
	}
	if opts.Versions == nil {
		opts.Versions = c.openVersions()
	}
	if opts.UndoDir == "" {
		opts.UndoDir = DefaultUndoDir()
	}
	if opts.Logf == nil {
		opts.Logf = c.cfg.Logf
	}
	res, err := ApplyWorkspaceEdit(we, opts)
	if err != nil {
		return nil, err
	}
	c.syncAfterApply(res)
	return res, nil
}

// syncAfterApply brings the client's open-document set in line with what the
// edit just wrote: a written file is re-sent, a file that is gone is closed.
// Failures are ignored — the write already happened, and a stale document only
// costs accuracy on the next query, never the edit.
func (c *Client) syncAfterApply(res *ApplyResult) {
	if res == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	join := func(rel string) string {
		if filepath.IsAbs(rel) {
			return rel
		}
		return filepath.Join(c.root, filepath.FromSlash(rel))
	}
	for _, rel := range res.Edited {
		_ = c.ensureOpen(ctx, join(rel))
	}
	for _, rel := range res.Created {
		_ = c.ensureOpen(ctx, join(rel))
	}
	for _, rel := range res.Deleted {
		_ = c.closeDoc(ctx, join(rel))
	}
	for _, rn := range res.Renamed {
		old, neu, ok := strings.Cut(rn, " -> ")
		if !ok {
			continue
		}
		_ = c.closeDoc(ctx, join(old))
		_ = c.closeDoc(ctx, join(neu))
	}
}

func (c *Client) openVersions() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.docs))
	for _, d := range c.docs {
		out[d.path] = d.version
	}
	return out
}

// DefaultUndoDir is <data_dir>/undo.
func DefaultUndoDir() string { return filepath.Join(config.Home(), "undo") }

// RenameAndApply is the whole rename in one call: prepare, compute, apply. It
// exists so a caller that has already decided to rename does not have to walk
// the three steps and risk applying an edit it did not validate.
func (c *Client) RenameAndApply(ctx context.Context, path string, pos Position, newName string, opts ApplyOptions) (*ApplyResult, error) {
	we, err := c.Rename(ctx, path, pos, newName)
	if err != nil {
		return nil, err
	}
	if we == nil {
		return nil, fmt.Errorf("lsp: server returned no edit for rename to %q", newName)
	}
	return c.ApplyEdit(we, opts)
}
