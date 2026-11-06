package telemetry

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func useDataDir(t *testing.T, dir string) {
	t.Helper()
	old := dataDirOverride
	dataDirOverride = dir
	failures.Store(0)
	t.Cleanup(func() { dataDirOverride = old })
}

func TestStatsSemBancoNaoCriaNada(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nao-existe")
	useDataDir(t, dir)
	out, err := RenderStats(StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No telemetry database at "+filepath.Join(dir, "fastexec.db")) || !strings.Contains(out, "fastexec doctor") {
		t.Errorf("mensagem sem banco: %s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stats criou o diretório de dados (%v)", err)
	}
}

func TestFalhaDeGravacaoFicaRegistrada(t *testing.T) {
	dir := t.TempDir()
	useDataDir(t, dir)

	db, err := sql.Open("sqlite", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	werr := writeEntry(db, AuditEntry{Tool: "exec"})
	if werr == nil {
		t.Fatal("gravar em banco fechado deveria falhar")
	}
	noteFailure("insert", werr)

	b, err := os.ReadFile(filepath.Join(dir, errorsLogName))
	if err != nil || !strings.Contains(string(b), "failures=1 insert:") {
		t.Fatalf("telemetry-errors.log: %q %v", b, err)
	}
	if r := failureReport(); !strings.Contains(r, "1 line(s)") || !strings.Contains(r, "insert:") {
		t.Errorf("failureReport: %s", r)
	}

	// só a 1a e cada 100a falha vão para o arquivo
	for i := 0; i < 98; i++ {
		noteFailure("queue", errQueueFull)
	}
	if r := failureReport(); !strings.Contains(r, "1 line(s)") {
		t.Errorf("falhas 2..99 não deveriam gravar linha: %s", r)
	}
	noteFailure("queue", errQueueFull)
	if r := failureReport(); !strings.Contains(r, "2 line(s)") || !strings.Contains(r, "failures=100 queue") {
		t.Errorf("100a falha deveria gravar: %s", r)
	}
	noteFailure("nada", nil)
	if failures.Load() != 100 {
		t.Errorf("erro nil não conta: %d", failures.Load())
	}
}
