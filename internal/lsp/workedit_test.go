package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileURI is the URI a server would send for a path.
func fileURI(path string) string { return PathToURI(path) }

// editAt builds a single-line replacement edit.
func editAt(line, start, end int, newText string) TextEdit {
	return TextEdit{
		Range:   Range{Start: Position{Line: line, Character: start}, End: Position{Line: line, Character: end}},
		NewText: newText,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestApplyEditPreservesCRLF(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.go", "package demo\r\n\r\nvar Name = 1\r\n")

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(2, 4, 8, "Title")},
	}}
	res, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")})
	if err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	if len(res.Edited) != 1 {
		t.Fatalf("Edited = %v, want one file", res.Edited)
	}
	got := readFile(t, path)
	if want := "package demo\r\n\r\nvar Title = 1\r\n"; got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestApplyEditInsertsTheFileLineEnding(t *testing.T) {
	root := t.TempDir()
	// A multi-line edit sent with LF, as every server does, into a CRLF file.
	path := writeFixture(t, root, "a.go", "package demo\r\nvar A = 1\r\n")

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(1, 0, 9, "var B = 2\nvar C = 3")},
	}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	got := readFile(t, path)
	if want := "package demo\r\nvar B = 2\r\nvar C = 3\r\n"; got != want {
		t.Fatalf("file = %q, want pure CRLF %q", got, want)
	}
}

func TestApplyEditPreservesBOMAndEncoding(t *testing.T) {
	root := t.TempDir()
	// A UTF-8 BOM plus a Latin-1 byte: the edit must not strip the BOM nor
	// mangle the byte that is not valid UTF-8.
	body := []byte{0xEF, 0xBB, 0xBF}
	body = append(body, []byte("package demo\n\nvar Name = \"caf\xe9\"\n")...)
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(2, 4, 8, "Title")},
	}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || got[0] != 0xEF || got[1] != 0xBB || got[2] != 0xBF {
		t.Fatalf("BOM was dropped: % x", got[:min(3, len(got))])
	}
	if !strings.Contains(string(got), "var Title = \"caf\xe9\"") {
		t.Fatalf("file = %q, want the byte preserved and the edit applied", got)
	}
}

func TestApplyEditIsRefusedWhenTheVersionIsStale(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.go", "package demo\n\nvar Name = 1\n")
	before := readFile(t, path)

	v := 7
	we := &WorkspaceEdit{DocumentChanges: []DocumentChange{{
		TextDocument: &VersionedTextDocumentIdentifier{URI: fileURI(path), Version: &v},
		Edits:        []TextEdit{editAt(2, 4, 8, "Title")},
	}}}
	// The server computed the edit against version 7; FastExec last sent 9.
	_, err := ApplyWorkspaceEdit(we, ApplyOptions{
		Root:     root,
		Versions: map[string]int{path: 9},
		UndoDir:  filepath.Join(t.TempDir(), "undo"),
	})
	if err == nil {
		t.Fatal("a stale edit was applied")
	}
	if !strings.Contains(err.Error(), "version 7") {
		t.Errorf("error = %v, want it to name the stale version", err)
	}
	if got := readFile(t, path); got != before {
		t.Fatal("the file changed even though the edit was refused")
	}
}

func TestApplyEditIsAtomic(t *testing.T) {
	root := t.TempDir()
	good := writeFixture(t, root, "good.go", "package demo\n\nvar Name = 1\n")
	before := readFile(t, good)

	// The second document cannot be read: nothing at all may be written.
	missing := filepath.Join(root, "missing.go")
	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(good):    {editAt(2, 4, 8, "Title")},
		fileURI(missing): {editAt(0, 0, 0, "x")},
	}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err == nil {
		t.Fatal("an edit touching an unreadable file succeeded")
	}
	if got := readFile(t, good); got != before {
		t.Fatalf("the readable file was written anyway: %q", got)
	}
}

