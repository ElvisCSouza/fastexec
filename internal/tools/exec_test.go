package tools

import (
	"strings"
	"testing"
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
	// Comandos com sleep ou timeout devem ser bloqueados
	proibidos := []string{
		"sleep 140",
		"bash -c 'sleep 100; echo oi'",
		"sleep 5 && ls",
		"timeout 10",
		"timeout /t 5 /nobreak",
	}

	for _, cmd := range proibidos {
		_, err := HandleExec(ExecArgs{
			Host:    "local",
			Command: cmd,
		})
		if err == nil {
			t.Errorf("esperava erro de guarda para comando '%s', mas passou", cmd)
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
