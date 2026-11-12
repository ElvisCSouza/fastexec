package tools

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLocalHostForWindowsPathSemHostPadraoRemoto: o caso do benchmark (exec sem host, caminho W:\ e padrão remoto).
func TestLocalHostForWindowsPathSemHostPadraoRemoto(t *testing.T) {
	casos := []struct {
		nome   string
		partes []string
	}{
		{"command", []string{`type W:\ReposHomeLab\AgenteHA\README.md`}},
		{"command com barra normal", []string{`cat W:/ReposHomeLab/x.txt`}},
		{"cwd", []string{"", `W:\ReposHomeLab`}},
		{"argv", []string{"", "", "cmd", "/c", `Get-Content D:\repos\a.go`}},
		{"entre aspas", []string{`bash -c "grep -n foo W:\repos\a.go"`}},
		{"depois de =", []string{`tool --dir=C:\tmp\out`}},
		{"depois de redirect", []string{`echo x > W:\tmp\a.txt`}},
	}
	for _, c := range casos {
		host, note := localHostForWindowsPath("", c.partes...)
		if host != "local" {
			t.Errorf("%s: esperava host local, veio %q", c.nome, host)
		}
		if !strings.Contains(note, "[FastExec Auto-Remediation]") || !strings.Contains(note, "'local'") ||
			!strings.Contains(note, "'server'") {
			t.Errorf("%s: nota sem o aviso esperado: %q", c.nome, note)
		}
	}
}

// TestLocalHostForWindowsPathNaoDispara: host explícito vence, URL e texto solto não mudam nada.
func TestLocalHostForWindowsPathNaoDispara(t *testing.T) {
	naoDispara := []struct {
		nome string
		host string
		part string
	}{
		{"host explícito remoto", "router", `W:\ReposHomeLab\x.txt`},
		{"host explícito igual ao padrão", "server", `W:\ReposHomeLab\x.txt`},
		{"host explícito local", "local", `W:\ReposHomeLab\x.txt`},
		{"URL http", "", `curl http://192.0.2.10:9142/health`},
		{"URL file", "", `curl file:///tmp/x`},
		{"sem caminho", "", `go build ./...`},
		{"letra sem separador", "", `echo a:b/c`},
		{"texto solto", "", `echo "a unidade W: nao existe aqui"`},
	}
	for _, c := range naoDispara {
		host, note := localHostForWindowsPath(c.host, c.part)
		if host != c.host || note != "" {
			t.Errorf("%s: esperava manter %q sem nota, veio %q / %q", c.nome, c.host, host, note)
		}
	}
}

func TestWindowsPathInExtraiOCaminho(t *testing.T) {
	if p := windowsPathIn(`type W:\ReposHomeLab\x.txt && echo ok`); p != `W:\ReposHomeLab\x.txt` {
		t.Errorf("caminho extraído errado: %q", p)
	}
	if p := windowsPathIn(`echo a`, `c:/tmp/a.sh`); p != `c:/tmp/a.sh` {
		t.Errorf("caminho extraído errado no argv: %q", p)
	}
	if p := windowsPathIn(`curl http://x:9142/y`); p != "" {
		t.Errorf("URL não deveria extrair caminho: %q", p)
	}
}

// TestExecWindowsPathSemHostRodaLocal: a nota chega à LLM na saída do exec (o comando em si para no guard de sleep,
// sem processo real).
func TestExecWindowsPathSemHostRodaLocal(t *testing.T) {
	origSleep := sleepFn
	sleepFn = func(time.Duration) {}
	defer func() { sleepFn = origSleep }()

	out, err := HandleExec(ExecArgs{Command: "sleep 3", Cwd: `W:\ReposHomeLab`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "host 'local'") {
		t.Errorf("saída sem a nota de remediação: %q", out)
	}
	// sem caminho de unidade nada muda
	if out, err = HandleExec(ExecArgs{Command: "sleep 3"}); err != nil || strings.Contains(out, "Auto-Remediation] 'host'") {
		t.Errorf("sem caminho Windows não deveria remediar: %q %v", out, err)
	}
}

// TestJobWindowsPathSemHostRodaLocal: a nota também sai no job, e o job é atendido pelo caminho local. Só no host
// Windows de verdade, que é onde o 'local' tem a implementação própria (Job Object).
func TestJobWindowsPathSemHostRodaLocal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("host local Windows")
	}
	out, err := HandleJob(JobArgs{Action: "list", Command: `type W:\ReposHomeLab\x.txt`})
	if err != nil {
		t.Fatalf("list falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "No local job") {
		t.Errorf("saída sem a nota de remediação: %q", out)
	}
}

// TestJobComErroNaoTrocaAMensagem: com erro e sem saída, a nota não pode esconder a mensagem de erro (o MCP só
// mostraria a saída).
func TestJobComErroNaoTrocaAMensagem(t *testing.T) {
	out, err := HandleJob(JobArgs{Action: "bogus", Command: `type W:\ReposHomeLab\x.txt`})
	if err == nil || !strings.Contains(err.Error(), "unknown job action") {
		t.Fatalf("ação inválida deveria falhar: %v", err)
	}
	if out != "" {
		t.Errorf("com erro e sem saída a nota não deve substituir a mensagem de erro: %q", out)
	}
}
