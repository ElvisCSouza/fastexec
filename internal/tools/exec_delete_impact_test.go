package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// exec_delete_impact_test.go cobre a PARTE B da task 20261112-edit-impacto: o exec que apaga arquivo de código
// avisa quais símbolos daquele arquivo continuam sendo usados em outros. O grafo consultado é o de ANTES do
// apagamento — é o que o código faz de verdade, porque o índice só é atualizado depois, pelo Touch debounced.
//
// Como aqui só o TEXTO do comando importa (o aviso é montado depois do exec ter rodado, a partir do comando),
// os testes chamam execDeleteImpact direto e não executam nada.
//
//	del.go:3  func Fetch(n int) error    keep.go:3  func Keep() { Fetch(1) }
//	del.go:5  func Parse(s string) error keep.go:5  func Outro() { Parse("x") }

// deleteRepo monta o repositório do caso: del.go declara, keep.go usa.
func deleteRepo(t *testing.T) string {
	t.Helper()
	root := impactRepo(t, map[string]string{
		"del.go": "package p\n\nfunc Fetch(n int) error {\n\treturn nil\n}\n\nfunc Parse(s string) error {\n\treturn nil\n}\n",
		"keep.go": "package p\n\nfunc Keep() { Fetch(1) }\n\nfunc Outro() { Parse(\"x\") }\n" +
			"\nfunc Extra() { Fetch(2); Fetch(3); Parse(\"y\") }\n",
	})
	return root
}

func TestExecDeleteImpactWarns(t *testing.T) {
	root := deleteRepo(t)
	got := execDeleteImpact(ExecArgs{Host: "local", Cwd: root, Command: "rm del.go"})
	if !strings.Contains(got, "del.go deleted:") {
		t.Fatalf("esperava o aviso de deleção:\n%s", got)
	}
	if !strings.Contains(got, "Fetch") || !strings.Contains(got, "Parse") {
		t.Errorf("os dois símbolos declarados em del.go deveriam aparecer:\n%s", got)
	}
	if !strings.Contains(got, "keep.go:3") {
		t.Errorf("esperava citar keep.go:3:\n%s", got)
	}
	if len(got) > deleteImpactBudget {
		t.Errorf("aviso com %d chars, teto %d:\n%s", len(got), deleteImpactBudget, got)
	}
}

// O aviso só existe quando há uso: símbolo declarado e ninguém chamando não vira linha.
func TestExecDeleteImpactSilentWhenNoUses(t *testing.T) {
	root := impactRepo(t, map[string]string{"sozinho.go": "package p\n\nfunc NinguemUsa() {}\n"})
	if got := execDeleteImpact(ExecArgs{Host: "local", Cwd: root, Command: "rm sozinho.go"}); got != "" {
		t.Errorf("símbolo sem uso não deveria avisar, veio %q", got)
	}
}

// Comando que não apaga código: nenhum byte a mais.
func TestExecDeleteImpactSilentOnOtherCommands(t *testing.T) {
	root := deleteRepo(t)
	for _, cmd := range []string{
		"go build ./...",
		"grep -rn Fetch .",
		"cat del.go",
		"rm build.log",
		"rm -rf ./tmp",
		"del /f notas.txt",
	} {
		if got := execDeleteImpact(ExecArgs{Host: "local", Cwd: root, Command: cmd}); got != "" {
			t.Errorf("%q não deveria avisar, veio %q", cmd, got)
		}
	}
}

// Sem índice não há resposta — e nada é criado.
func TestExecDeleteImpactNoIndexSilent(t *testing.T) {
	dbp := filepath.Join(t.TempDir(), "graph.db")
	graph.SetDBPath(dbp)
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "del.go"), []byte("package p\n\nfunc Fetch(n int) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := execDeleteImpact(ExecArgs{Host: "local", Cwd: root, Command: "rm del.go"}); got != "" {
		t.Errorf("sem índice não deveria avisar, veio %q", got)
	}
	if _, err := os.Stat(dbp); err == nil {
		t.Errorf("o aviso criou %s", dbp)
	}
}

// Host remoto: o índice daqui não fala do repositório do outro lado.
func TestExecDeleteImpactRemoteSilent(t *testing.T) {
	root := deleteRepo(t)
	if got := execDeleteImpact(ExecArgs{Host: "server", Cwd: root, Command: "rm del.go"}); got != "" {
		t.Errorf("host remoto não deveria avisar, veio %q", got)
	}
}

// Nome declarado em dois arquivos: o grafo casa por nome, então o aviso diz que é ambíguo em vez de afirmar.
func TestExecDeleteImpactAmbiguousMarked(t *testing.T) {
	root := impactRepo(t, map[string]string{
		"del.go":   "package p\n\nfunc Fetch(n int) error {\n\treturn nil\n}\n",
		"outro.go": "package p\n\nfunc Fetch(n int) error {\n\treturn nil\n}\n",
		"keep.go":  "package p\n\nfunc Keep() { Fetch(1) }\n",
	})
	got := execDeleteImpact(ExecArgs{Host: "local", Cwd: root, Command: "rm del.go"})
	if !strings.Contains(got, "Fetch (ambiguous)") {
		t.Errorf("esperava marcar o homônimo:\n%s", got)
	}
}

// Formas de comando reconhecidas: argv, del do cmd, Remove-Item do PowerShell e git rm.
func TestExecDeleteImpactCommandForms(t *testing.T) {
	root := deleteRepo(t)
	cases := []struct {
		name string
		args ExecArgs
	}{
		{"argv", ExecArgs{Host: "local", Cwd: root, Argv: []string{"rm", "del.go"}}},
		{"cmd del", ExecArgs{Host: "local", Cwd: root, Command: "del /f del.go"}},
		{"powershell", ExecArgs{Host: "local", Cwd: root, Command: "Remove-Item -Force del.go"}},
		{"git rm", ExecArgs{Host: "local", Cwd: root, Command: "git rm del.go"}},
		{"composto", ExecArgs{Host: "local", Cwd: root, Command: "cd src && rm del.go"}},
	}
	for _, c := range cases {
		got := execDeleteImpact(c.args)
		if !strings.Contains(got, "del.go deleted:") {
			t.Errorf("%s: esperava o aviso:\n%s", c.name, got)
		}
	}
}
