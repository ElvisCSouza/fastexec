package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cada classe de recusa do edit_file: a mensagem traz a classe e nada é gravado.
func TestEditFileErrorClasses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	src := "alpha\nbeta\nalpha\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// file_not_found: arquivo inexistente
	_, err := HandleEditFile(EditFileArgs{Host: "local", Path: filepath.Join(dir, "nao-existe.txt"), Target: "x", Replacement: "y"})
	if err == nil || !strings.Contains(err.Error(), "[edit_file file_not_found]") || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("file_not_found: got %v", err)
	}

	// target_not_found: texto ausente, com a classe e o nothing was written
	_, err = HandleEditFile(EditFileArgs{Host: "local", Path: p, Target: "gamma", Replacement: "delta"})
	if err == nil || !strings.Contains(err.Error(), "[edit_file target_not_found]") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("target_not_found: got %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("target_not_found must not write: %q", got)
	}

	// ambiguous_target: "alpha" aparece duas vezes; recusa, cita as linhas, não troca o primeiro
	_, err = HandleEditFile(EditFileArgs{Host: "local", Path: p, Target: "alpha", Replacement: "omega"})
	if err == nil || !strings.Contains(err.Error(), "[edit_file ambiguous_target]") || !strings.Contains(err.Error(), "lines 1, 3") {
		t.Fatalf("ambiguous_target: got %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("ambiguous_target must not write: %q", got)
	}
}

// Em lote, uma troca ambígua recusa o lote inteiro (tudo ou nada), mesmo que as outras sejam válidas.
func TestEditFileAmbiguityRefusesWholeBatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.txt")
	src := "one\ntwo\none\nthree\n"
	_ = os.WriteFile(p, []byte(src), 0o644)

	_, err := HandleEditFile(EditFileArgs{Host: "local", Path: p, Edits: []EditOp{
		{Target: "three", Replacement: "3"},
		{Target: "one", Replacement: "1"},
	}})
	if err == nil || !strings.Contains(err.Error(), "edits[1].target") || !strings.Contains(err.Error(), "[edit_file ambiguous_target]") {
		t.Fatalf("batch ambiguity: got %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("batch with ambiguity must not write anything: %q", got)
	}
}

// expected_sha256: hash correto aplica; hash antigo (arquivo mudou) recusa sem gravar e sem vazar o conteúdo.
func TestEditFileStalePrecondition(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.txt")
	src := "keep this line\nsecret-token-xyz\n"
	_ = os.WriteFile(p, []byte(src), 0o644)
	sum := sha256.Sum256([]byte(src))
	good := hex.EncodeToString(sum[:])

	stale := strings.Repeat("0", 64)
	_, err := HandleEditFile(EditFileArgs{Host: "local", Path: p, Target: "keep this line", Replacement: "changed", ExpectedSHA256: stale})
	if err == nil || !strings.Contains(err.Error(), "[edit_file stale_precondition]") || !strings.Contains(err.Error(), "current sha256 "+good) {
		t.Fatalf("stale: got %v", err)
	}
	if strings.Contains(err.Error(), "secret-token-xyz") {
		t.Fatalf("stale refusal must not leak file content: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("stale must not write: %q", got)
	}

	msg, err := HandleEditFile(EditFileArgs{Host: "local", Path: p, Target: "keep this line", Replacement: "changed", ExpectedSHA256: good})
	if err != nil {
		t.Fatalf("current hash must apply, got %v (%q)", err, msg)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "changed") {
		t.Fatalf("current hash edit not applied: %q", got)
	}
}
