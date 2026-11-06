package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

func TestSplitHostPath(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantPath string
	}{
		{"server:/tmp/foo", "server", "/tmp/foo"},
		{"local:C:\\test", "local", "C:\\test"},
		{"C:\\Users\\user", "local", "C:\\Users\\user"},
		{"c:/users/user", "local", "c:/users/user"},
		{"D:\\repos", "local", "D:\\repos"},
		{"\\\\server\\share\\file.txt", "local", "\\\\server\\share\\file.txt"},
		{"/var/log/syslog", "", "/var/log/syslog"},
		{"relative/path.txt", "", "relative/path.txt"},
		{"srv:/home/user/workspace", "srv", "/home/user/workspace"},
	}

	for _, tc := range tests {
		gotHost, gotPath := SplitHostPath(tc.input)
		if gotHost != tc.wantHost || gotPath != tc.wantPath {
			t.Errorf("SplitHostPath(%q) = (%q, %q); want (%q, %q)", tc.input, gotHost, gotPath, tc.wantHost, tc.wantPath)
		}
	}
}

func TestSearchAndDiffBenignExits(t *testing.T) {
	// 1. Grep exit 1 sem saída -> "sem resultado (exit 1)", nil
	out, err := formatExecResult("grep foo file.txt", transport.ExecResult{ExitCode: 1})
	if err != nil || out != NoResultText {
		t.Errorf("grep exit 1 sem saída falhou: got (%q, %v), want (%q, nil)", out, err, NoResultText)
	}

	// 2. Select-String exit 255 sem saída -> "sem resultado (exit 1)", nil
	out, err = formatExecResult("Select-String -Pattern foo bar.txt", transport.ExecResult{ExitCode: 255})
	if err != nil || out != NoResultText {
		t.Errorf("select-string exit 255 sem saída falhou: got (%q, %v), want (%q, nil)", out, err, NoResultText)
	}

	// 3. Diff exit 1 com stdout de diferenças -> benigno (err == nil)
	diffOutput := "3c3\n< linha_antiga\n---\n> linha_nova\n"
	out, err = formatExecResult("diff file1.txt file2.txt", transport.ExecResult{ExitCode: 1, Stdout: diffOutput})
	if err != nil {
		t.Errorf("diff exit 1 com diferenças não deveria retornar erro: %v", err)
	}
	if !strings.Contains(out, "linha_antiga") || !strings.Contains(out, "[exit 1]") {
		t.Errorf("diff exit 1 output incorreto: %q", out)
	}

	// 4. Diff exit 2 (erro de arquivo inexistente) -> deve falhar
	_, err = formatExecResult("diff file1.txt file2.txt", transport.ExecResult{ExitCode: 2, Stderr: "diff: file1.txt: No such file"})
	if err == nil {
		t.Error("diff exit 2 deveria retornar erro")
	}

	// 5. Comando genérico com exit 255 -> deve falhar
	_, err = formatExecResult("my_app run", transport.ExecResult{ExitCode: 255, Stderr: "crash"})
	if err == nil {
		t.Error("comando genérico com exit 255 deveria retornar erro")
	}
}

func TestListAutoRemediation(t *testing.T) {
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "arquivo1.txt")
	subDir := filepath.Join(tmpDir, "subpasta")
	if err := os.WriteFile(file1, []byte("conteudo"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. dir simples
	out, handled, err := tryRemediateList(ExecArgs{Host: "local", Command: "dir " + tmpDir})
	if !handled || err != nil {
		t.Fatalf("tryRemediateList(dir) falhou: handled=%v, err=%v", handled, err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "arquivo1.txt") || !strings.Contains(out, "subpasta/") {
		t.Errorf("conteúdo inesperado na remediação de dir: %q", out)
	}

	// 2. dir com flag /b
	out, handled, err = tryRemediateList(ExecArgs{Host: "local", Command: "dir /b " + tmpDir})
	if !handled || err != nil {
		t.Fatalf("tryRemediateList(dir /b) falhou: handled=%v, err=%v", handled, err)
	}
	if !strings.Contains(out, "arquivo1.txt") {
		t.Errorf("dir /b não listou arquivo1.txt: %q", out)
	}

	// 3. ls com flag -la
	out, handled, err = tryRemediateList(ExecArgs{Host: "local", Command: "ls -la " + tmpDir})
	if !handled || err != nil {
		t.Fatalf("tryRemediateList(ls -la) falhou: handled=%v, err=%v", handled, err)
	}
	if !strings.Contains(out, "arquivo1.txt") {
		t.Errorf("ls -la não listou arquivo1.txt: %q", out)
	}

	// 4. Wildcards NÃO devem ser remediados (passam para o shell expandir)
	_, handled, _ = tryRemediateList(ExecArgs{Host: "local", Command: "dir " + filepath.Join(tmpDir, "*.txt")})
	if handled {
		t.Error("dir com wildcard (*.txt) não deveria ter sido auto-remediado")
	}

	// 5. Pipes NÃO devem ser remediados
	_, handled, _ = tryRemediateList(ExecArgs{Host: "local", Command: "dir " + tmpDir + " | findstr arquivo"})
	if handled {
		t.Error("dir com pipe não deveria ter sido auto-remediado")
	}

	// 6. Listagem de um arquivo único (não diretório) deve delegar para o shell nativo
	_, handled, _ = tryRemediateList(ExecArgs{Host: "local", Command: "ls " + file1})
	if handled {
		t.Error("ls em arquivo não deveria ter sido remediado (deve delegar para o shell)")
	}
}