func TestApplyEditRefusesToOverwriteOnCreate(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.go", "package demo\n")

	we := &WorkspaceEdit{DocumentChanges: []DocumentChange{{
		Kind: "create", URI: fileURI(path),
	}}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err == nil {
		t.Fatal("create overwrote an existing file without permission")
	}
	if got := readFile(t, path); got != "package demo\n" {
		t.Fatalf("file = %q, want it untouched", got)
	}

	// With overwrite allowed it goes through.
	we.DocumentChanges[0].Options = []byte(`{"overwrite":true}`)
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err != nil {
		t.Fatalf("create with overwrite: %v", err)
	}
	if got := readFile(t, path); got != "" {
		t.Fatalf("file = %q, want it emptied by the create", got)
	}
}

// TestApplyEditNeverWritesIntoVendoredDependencies: a server that is asked to
// update imports across a project can name a file inside node_modules, and those
// edits must be refused as a whole — the dependency is not the user's file and
// the next install would overwrite it anyway.
func TestApplyEditNeverWritesIntoVendoredDependencies(t *testing.T) {
	root := t.TempDir()
	dep := writeFixture(t, root, filepath.Join("node_modules", "left-pad", "index.js"), "module.exports = 1;\n")
	mine := writeFixture(t, root, "app.js", "const x = 1;\n")
	before := readFile(t, dep)

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(mine): {editAt(0, 6, 7, "y")},
		fileURI(dep):  {editAt(0, 20, 21, "2")},
	}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err == nil {
		t.Fatal("an edit inside node_modules was applied")
	}
	if got := readFile(t, dep); got != before {
		t.Fatalf("the dependency was written: %q", got)
	}
	// The whole edit is refused, not just the vendored half: a half-applied
	// rename would leave an import pointing at a path that moved.
	if got := readFile(t, mine); got != "const x = 1;\n" {
		t.Fatalf("the non-vendored file was written anyway: %q", got)
	}

	// Both ends of a rename, and a create/delete, go through the same gate. The
	// options are permissive on purpose, so the refusal can only be the gate.
	permissive := []byte(`{"overwrite":true,"ignoreIfNotExists":true,"recursive":true}`)
	resourceOps := []struct {
		name string
		doc  DocumentChange
	}{
		{"rename out of node_modules", DocumentChange{Kind: "rename", OldURI: fileURI(dep), NewURI: fileURI(filepath.Join(root, "left-pad.js"))}},
		{"rename into node_modules", DocumentChange{Kind: "rename", OldURI: fileURI(mine), NewURI: fileURI(filepath.Join(root, "node_modules", "app.js"))}},
		{"create inside node_modules", DocumentChange{Kind: "create", URI: fileURI(filepath.Join(root, "node_modules", "evil.js"))}},
		{"delete inside node_modules", DocumentChange{Kind: "delete", URI: fileURI(dep)}},
	}
	for _, tc := range resourceOps {
		tc.doc.Options = permissive
		we := &WorkspaceEdit{DocumentChanges: []DocumentChange{tc.doc}}
		if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err == nil {
			t.Errorf("%s: applied, want a refusal", tc.name)
		}
	}
	if got := readFile(t, dep); got != before {
		t.Errorf("dependency = %q, want it still untouched", got)
	}

	// Case is not an escape hatch: the same tree is checked out on Windows and on
	// Linux in this workspace.
	odd := writeFixture(t, root, filepath.Join("Node_Modules", "x", "index.js"), "var a = 1;\n")
	we = &WorkspaceEdit{Changes: map[string][]TextEdit{fileURI(odd): {editAt(0, 4, 5, "b")}}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root}); err == nil {
		t.Error("an edit inside Node_Modules was applied")
	}

	// A directory whose name merely contains the word is an ordinary directory.
	plain := writeFixture(t, root, filepath.Join("node_modules_backup", "index.js"), "var a = 1;\n")
	we = &WorkspaceEdit{Changes: map[string][]TextEdit{fileURI(plain): {editAt(0, 4, 5, "b")}}}
	if _, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")}); err != nil {
		t.Errorf("node_modules_backup was refused: %v", err)
	}
}

