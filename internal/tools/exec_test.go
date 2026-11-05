package tools

import (
	"strings"
	"testing"
	"time"
)

func TestSqlDuckDBSession(t *testing.T) {
	out, err := HandleSql(SqlArgs{
		Session: "test_suite",
		Query:   "SELECT 123 as numero, 'ok' as status;",
	})
	if err != nil {
		t.Fatalf("HandleSql DuckDB falhou: %v", err)
	}
	if !strings.Contains(out, `"numero":123`) {
		t.Errorf("saída inesperada: %s", out)
	}
}

func TestExecWithInputLocal(t *testing.T) {
	out, err := HandleExec(ExecArgs{
		Host:    "local",
		Command: "findstr teste",
		Input:   "linha 1\nteste de entrada\nlinha 3\n",
	})
	if err != nil {
		t.Fatalf("HandleExec local com input falhou: %v", err)
	}
	if !strings.Contains(out, "teste de entrada") {
		t.Errorf("saída inesperada: %s", out)
	}
}

func TestExecWithInputNoteServer(t *testing.T) {
	out, err := HandleExec(ExecArgs{
		Host:    "noteserver",
		Command: "grep banana",
		Input:   "maca\nbanana nanica\nabacaxi\n",
	})
	if err != nil {
		t.Fatalf("HandleExec noteserver com input falhou: %v", err)
	}
	if !strings.Contains(out, "banana nanica") {
		t.Errorf("saída inesperada: %s", out)
	}
}

func TestSleepGuardRejection(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(d time.Duration) {}
	defer func() { sleepFn = origSleep }()

	// Comandos com sleep longo (>15s) ou encadeados no shell continuam bloqueados
	proibidos := []string{
		"sleep 140",
		"bash -c 'sleep 100; echo oi'",
		"sleep 5 && ls",
		"timeout 20",
		"timeout /t 25",
	}

	for _, cmd := range proibidos {
		_, err := HandleExec(ExecArgs{
			Host:    "local",
			Command: cmd,
		})
		if err == nil {
			t.Errorf("esperava erro de guarda para comando '%s', mas passou", cmd)
			continue
		}
		if !strings.Contains(err.Error(), "[FastExec Guard]") {
			t.Errorf("mensagem de erro esperada não contém '[FastExec Guard]': %v", err)
		}
	}

	// Comandos válidos contendo a palavra 'sleep' sem número NÃO devem ser bloqueados
	permitidos := []string{
		"echo sleep",
		"grep 'sleeping' arquivo.txt",
	}

	for _, cmd := range permitidos {
		if sleepGuardRegex.MatchString(cmd) {
			t.Errorf("comando legítimo '%s' disparou a guarda indevidamente", cmd)
		}
	}
}

