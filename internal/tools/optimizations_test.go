package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditFileSynonyms(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("Hello world! Alpha Bravo Charlie."), 0644); err != nil {
		t.Fatalf("falha ao criar arquivo de teste: %v", err)
	}

	// 1. Teste com old_string / new_string (padrão Claude)
	payload1 := fmt.Sprintf(`{"path": %q, "old_string": "world", "new_string": "FastExec"}`, filePath)
	var args1 EditFileArgs
	if err := json.Unmarshal([]byte(payload1), &args1); err != nil {
		t.Fatalf("falha ao deserializar com old_string: %v", err)
	}
	out1, err := HandleEditFile(args1)
	if err != nil {
		t.Fatalf("HandleEditFile com old_string falhou: %v", err)
	}
	if !strings.Contains(out1, "Substituído com sucesso") {
		t.Errorf("saída inesperada: %s", out1)
	}

	// 2. Teste com search / replace (padrão editor)
	payload2 := fmt.Sprintf(`{"path": %q, "search": "Alpha", "replace": "Delta"}`, filePath)
	var args2 EditFileArgs
	if err := json.Unmarshal([]byte(payload2), &args2); err != nil {
		t.Fatalf("falha ao deserializar com search/replace: %v", err)
	}
	out2, err := HandleEditFile(args2)
	if err != nil {
		t.Fatalf("HandleEditFile com search/replace falhou: %v", err)
	}
	if !strings.Contains(out2, "Substituído com sucesso") {
		t.Errorf("saída inesperada: %s", out2)
	}

	// 3. Teste com find / content
	payload3 := fmt.Sprintf(`{"path": %q, "find": "Charlie", "content": "Omega"}`, filePath)
	var args3 EditFileArgs
	if err := json.Unmarshal([]byte(payload3), &args3); err != nil {
		t.Fatalf("falha ao deserializar com find/content: %v", err)
	}
	out3, err := HandleEditFile(args3)
	if err != nil {
		t.Fatalf("HandleEditFile com find/content falhou: %v", err)
	}
	if !strings.Contains(out3, "Substituído com sucesso") {
		t.Errorf("saída inesperada: %s", out3)
	}

	// Verifica conteúdo final
	content, _ := os.ReadFile(filePath)
	expected := "Hello FastExec! Delta Bravo Omega."
	if string(content) != expected {
		t.Errorf("conteúdo final incorreto. Esperava %q, obteve %q", expected, string(content))
	}
}

func TestSleepAutoRemediationPython(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(d time.Duration) {}
	defer func() { sleepFn = origSleep }()

	// Python curto (<=15s) deve sofrer auto-remediação via timer Go nativo
	pyShort := []string{
		`python -c "import time; time.sleep(3)"`,
		`python3 -c 'import time; time.sleep(5)'`,
		`py -c "import time; time.sleep(10)"`,
	}
	for _, cmd := range pyShort {
		out, err := HandleExec(ExecArgs{
			Host:    "local",
			Command: cmd,
		})
		if err != nil {
			t.Errorf("esperava sucesso com auto-remediação para '%s', recebeu erro: %v", cmd, err)
		}
		if !strings.Contains(out, "[FastExec Auto-Remediation]") {
			t.Errorf("saída de auto-remediação não contém cabeçalho para '%s': %s", cmd, out)
		}
	}

	// Python longo (>15s) deve ser bloqueado com instrução de job assíncrono
	pyLong := []string{
		`python -c "import time; time.sleep(20)"`,
		`python3 -c 'import time; time.sleep(30)'`,
	}
	for _, cmd := range pyLong {
		_, err := HandleExec(ExecArgs{
			Host:    "local",
			Command: cmd,
		})
		if err == nil {
			t.Errorf("esperava erro de guarda para '%s', mas passou", cmd)
		} else if !strings.Contains(err.Error(), "[FastExec Guard]") {
			t.Errorf("mensagem de erro esperada não contém '[FastExec Guard]': %v", err)
		}
	}
}

func TestWriteFileNudgeExisting(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "large_file.txt")

	// Conteúdo com mais de 1.5 KB (> 1500 bytes)
	largeData := strings.Repeat("Linha de teste para preencher arquivo com volume suficiente.\n", 40) // ~2.4 KB

	// 1. Primeira gravação: arquivo não existia antes -> sem nudge
	out1, err := HandleWriteFile(WriteFileArgs{
		Host:    "local",
		Path:    filePath,
		Content: largeData,
	})
	if err != nil {
		t.Fatalf("primeira gravação falhou: %v", err)
	}
	if strings.Contains(out1, "[FastExec Hint]") {
		t.Errorf("primeira gravação não deveria ter Hint de arquivo existente: %s", out1)
	}

	// 2. Segunda gravação: arquivo já existe e tem > 1.5 KB -> deve anexar Hint orientando edit_file
	out2, err := HandleWriteFile(WriteFileArgs{
		Host:    "local",
		Path:    filePath,
		Content: largeData + "Nova linha no final.\n",
	})
	if err != nil {
		t.Fatalf("segunda gravação falhou: %v", err)
	}
	if !strings.Contains(out2, "[FastExec Hint] O arquivo sobrescrito já existia") {
		t.Errorf("segunda gravação esperava Hint de arquivo existente, recebeu: %s", out2)
	}
	if !strings.Contains(out2, "edit_file") {
		t.Errorf("segunda gravação esperava sugestão da tool edit_file: %s", out2)
	}
}

func TestExecDurationWarning(t *testing.T) {
	oldWarning := execDurationWarningMs
	execDurationWarningMs = 5 // 5ms para disparar com qualquer comando real
	defer func() { execDurationWarningMs = oldWarning }()

	out, err := HandleExec(ExecArgs{
		Host:    "local",
		Command: "cmd /c echo ok",
	})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Warning]") {
		t.Errorf("esperava FastExec Warning de duração longa, recebeu: %s", out)
	}
	if !strings.Contains(out, "tool 'job'") {
		t.Errorf("aviso deveria recomendar a tool 'job': %s", out)
	}
}