func TestApplyEditsCreatesRenamesAndDeletes(t *testing.T) {
	root := t.TempDir()
	old := writeFixture(t, root, "old.go", "package demo\n\nvar Name = 1\n")
	doomed := writeFixture(t, root, "doomed.go", "package demo\n")
	created := filepath.Join(root, "sub", "new.go")

	we := &WorkspaceEdit{DocumentChanges: []DocumentChange{
		{Kind: "create", URI: fileURI(created)},
		{Kind: "rename", OldURI: fileURI(old), NewURI: fileURI(filepath.Join(root, "renamed.go"))},
		{Kind: "delete", URI: fileURI(doomed)},
	}}
	res, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")})
	if err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	if len(res.Created) != 1 || len(res.Renamed) != 1 || len(res.Deleted) != 1 {
		t.Fatalf("result = %+v, want one create, one rename, one delete", res)
	}
	if _, err := os.Stat(created); err != nil {
		t.Errorf("created file missing: %v", err)
	}
	if _, err := os.Stat(doomed); !os.IsNotExist(err) {
		t.Error("deleted file still exists")
	}
	if got := readFile(t, filepath.Join(root, "renamed.go")); got != "package demo\n\nvar Name = 1\n" {
		t.Errorf("renamed file = %q, want the bytes carried over", got)
	}
}

func TestApplyEditWritesAReversePatchThatUndoes(t *testing.T) {
	root := t.TempDir()
	undoDir := filepath.Join(t.TempDir(), "undo")
	path := writeFixture(t, root, "a.go", "package demo\r\n\r\nvar Name = 1\r\nvar Keep = 2\r\n")
	before := readFile(t, path)

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(2, 4, 8, "Title")},
	}}
	res, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: undoDir})
	if err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	if res.PatchPath == "" {
		t.Fatal("no reverse patch was written")
	}
	if !strings.HasPrefix(res.PatchPath, undoDir) {
		t.Errorf("patch = %q, want it under %q", res.PatchPath, undoDir)
	}
	edited := readFile(t, path)
	if edited == before {
		t.Fatal("the edit did not change the file")
	}

	undo, err := Undo(res.PatchPath, UndoOptions{Root: root})
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !undo.Undone {
		t.Error("the result does not report an undo")
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("after undo the file = %q, want the original %q", got, before)
	}
}

func TestUndoIsRefusedWhenTheFileMovedOn(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.go", "package demo\n\nvar Name = 1\n")

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(2, 4, 8, "Title")},
	}}
	res, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, UndoDir: filepath.Join(t.TempDir(), "undo")})
	if err != nil {
		t.Fatalf("ApplyWorkspaceEdit: %v", err)
	}
	// Someone else edited the same lines in the meantime; undoing over that
	// would destroy their work.
	if err := os.WriteFile(path, []byte("package demo\n\nvar Other = 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(res.PatchPath, UndoOptions{Root: root}); err == nil {
		t.Fatal("undo applied over newer changes")
	}
	if got := readFile(t, path); got != "package demo\n\nvar Other = 9\n" {
		t.Fatalf("file = %q, want the newer content intact", got)
	}
}

func TestApplyEditRefusesNonFileURIs(t *testing.T) {
	_, err := ApplyWorkspaceEdit(&WorkspaceEdit{Changes: map[string][]TextEdit{
		"untitled:Untitled-1": {editAt(0, 0, 0, "x")},
	}}, ApplyOptions{Root: t.TempDir()})
	if err == nil {
		t.Fatal("a non-file URI was accepted")
	}
	if !strings.Contains(err.Error(), "non-file URI") {
		t.Errorf("error = %v", err)
	}
}

func TestApplyEditDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "a.go", "package demo\n\nvar Name = 1\n")
	before := readFile(t, path)

	we := &WorkspaceEdit{Changes: map[string][]TextEdit{
		fileURI(path): {editAt(2, 4, 8, "Title")},
	}}
	res, err := ApplyWorkspaceEdit(we, ApplyOptions{Root: root, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(res.Edited) != 1 || res.PatchPath != "" {
		t.Fatalf("dry run result = %+v, want the plan and no patch", res)
	}
	if got := readFile(t, path); got != before {
		t.Fatal("a dry run wrote to disk")
	}
}