func TestSleepAutoRemediation(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(d time.Duration) {}
	defer func() { sleepFn = origSleep }()

	curtos := []string{
		"sleep 3",
		"timeout 5",
		"timeout /t 2",
		"Start-Sleep 2",
	}
	for _, cmd := range curtos {
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
}

func TestCatAutoRemediation(t *testing.T) {
	out, err := HandleExec(ExecArgs{
		Host:    "local",
		Command: "cat exec.go",
	})
	if err != nil {
		t.Fatalf("HandleExec com cat exec.go falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") {
		t.Errorf("esperava cabeçalho de auto-remediação, recebeu: %s", out)
	}
	if !strings.Contains(out, "package tools") {
		t.Errorf("esperava conteúdo de exec.go na saída, recebeu: %s", out)
	}
}

func TestCatGuardRejection(t *testing.T) {
	proibidos := []struct {
		cmd  string
		argv []string
		path string
	}{
		{cmd: "cat main.go", path: "main.go"},
		{cmd: "type config.json", path: "config.json"},
		{cmd: "Get-Content src/file.txt", path: "src/file.txt"},
		{cmd: "gc file.go", path: "file.go"},
		{cmd: "cat /etc/os-release", path: "/etc/os-release"},
		{argv: []string{"cat", "foo.txt"}, path: "foo.txt"},
		{argv: []string{"type", "C:\\test\\file.txt"}, path: "C:\\test\\file.txt"},
	}

	for _, tc := range proibidos {
		var err error
		if len(tc.argv) > 0 {
			err = checkCatGuard(ExecArgs{Host: "local", Argv: tc.argv})
		} else {
			err = checkCatGuard(ExecArgs{Host: "local", Command: tc.cmd})
		}
		if err == nil {
			t.Errorf("esperava bloqueio de guarda para '%s' / %v, mas passou", tc.cmd, tc.argv)
			continue
		}
		if !strings.Contains(err.Error(), "[FastExec Guard]") || !strings.Contains(err.Error(), "read_file") {
			t.Errorf("mensagem de erro esperada não orienta para 'read_file': %v", err)
		}
		if tc.path != "" && !strings.Contains(err.Error(), tc.path) {
			t.Errorf("mensagem de erro não incluiu o caminho alvo '%s': %v", tc.path, err)
		}
	}

	permitidos := []struct {
		cmd  string
		argv []string
	}{
		{cmd: "cat file.txt | grep teste"},
		{cmd: "cat file.txt > destino.txt"},
		{cmd: "cat << 'EOF'\nconteudo\nEOF"},
		{cmd: "cat file1.txt && cat file2.txt"},
		{cmd: "type file.txt | findstr ok"},
		{cmd: "echo 'cat file.txt'"},
		{argv: []string{"grep", "cat", "file.txt"}},
	}

	for _, tc := range permitidos {
		var err error
		if len(tc.argv) > 0 {
			err = checkCatGuard(ExecArgs{Host: "local", Argv: tc.argv})
		} else {
			err = checkCatGuard(ExecArgs{Host: "local", Command: tc.cmd})
		}
		if err != nil {
			t.Errorf("comando legítimo '%s' disparou a guarda de cat indevidamente: %v", tc.cmd, err)
		}
	}
}

func TestDetectNudge(t *testing.T) {
	cases := []struct {
		label    string
		args     ExecArgs
		contains string
	}{
		{label: "ls -la", args: ExecArgs{Command: "ls -la"}, contains: "file_ops"},
		{label: "dir /s", args: ExecArgs{Command: "dir /s"}, contains: "file_ops"},
		{label: "docker ps -a", args: ExecArgs{Command: "docker ps -a"}, contains: "docker"},
		{label: "sqlite3 my.db", args: ExecArgs{Command: "sqlite3 my.db"}, contains: "sql"},
		{label: "ls | grep foo", args: ExecArgs{Command: "ls | grep foo"}, contains: ""},
	}

	for _, tc := range cases {
		hint := detectNudge(tc.label, tc.args)
		if tc.contains == "" {
			if hint != "" {
				t.Errorf("esperava nenhum nudge para '%s', recebeu: %s", tc.label, hint)
			}
		} else {
			if !strings.Contains(hint, tc.contains) {
				t.Errorf("esperava nudge com '%s' para '%s', recebeu: %s", tc.contains, tc.label, hint)
			}
		}
	}
}

func TestCdAutoRemediation(t *testing.T) {
	// Caso 1: puro cd ~/workspace
	args1 := ExecArgs{Host: "noteserver", Command: "cd ~/workspace"}
	out, handled, _ := tryRemediateCd(&args1)
	if !handled || !strings.Contains(out, "já é ~/workspace") {
		t.Errorf("esperava tratamento de cd ~/workspace puro, recebeu handled=%v out=%s", handled, out)
	}

	// Caso 2: cd ~/workspace && <resto>
	args2 := ExecArgs{Host: "noteserver", Command: "cd ~/workspace && git status"}
	_, handled, hint := tryRemediateCd(&args2)
	if handled {
		t.Errorf("esperava handled=false para executar resto do comando")
	}
	if args2.Command != "git status" {
		t.Errorf("esperava command 'git status', recebeu: %s", args2.Command)
	}
	if !strings.Contains(hint, "[FastExec Auto-Remediation]") {
		t.Errorf("esperava hint pedagógico, recebeu: %s", hint)
	}

	// Caso 3: cd /home/elvis/workspace; <resto>
	args3 := ExecArgs{Host: "noteserver", Command: "cd /home/elvis/workspace; set -a"}
	_, handled, hint = tryRemediateCd(&args3)
	if args3.Command != "set -a" {
		t.Errorf("esperava command 'set -a', recebeu: %s", args3.Command)
	}

	// Caso 4: cd ~/workspace/homeassistant/config; echo "ok"
	args4 := ExecArgs{Host: "noteserver", Command: `cd ~/workspace/homeassistant/config; echo "ok"`}
	_, handled, hint = tryRemediateCd(&args4)
	if args4.Cwd != "homeassistant/config" {
		t.Errorf("esperava Cwd 'homeassistant/config', recebeu: %s", args4.Cwd)
	}
	if args4.Command != `echo "ok"` {
		t.Errorf("esperava command 'echo \"ok\"', recebeu: %s", args4.Command)
	}
}
