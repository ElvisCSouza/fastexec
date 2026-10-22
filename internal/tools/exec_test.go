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
