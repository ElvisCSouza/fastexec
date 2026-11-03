//go:build windows

package transport

import (
	"strings"
	"testing"
)

func TestResolveShellArgv(t *testing.T) {
	// Chamada explícita a powershell deve ser promovida para pwsh quando pwsh estiver presente
	argv := []string{"powershell", "-NoProfile", "-Command", "Write-Output 1"}
	resolved := ResolveShellArgv(argv)
	if len(resolved) != len(argv) {
		t.Fatalf("tamanho de argv alterado: %+v", resolved)
	}
	lower := strings.ToLower(resolved[0])
	if !strings.Contains(lower, "pwsh") && !strings.Contains(lower, "powershell") {
		t.Errorf("executável inesperado: %s", resolved[0])
	}

	// Chamada que já é pwsh deve ser mantida/resolvida para pwsh válido
	argvPwsh := []string{"pwsh", "-NoProfile", "-Command", "Write-Output 1"}
	resolvedPwsh := ResolveShellArgv(argvPwsh)
	if !strings.Contains(strings.ToLower(resolvedPwsh[0]), "pwsh") {
		t.Errorf("deveria manter pwsh: %s", resolvedPwsh[0])
	}

	// Comandos não-powershell (ex: node, git, python) devem permanecer intocados
	argvOther := []string{"node", "-v"}
	resolvedOther := ResolveShellArgv(argvOther)
	if resolvedOther[0] != "node" {
		t.Errorf("comando alterado indevidamente: %s", resolvedOther[0])
	}

	// Execução real de "powershell" deve rodar no runtime do PowerShell 7 (>= 7.0)
	res, err := ExecLocalArgv([]string{"powershell", "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}, "", "", 15000000000)
	if err != nil {
		t.Fatalf("falha ao executar powershell promovido para pwsh: %v", err)
	}
	out := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(out, "7.") {
		t.Errorf("esperava versão 7.x do PowerShell, veio: %q", out)
	}
}
