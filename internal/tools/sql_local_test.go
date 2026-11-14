package tools

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSqlLocalSelectDevolveMesmoJSONQueHostLinux: SELECT em local no Windows devolve o JSON no formato `sqlite3 -json`
// (antes qualquer SELECT vinha `[]`, porque o pipeline echo|base64|sqlite3 morria no shell local).
func TestSqlLocalSelectDevolveMesmoJSONQueHostLinux(t *testing.T) {
	path := criarDBTeste(t)

	out, err := HandleSql(SqlArgs{Host: "local", Database: path, Query: "SELECT * FROM itens ORDER BY id"})
	if err != nil {
		t.Fatalf("SELECT falhou: %v", err)
	}
	esperado := "[{\"id\":1,\"nome\":\"lapis\",\"preco\":1.5},\n {\"id\":2,\"nome\":null,\"preco\":2.25}]"
	if out != esperado {
		t.Errorf("JSON diferente do esperado:\n veio:  %q\n quero: %q", out, esperado)
	}

	// o caso que motivou o conserto: agregação simples não pode virar []
	if out, err = HandleSql(SqlArgs{Host: "local", Database: path, Query: "SELECT count(*) AS n FROM itens"}); err != nil || out != `[{"n":2}]` {
		t.Errorf("count(*): veio %q / %v, quero [{\"n\":2}]", out, err)
	}

	// SELECT sem linhas continua devolvendo [] (contrato do caminho remoto)
	if out, err = HandleSql(SqlArgs{Host: "local", Database: path, Query: "SELECT * FROM itens WHERE id = 99"}); err != nil || out != "[]" {
		t.Errorf("sem linhas: veio %q / %v, quero []", out, err)
	}
}

// TestSqlLocalErrosClaros: faltar o que é preciso para rodar vira erro dizendo o que falta, nunca [].
func TestSqlLocalErrosClaros(t *testing.T) {
	if _, err := HandleSql(SqlArgs{Host: "local", Database: filepath.Join(t.TempDir(), "nao-existe.db"), Query: "SELECT 1"}); err == nil || !strings.Contains(err.Error(), "database file not found") {
		t.Errorf("arquivo ausente deveria dar erro claro: %v", err)
	}
	path := criarDBTeste(t)
	if _, err := HandleSql(SqlArgs{Host: "local", Database: path, Query: "SELECT * FROM tabela_que_nao_existe"}); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("SQL inválido deveria dar erro claro: %v", err)
	}
}

// TestSqlWindowsPathSemHostRodaLocal: database com caminho de unidade Windows e sem host roda em local, com a nota
// [FastExec Auto-Remediation] no mesmo padrão do exec. Só faz sentido no Windows de verdade (o remédio é para o
// padrão remoto com caminho de drive; a detecção do caminho já está coberta em TestLocalHostForWindowsPath...).
func TestSqlWindowsPathSemHostRodaLocal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("host local Windows")
	}
	path := criarDBTeste(t)

	out, err := HandleSql(SqlArgs{Database: path, Query: "SELECT count(*) AS n FROM itens"})
	if err != nil {
		t.Fatalf("SELECT sem host falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "host 'local'") {
		t.Errorf("saída sem a nota de remediação: %q", out)
	}
	if !strings.Contains(out, `[{"n":2}]`) {
		t.Errorf("nota presente mas resultado errado: %q", out)
	}
}

// TestSqlErroComNotaNaoEscondeOMotivo: com erro e sem saída, a nota não pode substituir a mensagem de erro.
func TestSqlErroComNotaNaoEscondeOMotivo(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("host local Windows")
	}
	out, err := HandleSql(SqlArgs{Database: filepath.Join(t.TempDir(), "ausente.db"), Query: "SELECT 1"})
	if err == nil || !strings.Contains(err.Error(), "database file not found") {
		t.Fatalf("arquivo ausente deveria falhar: %v", err)
	}
	if out != "" {
		t.Errorf("com erro e sem saída a nota não deve substituir a mensagem de erro: %q", out)
	}
}

// TestSqlSemQueryEhErro: parâmetro obrigatório ausente vira erro, não [].
func TestSqlSemQueryEhErro(t *testing.T) {
	if _, err := HandleSql(SqlArgs{Host: "local"}); err == nil || !strings.Contains(err.Error(), "'query' is required") {
		t.Errorf("sem query deveria falhar: %v", err)
	}
}

// criarDBTeste cria um SQLite pequeno no TempDir do teste: duas linhas, com NULL e REAL para exercitar a codificação.
func criarDBTeste(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "itens.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("abrir teste.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE itens (id INTEGER, nome TEXT, preco REAL)`); err != nil {
		t.Fatalf("criar tabela: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO itens VALUES (1,'lapis',1.5),(2,NULL,2.25)`); err != nil {
		t.Fatalf("inserir linhas: %v", err)
	}
	return path
}
