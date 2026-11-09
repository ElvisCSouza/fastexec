package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCompileOrTestFailureGo(t *testing.T) {
	dir := t.TempDir()
	src := "package myapp\n\nfunc ExistingFunc() error {\n\treturn nil\n}\n"
	_ = os.WriteFile(filepath.Join(dir, "worker.go"), []byte(src), 0o644)

	errMsg := "./main.go:15:2: undefined: ExistingFunc\nexit status 1"
	hint := checkCompileOrTestFailure(dir, "go test ./...", errMsg)
	if hint == "" {
		t.Fatalf("esperava hint para undefined: ExistingFunc")
	}
	if !strings.Contains(hint, "[FastExec Context Helper]") || !strings.Contains(hint, "ExistingFunc") {
		t.Errorf("hint incorreto: %s", hint)
	}
	if !strings.Contains(hint, "worker.go") {
		t.Errorf("hint deveria apontar para worker.go: %s", hint)
	}
}

func TestCheckCompileOrTestFailureTypeScript(t *testing.T) {
	dir := t.TempDir()
	src := "export class AuthService {\n  login() {}\n}\n"
	_ = os.WriteFile(filepath.Join(dir, "auth.ts"), []byte(src), 0o644)

	errMsg := "src/index.ts(10,5): error TS2304: Cannot find name 'AuthService'."
	hint := checkCompileOrTestFailure(dir, "npx tsc", errMsg)
	if hint == "" {
		t.Fatalf("esperava hint para TS Cannot find name 'AuthService'")
	}
	if !strings.Contains(hint, "auth.ts") || !strings.Contains(hint, "read_file(path, symbol='AuthService')") {
		t.Errorf("hint TS incorreto: %s", hint)
	}
}

func TestCheckCompileOrTestFailurePython(t *testing.T) {
	dir := t.TempDir()
	src := "def calculate_discount(price):\n    return price * 0.9\n"
	_ = os.WriteFile(filepath.Join(dir, "billing.py"), []byte(src), 0o644)

	errMsg := "Traceback (most recent call last):\n  File \"app.py\", line 4, in <module>\nNameError: name 'calculate_discount' is not defined"
	hint := checkCompileOrTestFailure(dir, "python3 app.py", errMsg)
	if hint == "" {
		t.Fatalf("esperava hint para Python NameError")
	}
	if !strings.Contains(hint, "billing.py") || !strings.Contains(hint, "calculate_discount") {
		t.Errorf("hint Python incorreto: %s", hint)
	}
}

func TestCheckCompileOrTestFailureNoMatch(t *testing.T) {
	dir := t.TempDir()
	errMsg := "fatal: some general network error"
	hint := checkCompileOrTestFailure(dir, "curl example.com", errMsg)
	if hint != "" {
		t.Errorf("não deveria gerar hint para erro genérico: %s", hint)
	}
}
