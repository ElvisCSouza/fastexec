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
	if !strings.Contains(out1, "change(s) applied") {
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
	if !strings.Contains(out2, "change(s) applied") {
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
	if !strings.Contains(out3, "change(s) applied") {
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

	// 2. Segunda gravação sem overwrite: arquivo já existe com conteúdo -> recusada, aponta edit_file, nada muda
	_, err = HandleWriteFile(WriteFileArgs{
		Host:    "local",
		Path:    filePath,
		Content: largeData + "Nova linha no final.\n",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "edit_file") || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("segunda gravação deveria ser recusada apontando edit_file e overwrite: %v", err)
	}
	if b, _ := os.ReadFile(filePath); string(b) != largeData {
		t.Errorf("gravação recusada alterou o arquivo")
	}

	// 3. Com overwrite: regrava inteiro
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: filePath, Content: "novo\n", Overwrite: true}); err != nil {
		t.Fatalf("overwrite falhou: %v", err)
	}
	if b, _ := os.ReadFile(filePath); string(b) != "novo\n" {
		t.Errorf("overwrite não regravou: %q", b)
	}

	// 4. Arquivo existente mas vazio: gravação livre
	empty := filepath.Join(tmpDir, "vazio.txt")
	_ = os.WriteFile(empty, nil, 0o644)
	if _, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: empty, Content: "x"}); err != nil {
		t.Errorf("arquivo vazio deveria aceitar gravação: %v", err)
	}
}

func TestEditFileVariasTrocasAtomicas(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.go")
	orig := "package a\n\nfunc A() int { return 1 }\n\nfunc B() int { return 2 }\n"
	_ = os.WriteFile(p, []byte(orig), 0o644)

	out, err := HandleEditFile(EditFileArgs{Path: p, Edits: []EditOp{
		{Target: "return 1", Replacement: "return 10"},
		{Target: "return 2", Replacement: "return 20"},
	}})
	if err != nil || !strings.Contains(out, "2 change(s) applied") {
		t.Fatalf("duas trocas: %v %s", err, out)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "return 10") || !strings.Contains(string(b), "return 20") {
		t.Errorf("trocas não aplicadas: %s", b)
	}

	// uma troca ausente: nada é gravado
	before, _ := os.ReadFile(p)
	_, err = HandleEditFile(EditFileArgs{Path: p, Edits: []EditOp{
		{Target: "return 10", Replacement: "return 100"},
		{Target: "nao existe", Replacement: "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), "edits[1].target not found") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("troca ausente deveria abortar tudo: %v", err)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Errorf("edição parcial gravada")
	}

	// edits via JSON com sinônimos (old_string/new_string)
	var args EditFileArgs
	payload := fmt.Sprintf(`{"path": %q, "edits": [{"old_string": "return 10", "new_string": "return 11"}]}`, p)
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleEditFile(args); err != nil {
		t.Fatalf("edits com sinônimos: %v", err)
	}
}

func TestEditFileCasaFimDeLinha(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crlf.go")
	_ = os.WriteFile(p, []byte("package a\r\n\r\nfunc A() {\r\n\treturn\r\n}\r\n"), 0o644)
	// target com LF num arquivo CRLF
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "func A() {\n\treturn\n}", Replacement: "func A() {\n\tprintln()\n}"}); err != nil {
		t.Fatalf("LF em arquivo CRLF: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "package a\r\n\r\nfunc A() {\r\n\tprintln()\r\n}\r\n" {
		t.Errorf("CRLF não preservado: %q", b)
	}

	q := filepath.Join(t.TempDir(), "lf.go")
	_ = os.WriteFile(q, []byte("a\nb\nc\n"), 0o644)
	// target com CRLF num arquivo LF
	if _, err := HandleEditFile(EditFileArgs{Path: q, Target: "a\r\nb", Replacement: "x\r\ny"}); err != nil {
		t.Fatalf("CRLF em arquivo LF: %v", err)
	}
	if b, _ := os.ReadFile(q); string(b) != "x\ny\nc\n" {
		t.Errorf("LF não preservado: %q", b)
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
