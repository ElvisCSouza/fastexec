package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRelatedTestsLinePointsAtTheTestFile(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"src/Lib/Validators/LengthValidator.cs":      "class LengthValidator {}",
		"src/Lib.Tests/LengthValidatorTests.cs":      "class LengthValidatorTests {}",
		"src/Lib.Tests/OtherTests.cs":                "class OtherTests {}",
		"src/Lib/bin/Debug/LengthValidatorTests.cs":  "ignored: bin",
		"src/Lib.Tests/obj/LengthValidatorTests2.cs": "ignored: obj",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib", "Validators", "LengthValidator.cs"), lang: "csharp"}
	line := a.relatedTestsLine()
	if !strings.Contains(line, "src/Lib.Tests/LengthValidatorTests.cs") ||
		!strings.Contains(line, `dotnet test --filter "FullyQualifiedName~LengthValidatorTests"`) {
		t.Fatalf("line wrong: %q", line)
	}
	if strings.Contains(line, "OtherTests") || strings.Contains(line, "bin") || strings.Contains(line, "obj") {
		t.Fatalf("unrelated or build-output files listed: %q", line)
	}
	if again := a.relatedTestsLine(); again != "" {
		t.Fatalf("the hint is once per file, got %q", again)
	}
}

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"src/FluentValidation.Tests/LengthValidatorTests.cs": true,
		"src/Lib/LengthValidatorTests.cs":                    true,
		"test/Serilog.Tests/X.cs":                            true,
		"pkg/x_test.go":                                      true,
		"src/Lib/Validators/LengthValidator.cs":              false,
		"src/Lib/Contest.cs":                                 false,
		"src/latest/Thing.cs":                                false,
		"src/Lib/Attestation.cs":                             false,
	} {
		if got := isTestPath(p); got != want {
			t.Errorf("isTestPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestRelatedTestsLineSilentCases(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"src/Lib/Thing.cs":                      "class Thing {}",
		"src/Lib.Tests/LengthValidatorTests.cs": "class T {}",
	})
	// arquivo sem teste de nome parecido
	a := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib", "Thing.cs"), lang: "csharp"}
	if got := a.relatedTestsLine(); got != "" {
		t.Fatalf("no related test, want silence, got %q", got)
	}
	// o próprio arquivo de teste não aponta teste
	b := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib.Tests", "LengthValidatorTests.cs"), lang: "csharp"}
	if got := b.relatedTestsLine(); got != "" {
		t.Fatalf("a test file points at nothing, got %q", got)
	}
	// arquivo novo e sem raiz: silêncio
	c := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib", "Thing.cs"), lang: "csharp", isNew: true}
	if got := c.relatedTestsLine(); got != "" {
		t.Fatalf("new file, want silence, got %q", got)
	}
	var nilEdit *autoEdit
	if got := nilEdit.relatedTestsLine(); got != "" {
		t.Fatalf("nil edit, got %q", got)
	}
}
