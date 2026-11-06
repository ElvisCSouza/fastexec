package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalFileOpsLifecycle(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "fastexec_test_ops")
	_ = os.RemoveAll(tempDir)
	defer os.RemoveAll(tempDir)

	// 1. mkdir
	srcDir := filepath.Join(tempDir, "src")
	_, err := HandleFileOps(FileOpsArgs{
		Action: "mkdir",
		Path:   srcDir,
	})
	if err != nil {
		t.Fatalf("mkdir falhou: %v", err)
	}

	// 2. create sample file
	srcFile := filepath.Join(srcDir, "hello.txt")
	if err := os.WriteFile(srcFile, []byte("conteudo teste"), 0644); err != nil {
		t.Fatalf("write teste falhou: %v", err)
	}

	// 3. copy
	dstFile := filepath.Join(tempDir, "hello_copied.txt")
	_, err = HandleFileOps(FileOpsArgs{
		Action: "copy",
		Path:   srcFile,
		Target: dstFile,
	})
	if err != nil {
		t.Fatalf("copy falhou: %v", err)
	}
	if b, _ := os.ReadFile(dstFile); string(b) != "conteudo teste" {
		t.Fatalf("conteudo copiado divergente: %s", string(b))
	}

	// 4. move
	movedFile := filepath.Join(tempDir, "hello_moved.txt")
	_, err = HandleFileOps(FileOpsArgs{
		Action: "move",
		Path:   dstFile,
		Target: movedFile,
	})
	if err != nil {
		t.Fatalf("move falhou: %v", err)
	}
	if _, err := os.Stat(dstFile); !os.IsNotExist(err) {
		t.Fatalf("arquivo original ainda existe apos move")
	}

	// 5. list
	out, err := HandleFileOps(FileOpsArgs{
		Action: "list",
		Path:   tempDir,
	})
	if err != nil {
		t.Fatalf("list falhou: %v", err)
	}
	if !strings.Contains(out, "hello_moved.txt") {
		t.Fatalf("listagem nao encontrou arquivo movido: %s", out)
	}

	// 6. delete
	_, err = HandleFileOps(FileOpsArgs{
		Action: "delete",
		Path:   tempDir,
	})
	if err != nil {
		t.Fatalf("delete falhou: %v", err)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("diretorio ainda existe apos delete")
	}
}

func TestReadFileLines(t *testing.T) {
	tempFile := filepath.Join(os.TempDir(), "fastexec_test_lines.txt")
	defer os.Remove(tempFile)
	content := "linha 1\nlinha 2\nlinha 3\nlinha 4\nlinha 5"
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		t.Fatalf("write falhou: %v", err)
	}

	out, err := HandleReadFile(ReadFileArgs{
		Path:      tempFile,
		StartLine: 2,
		EndLine:   4,
	})
	if err != nil {
		t.Fatalf("HandleReadFile falhou: %v", err)
	}
	expected := "linha 2\nlinha 3\nlinha 4"
	if out != expected {
		t.Fatalf("esperava %q, recebeu %q", expected, out)
	}
}

func TestHandleEditFile(t *testing.T) {
	tempFile := filepath.Join(os.TempDir(), "fastexec_test_edit.txt")
	defer os.Remove(tempFile)
	content := "header\n<div class=\"search\">remover</div>\nfooter"
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		t.Fatalf("write falhou: %v", err)
	}

	res, err := HandleEditFile(EditFileArgs{
		Path:        tempFile,
		Target:      "<div class=\"search\">remover</div>\n",
		Replacement: "",
	})
	if err != nil {
		t.Fatalf("HandleEditFile falhou: %v", err)
	}
	if !strings.Contains(res, "[edit_file] 1 change(s) applied") {
		t.Errorf("resposta inesperada: %s", res)
	}

	b, _ := os.ReadFile(tempFile)
	if string(b) != "header\nfooter" {
		t.Fatalf("conteúdo pós-edição inesperado: %q", string(b))
	}

	// Target não encontrado
	_, err = HandleEditFile(EditFileArgs{
		Path:        tempFile,
		Target:      "nao_existe",
		Replacement: "novo",
	})
	if err == nil {
		t.Fatalf("esperava erro quando target não existe")
	}
}
