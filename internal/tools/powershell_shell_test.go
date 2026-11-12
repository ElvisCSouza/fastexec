package tools

import (
	"strings"
	"testing"
)

// comPowerShellFalso fixa a sonda do PowerShell (nome/caminho) sem depender do host que roda o teste. path vazio
// simula host sem pwsh e sem powershell.
func comPowerShellFalso(t *testing.T, name, path string) {
	t.Helper()
	orig := powershellOnHost
	powershellOnHost = func() psShellPath {
		if path == "" {
			return psShellPath{}
		}
		return psShellPath{name: name, path: path}
	}
	t.Cleanup(func() { powershellOnHost = orig })
}

// comPowerShellReal: host Windows com pwsh (ou powershell) de verdade. Pula quando a máquina não tem nenhum.
func comPowerShellReal(t *testing.T) psShellPath {
	t.Helper()
	semAmbienteUnix(t)
	powershellOnHost = probePowerShellOnHost
	sh := powershellOnHost()
	if sh.path == "" {
		t.Skip("sem pwsh/powershell nesta máquina")
	}
	return sh
}

// TestHasPowerShellSyntax: tabela de classificação — sintaxe inequívoca de PowerShell entra; linha de cmd, linha
// Unix e linha mista ficam de fora.
func TestHasPowerShellSyntax(t *testing.T) {
	sim := map[string]string{
		// o comando exato do CS4 (ds-full4), que morreu nos dois shells
		`$env:DOTNET_ROOT='W:\sdk'; $env:NUGET_PACKAGES='W:\nuget'; & 'W:\sdk\dotnet.exe' build src\App\App.csproj`: "operador de chamada",
		`& "C:\Program Files\dotnet\dotnet.exe" --info`:                  "operador de chamada com aspas duplas",
		`$x = Get-ChildItem src`:                                        "atribuição $x = ",
		`$linhas = 3; Write-Output $linhas`:                             "atribuição depois de ';'",
		`Get-ChildItem -Recurse -Filter *.cs | Select-String "Foo"`:      "cmdlet no começo do estágio",
		`Get-Content app.log -Tail 20`:                                  "cmdlet de leitura",
		`Get-ChildItem . -ErrorAction Stop`:                             "parâmetro comum de cmdlet",
		`select-string -Path src\a.cs -Pattern Foo`:                     "cmdlet em minúsculas",
		`ForEach-Object { $_ }`:                                        "cmdlet de pipeline",
		`.\Get-Thing.ps1 -Name x`:                                       "script .ps1 do PowerShell",
		`echo $env:USERNAME`:                                            "variável de ambiente do PowerShell",
	}
	for label, porque := range sim {
		if !hasPowerShellSyntax(label) {
			t.Errorf("%s: devia ser reconhecida como PowerShell: %q", porque, label)
		}
	}
	nao := []string{
		``,
		`   `,
		`findstr /s /i foo *.go`,
		`dir /s /b src\*.cs`,
		`echo ok && echo done`,
		`grep -rn "func main" . | wc -l`,
		`ls -la`,
		`git status --short`,
		`type x.txt | sort | head -5`,
		`go build ./...`,
		`bash -lc "Get-Item x"`,                   // já é shell Unix: não embrulha de novo
		`pwsh -NoProfile -Command "Get-Item x"`,   // já é PowerShell: não embrulha de novo
		`powershell -c "$env:X"`,                  // idem
		`grep x . | Select-Object -First 5`,       // mista: ferramenta Unix no primeiro estágio
		`find . -name '*.cs' | Select-String Foo`, // idem, com homônimo do Windows
		`wc -l < a.txt | Out-File b.txt`,          // idem
	}
	for _, label := range nao {
		if hasPowerShellSyntax(label) {
			t.Errorf("linha que não é PowerShell: %q", label)
		}
	}
}

