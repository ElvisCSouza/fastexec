package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fim a fim, com um `go test` de verdade: módulo minúsculo num diretório temporário cujo teste imprime oito linhas
// de progresso ('ok  fake/pkgN') antes de falhar. É o formato exato que consome contexto no ciclo de trabalho.
const buildCompactModule = `module fake/pkg

go 1.21
`

const buildCompactTestFile = `package fake

import (
	"fmt"
	"testing"
)

func TestFails(t *testing.T) {
	for i := 0; i < 8; i++ {
		fmt.Printf("ok  \tfake/pkg%d\t0.100s\n", i)
	}
	t.Fatalf("boom: got 1, want 2")
}
`

func buildCompactFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go ausente no PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(buildCompactModule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg_test.go"), []byte(buildCompactTestFile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExecCompactsGoTestFailureAndAnnouncesIt(t *testing.T) {
	dir := buildCompactFixture(t)
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
	out, _ := HandleExec(ExecArgs{Host: "local", Command: "go test ./...", Cwd: dir})

	if !strings.Contains(out, "output compacted") {
		t.Fatalf("compactou sem se anunciar:\n%s", out)
	}
	if !strings.Contains(out, "use --raw") {
		t.Errorf("o aviso tem de dizer como pedir a íntegra:\n%s", out)
	}
	// O que decide a próxima ação não pode faltar.
	for _, want := range []string{"boom: got 1, want 2", "FAIL"} {
		if !strings.Contains(out, want) {
			t.Errorf("perdeu %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fake/pkg0") {
		t.Errorf("linha de progresso que passou continuou na saída:\n%s", out)
	}
}

func TestExecRawReturnsFullGoTestOutput(t *testing.T) {
	dir := buildCompactFixture(t)
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
	out, _ := HandleExec(ExecArgs{Host: "local", Command: "go test ./... --raw", Cwd: dir})

	if strings.Contains(out, "output compacted") {
		t.Errorf("com --raw nada é compactado:\n%s", out)
	}
	if !strings.Contains(out, "fake/pkg0") || !strings.Contains(out, "boom: got 1, want 2") {
		t.Errorf("--raw tem de devolver a saída inteira:\n%s", out)
	}
	if !strings.Contains(out, "raw output as asked") {
		t.Errorf("o pedido de saída íntegra também se anuncia:\n%s", out)
	}
}

func TestExecNoOutputFilterEnvReturnsFullGoTestOutput(t *testing.T) {
	dir := buildCompactFixture(t)
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "1")
	out, _ := HandleExec(ExecArgs{Host: "local", Command: "go test ./...", Cwd: dir})

	if strings.Contains(out, "output compacted") {
		t.Errorf("FASTEXEC_NO_OUTPUT_FILTER devolve a saída íntegra:\n%s", out)
	}
	if !strings.Contains(out, "fake/pkg0") {
		t.Errorf("saída íntegra tem as linhas de progresso:\n%s", out)
	}
}
