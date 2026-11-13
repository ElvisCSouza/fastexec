package tools

import (
	"os"
	"strings"
	"testing"
)

// Sem git: o rename exato roda (o projeto Go é provado pelo go.mod) e o desfazer em memória devolve os dois arquivos.
func TestRenameAndUndoWithoutGit(t *testing.T) {
	f := newLSPFixture(t, renameCtl("ParseRecord"))
	oldA := "package main\n\nfunc ParseLine() {}\n"
	newA := "package main\n\nfunc ParseRecord() {}\n"
	f.write("a.go", oldA)
	f.write("b.go", "package main\n\nfunc main() { ParseLine() }\n")
	if err := os.RemoveAll(f.path(".git")); err != nil {
		t.Fatal(err)
	}
	f.start()

	msg := f.edit("a.go", newA)
	if !strings.Contains(msg, "DONE ParseLine -> ParseRecord") {
		t.Fatalf("rename must work without git, got:\n%s", msg)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseRecord()") {
		t.Fatalf("b.go not renamed:\n%s", got)
	}
	out, err := HandleExec(ExecArgs{Command: UndoCommand})
	if err != nil || !strings.Contains(out, "restored 2 file(s)") {
		t.Fatalf("undo: %v: %s", err, out)
	}
	if got := f.read("a.go"); got != oldA {
		t.Fatalf("a.go not restored: %q", got)
	}
	if got := f.read("b.go"); !strings.Contains(got, "ParseLine()") {
		t.Fatalf("b.go not restored:\n%s", got)
	}
}

// Só a última operação fica; o undo consome e uma segunda chamada não tem o que desfazer.
func TestUndoKeepsOnlyTheLastOperation(t *testing.T) {
	dir := t.TempDir()
	a, b := dir+"/a.txt", dir+"/b.txt"
	if err := os.WriteFile(a, []byte("a0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: a, Content: "a1", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: b, Content: "b1", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if out, _ := HandleExec(ExecArgs{Command: UndoCommand}); !strings.Contains(out, "restored 1 file(s)") {
		t.Fatalf("undo of the last operation: %s", out)
	}
	if got, _ := os.ReadFile(b); string(got) != "b0" {
		t.Fatalf("b.txt = %q, want b0", got)
	}
	if got, _ := os.ReadFile(a); string(got) != "a1" {
		t.Fatalf("a.txt (older operation) must stay a1, got %q", got)
	}
	if out, _ := HandleExec(ExecArgs{Command: UndoCommand}); !strings.Contains(out, "nothing to undo") {
		t.Fatalf("second undo must have nothing to do: %s", out)
	}
}

// Arquivo que mudou depois da gravação não é sobrescrito pelo undo.
func TestUndoSkipsFileChangedSince(t *testing.T) {
	dir := t.TempDir()
	a := dir + "/a.txt"
	if err := os.WriteFile(a, []byte("a0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: a, Content: "a1", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := HandleExec(ExecArgs{Command: UndoCommand})
	if !strings.Contains(out, "Not restored") {
		t.Fatalf("diverged file must be reported, got: %s", out)
	}
	if got, _ := os.ReadFile(a); string(got) != "someone else" {
		t.Fatalf("diverged file must stay untouched, got %q", got)
	}
}

// Arquivo novo: o undo o apaga.
func TestUndoRemovesCreatedFile(t *testing.T) {
	p := t.TempDir() + "/novo.txt"
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: p, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := HandleExec(ExecArgs{Command: UndoCommand}); !strings.Contains(out, "restored 1 file(s)") {
		t.Fatalf("undo: %s", out)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("created file must be removed, stat err = %v", err)
	}
}