// TestPowershellPlanFor: o plano só sai no host local Windows com pwsh/powershell presente, e a nota diz o que rodou
// e como repetir.
func TestPowershellPlanFor(t *testing.T) {
	semAmbienteUnix(t)
	comPowerShellFalso(t, "pwsh", `C:\Program Files\PowerShell\7\pwsh.exe`)

	linha := `$env:DOTNET_ROOT='W:\sdk'; & 'W:\sdk\dotnet.exe' build`
	argv, note, ok := powershellPlanFor("local", linha)
	if !ok {
		t.Fatal("linha de PowerShell no host local com pwsh deveria virar plano")
	}
	if len(argv) != 4 || argv[0] != `C:\Program Files\PowerShell\7\pwsh.exe` || argv[1] != "-NoProfile" ||
		argv[2] != "-Command" || argv[3] != linha {
		t.Errorf("argv errado: %#v", argv)
	}
	for _, pedaco := range []string{
		"[FastExec Auto-Remediation] PowerShell syntax detected: ran in pwsh.",
		"Next time pass argv ['pwsh','-NoProfile','-Command','<line>'].",
	} {
		if !strings.Contains(note, pedaco) {
			t.Errorf("nota sem %q: %q", pedaco, note)
		}
	}

	// sem pwsh no host, mas com powershell (5.x): o plano usa o que existe e a nota diz o nome certo.
	comPowerShellFalso(t, "powershell", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`)
	argv, note, ok = powershellPlanFor("local", linha)
	if !ok || argv[0] != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("devia cair no powershell 5.x: %#v / %v", argv, ok)
	}
	if !strings.Contains(note, "ran in powershell") || !strings.Contains(note, "argv ['powershell','-NoProfile'") {
		t.Errorf("nota sem o nome do shell que rodou: %q", note)
	}

	// host sem nenhum PowerShell: caminho de sempre, sem nota.
	comPowerShellFalso(t, "", "")
	if argv, note, ok := powershellPlanFor("local", linha); ok || argv != nil || note != "" {
		t.Errorf("sem PowerShell no host não pode haver plano: %#v / %q / %v", argv, note, ok)
	}

	// host remoto e linha que não é PowerShell: nada muda.
	comPowerShellFalso(t, "pwsh", `C:\pwsh.exe`)
	if _, _, ok := powershellPlanFor("notebook", linha); ok {
		t.Error("host remoto não pode virar plano de PowerShell")
	}
	for _, label := range []string{`findstr /s /i foo *.go`, `grep -rn x . | wc -l`, `echo ok`} {
		if _, _, ok := powershellPlanFor("local", label); ok {
			t.Errorf("%q: linha que não é PowerShell não pode virar plano", label)
		}
	}
}

// TestOrdemDosPlanos: a linha do CS4 também é classificada como Unix (as duas aspas simples bastam para o
// hasUnixSignal), então o plano de bash também a reivindicava — era assim que ela ia parar no bash e morria com
// "syntax error near unexpected token '&'". Este teste fixa a premissa que obriga o PowerShell a vir primeiro no
// handleExec; a ordem em si é coberta de ponta a ponta por TestExecPowerShellRodaNoPwsh.
func TestOrdemDosPlanos(t *testing.T) {
	comBashReal(t) // é o cenário do CS4: bash presente no host
	comPowerShellFalso(t, "pwsh", `C:\pwsh.exe`)
	if _, _, ok := bashPlanFor("local", cs4Lenha()); !ok {
		t.Error("premissa: a linha do CS4 era reivindicada pelo plano de bash — é por isso que o PowerShell vem antes")
	}
	if argv, _, ok := powershellPlanFor("local", cs4Lenha()); !ok || argv[2] != "-Command" {
		t.Fatalf("o plano de PowerShell tem de sair para a linha do CS4: %#v / %v", argv, ok)
	}
}

func cs4Lenha() string {
	return `$env:DOTNET_ROOT='W:\sdk'; $env:NUGET_PACKAGES='W:\nuget'; & 'W:\sdk\dotnet.exe' build src\App\App.csproj`
}

// TestExecPowerShellRodaNoPwsh: ponta a ponta no host local — a linha do CS4 roda de primeira no pwsh, com a nota,
// e a saída/exit code são os do comando.
func TestExecPowerShellRodaNoPwsh(t *testing.T) {
	sh := comPowerShellReal(t)
	out, err := HandleExec(ExecArgs{Host: "local", Command: `$env:FASTEXEC_PS='ok'; Write-Output $env:FASTEXEC_PS`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation] PowerShell syntax detected: ran in "+sh.name) {
		t.Errorf("saída sem a nota do shell que rodou: %q", out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("a linha de PowerShell não rodou: %q", out)
	}
	if strings.Contains(out, "ran in bash") {
		t.Errorf("linha de PowerShell não pode ir para o bash: %q", out)
	}
	if strings.Contains(out, "[FastExec Hint]") {
		t.Errorf("depois de rodar no pwsh não cabe dica de ferramenta ausente: %q", out)
	}

	// erro do comando continua preservado: exit code e stderr são os do pwsh.
	out, err = HandleExec(ExecArgs{Host: "local", Command: `$x = 1; Get-Item C:\fastexec-nao-existe-xyz`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "fastexec-nao-existe-xyz") {
		t.Errorf("o erro do comando tem de aparecer: %q", out)
	}

	// linha de cmd segue no cmd.exe, sem nota de batch/PowerShell.
	out, err = HandleExec(ExecArgs{Host: "local", Command: `echo ok`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if strings.Contains(out, "PowerShell syntax detected") || strings.Contains(out, "ran in bash") {
		t.Errorf("linha de cmd não pode trocar de shell: %q", out)
	}
}
