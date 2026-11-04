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

func TestSleepGuardHeredocExclusion(t *testing.T) {
	// Comandos com heredocs que mencionam sleep ou timeout em seu texto não devem ser bloqueados
	cmdWithHeredoc := "cat >> doc.md <<'EOF'\n- `email lixeira` espera timeout 30s da resposta.\n- loop com sleep 10.\nEOF"
	stripped := stripHeredocs(cmdWithHeredoc)
	if strings.Contains(stripped, "timeout 30s") || strings.Contains(stripped, "sleep 10") {
		t.Errorf("stripHeredocs não removeu o corpo do heredoc: %s", stripped)
	}

	if checkSleepGuard(stripped, ExecArgs{Command: cmdWithHeredoc}) {
		t.Errorf("comando com heredoc contendo texto sobre timeout não deveria ser bloqueado pelo guard")
	}
}

func TestCoreutilsTimeoutAllowed(t *testing.T) {
	// 1. Timeout com subcomando (coreutils) NÃO deve ser bloqueado
	permitidos := []string{
		"timeout 40 docker exec assistente-openclaw node index.js",
		"timeout 10s curl http://127.0.0.1:8080/health",
		"timeout --preserve-status 15 python script.py",
		"timeout -s KILL 30 ./bin/worker",
	}
	for _, cmd := range permitidos {
		if checkSleepGuard(cmd, ExecArgs{Command: cmd}) {
			t.Errorf("utilitário GNU coreutils '%s' foi bloqueado indevidamente pelo sleep guard", cmd)
		}
	}

	// 2. Timeout puro (pausa artificial) DEVE continuar bloqueado
	bloqueados := []string{
		"timeout 20",
		"timeout /t 20",
		"timeout 40 docker exec node && sleep 5",
	}
	for _, cmd := range bloqueados {
		if !checkSleepGuard(cmd, ExecArgs{Command: cmd}) {
			t.Errorf("pausa artificial '%s' deveria ser bloqueada pelo sleep guard", cmd)
		}
	}
}

func TestResolveTargetHostInference(t *testing.T) {
	// Caminho Linux sem host deve inferir noteserver
	targetLinux, err := ResolveTargetHost("", "/home/elvis/workspace/project")
	if err != nil {
		t.Fatalf("erro ao resolver host linux: %v", err)
	}
	if targetLinux.Name != "noteserver" || targetLinux.IsLocal {
		t.Errorf("esperava noteserver para path linux, obteve %s (isLocal=%v)", targetLinux.Name, targetLinux.IsLocal)
	}

	// Caminho Windows com letra de unidade deve inferir local
	targetWin, err := ResolveTargetHost("", `W:\ReposHomeLab\fastexec`)
	if err != nil {
		t.Fatalf("erro ao resolver host windows: %v", err)
	}
	if !targetWin.IsLocal {
		t.Errorf("esperava local para path windows W:, obteve %s (isLocal=%v)", targetWin.Name, targetWin.IsLocal)
	}
}

func TestAliasesAndLocalFileOperations(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "origem.txt")
	destFile := filepath.Join(tmpDir, "destino.txt")
	_ = os.WriteFile(srcFile, []byte("conteudo teste"), 0644)

	// 1. GetFile com aliases (src e dest) e cópia local
	jsonGet := fmt.Sprintf(`{"host":"local", "src":%q, "dest":%q}`, srcFile, destFile)
	var getArgs GetFileArgs
	if err := json.Unmarshal([]byte(jsonGet), &getArgs); err != nil {
		t.Fatalf("falha ao deserializar get_file com aliases: %v", err)
	}
	if getArgs.RemotePath != srcFile || getArgs.LocalPath != destFile {
		t.Errorf("aliases de get_file não foram mapeados corretamente: %+v", getArgs)
	}
	outGet, err := HandleGetFile(getArgs)
	if err != nil {
		t.Fatalf("HandleGetFile com host local falhou: %v", err)
	}
	if !strings.Contains(outGet, "ok") {
		t.Errorf("esperava ok na cópia local de get_file: %s", outGet)
	}
	destContent, _ := os.ReadFile(destFile)
	if string(destContent) != "conteudo teste" {
		t.Errorf("conteúdo copiado incorreto: %s", string(destContent))
	}

	// 2. FileOps com alias operation e normalização de action
	jsonOps := fmt.Sprintf(`{"operation":"LIST", "source":%q}`, tmpDir)
	var opsArgs FileOpsArgs
	if err := json.Unmarshal([]byte(jsonOps), &opsArgs); err != nil {
		t.Fatalf("falha ao deserializar file_ops com aliases: %v", err)
	}
	if opsArgs.Action != "list" || opsArgs.Path != tmpDir {
		t.Errorf("aliases de file_ops não foram mapeados: %+v", opsArgs)
	}
	outOps, err := HandleFileOps(opsArgs)
	if err != nil {
		t.Fatalf("HandleFileOps com alias falhou: %v", err)
	}
	if !strings.Contains(outOps, "origem.txt") {
		t.Errorf("saída de listagem não contém origem.txt: %s", outOps)
	}
}
