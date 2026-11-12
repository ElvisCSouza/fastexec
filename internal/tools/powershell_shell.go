package tools

// powershell_shell.go: o host 'local' é Windows e o exec roda por cmd.exe. A LLM também manda PowerShell na forma de
// uma linha só ('$env:DOTNET_ROOT=...; & "W:\...\dotnet.exe" build ...') e a linha morria sem produzir nada: o
// cmd.exe devolvia "syntax error near unexpected token '&'" (o '$env:' e as aspas fazem a linha parecer Unix, então
// ela ia para o bash — erro de sintaxe igual) e a chamada seguinte era gasta para adivinhar o shell. Aqui a linha com
// sintaxe inequívoca de PowerShell vai inteira para 'pwsh -NoProfile -Command' (ou 'powershell' quando não há pwsh),
// com aviso dizendo o que rodou e como repetir passando argv. Nenhum parâmetro novo no schema.
//
// Conservador de propósito (precisão antes de cobertura): só age em host local Windows; exige sinal forte de
// PowerShell; e não age quando algum estágio usa ferramenta Unix-only ou o homônimo do Windows com sintaxe Unix
// (linha mista como 'grep x | Select-Object -First 5' continua no caminho de sempre). Na dúvida, cmd.exe como antes.

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"homelab/fastexec/internal/transport"
)

// Sinais fortes de PowerShell numa linha:
//
//	psEnvRe        '$env:DOTNET_ROOT' (aceita ${env:...} e qualquer caixa);
//	psAssignRe     atribuição '$x = ...' no começo de um estágio (em bash ou cmd isso é inválido);
//	psCallOpRe     operador de chamada '& "C:\...\x.exe"' / "& '...'" (o caso do benchmark);
//	psCommonParamRe parâmetros comuns de cmdlet (-ErrorAction, -OutVariable...).
var (
	psEnvRe         = regexp.MustCompile(`(?i)\$\{?env:`)
	psAssignRe      = regexp.MustCompile(`(?:^|[;|&\n]\s*)\$[A-Za-z_][A-Za-z0-9_]*\s*=`)
	psCallOpRe      = regexp.MustCompile(`(?:^|[^&])&\s*["'$]`)
	psCommonParamRe = regexp.MustCompile(`(?i)\s-(?:erroraction|errorvariable|warningaction|informationaction|outvariable|outbuffer|pipelinevariable)\b`)
	psVerbNounRe    = regexp.MustCompile(`^[A-Za-z]+-[A-Za-z]+$`)
)

// psVerbs: verbos aprovados de cmdlet (a lista que o PowerShell usa em Get-Verb, reduzida ao que aparece em linha de
// comando). Sem a lista, 'nome-composto' de ferramenta Unix ('docker-compose') viraria cmdlet por engano.
var psVerbs = map[string]bool{
	"add": true, "block": true, "clear": true, "close": true, "compare": true, "complete": true, "connect": true,
	"convertfrom": true, "convertto": true, "copy": true, "debug": true, "disable": true, "disconnect": true,
	"enable": true, "enter": true, "error": true, "exit": true, "export": true, "find": true, "foreach": true,
	"format": true, "get": true, "grant": true, "group": true, "hide": true, "import": true, "invoke": true,
	"join": true, "measure": true, "move": true, "new": true, "out": true, "pop": true, "push": true, "read": true,
	"receive": true, "register": true, "remove": true, "rename": true, "repair": true, "resolve": true,
	"restart": true, "resume": true, "save": true, "select": true, "send": true, "set": true, "show": true,
	"sort": true, "split": true, "start": true, "stop": true, "submit": true, "suspend": true, "switch": true,
	"tee": true, "test": true, "trace": true, "unblock": true, "undo": true, "unregister": true, "update": true,
	"use": true, "wait": true, "warning": true, "watch": true, "where": true, "write": true,
}

// powershellExeNames: ordem de preferência. 'pwsh' é o PowerShell 7; 'powershell' é o 5.x do Windows.
var powershellExeNames = []string{"pwsh", "powershell"}

// isCmdletName: 'Get-Item', 'select-string', 'ForEach-Object' — verbo conhecido + substantivo só de letras (o
// substantivo sem ponto é o que separa cmdlet de nome de arquivo: 'wait-for-it.sh' não passa).
func isCmdletName(name string) bool {
	if !psVerbNounRe.MatchString(name) {
		return false
	}
	verb, _, _ := strings.Cut(name, "-")
	return psVerbs[strings.ToLower(verb)]
}

// psCmdletStage: primeiro estágio cuja ferramenta é um cmdlet. O nome vem sem diretório, sem aspas e sem o sufixo
// '.exe'/'.ps1' (chamar '.\Get-Thing.ps1' também precisa do PowerShell).
func psCmdletStage(label string) string {
	for _, stage := range shellStages(label) {
		name := stageTool(stageWords(stage))
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".ps1")
		if isCmdletName(name) {
			return name
		}
	}
	return ""
}

// invokesPowerShell: algum estágio já chama o PowerShell ('pwsh -Command ...'). Aí não há o que embrulhar: a linha
// roda no shell que ela mesma escolheu.
func invokesPowerShell(label string) bool {
	for _, stage := range shellStages(label) {
		switch stageTool(stageWords(stage)) {
		case "pwsh", "powershell", "powershell.exe":
			return true
		}
	}
	return false
}

// hasPowerShellSyntax: a linha deve ser tratada como PowerShell? Linha vazia, linha que já entrega o texto a um shell
// Unix ('bash -lc ...', 'wsl ...'), linha que já chama o PowerShell e linha mista (estágio com ferramenta Unix-only)
// não entram: esses caminhos continuam intocados.
func hasPowerShellSyntax(label string) bool {
	if strings.TrimSpace(label) == "" || hasUnixShellInvocation(label) || invokesPowerShell(label) {
		return false
	}
	if psUnixConflict(label) {
		return false
	}
	if psEnvRe.MatchString(label) || psAssignRe.MatchString(label) || psCallOpRe.MatchString(label) ||
		psCommonParamRe.MatchString(label) {
		return true
	}
	return psCmdletStage(label) != ""
}

// psUnixConflict: algum estágio é ferramenta Unix-only (ou o homônimo do Windows usado com sintaxe Unix). Sem isso o
// 'Select-Object' de 'grep x . | Select-Object -First 5' arrastaria uma linha claramente Unix para o PowerShell.
// A precisão vem antes da cobertura: na linha mista, o caminho continua sendo o de sempre.
// A sonda é a mesma do unix_shell.go (unixToolIn), para as duas detecções nunca discordarem sobre a mesma linha.
func psUnixConflict(label string) bool {
	_, _, ok := unixToolIn(label)
	return ok
}

// psShellPath: onde o PowerShell está neste host e com que nome (o nome é o que a LLM deve passar em argv).
type psShellPath struct{ name, path string }

// toolPath é var (unix_shell.go) para o teste poder fixar o PATH sem depender do host.
var powershellOnHost = sync.OnceValue(probePowerShellOnHost)

func probePowerShellOnHost() psShellPath {
	for _, name := range powershellExeNames {
		if p, err := toolPath(name); err == nil && p != "" {
			return psShellPath{name: name, path: p}
		}
	}
	return psShellPath{}
}

// powershellPlanFor: a linha vai para o PowerShell antes de rodar? Devolve o argv pronto
// ('pwsh -NoProfile -Command <linha>'), a nota para a LLM e ok=false quando o caminho de sempre vale — host remoto,
// fora do Windows, linha sem sintaxe de PowerShell, linha mista com ferramenta Unix ou PowerShell ausente no host.
// A linha vai como está: quem traduz caminho de unidade é o remediateLocalWindowsPaths, antes daqui.
func powershellPlanFor(host, command string) ([]string, string, bool) {
	if runtime.GOOS != "windows" || strings.TrimSpace(command) == "" {
		return nil, "", false
	}
	t, err := transport.ResolveHost(host)
	if err != nil || !t.IsLocal {
		return nil, "", false
	}
	if !hasPowerShellSyntax(command) {
		return nil, "", false
	}
	sh := powershellOnHost()
	if sh.path == "" {
		return nil, "", false
	}
	note := fmt.Sprintf("[FastExec Auto-Remediation] PowerShell syntax detected: ran in %s. Next time pass argv "+
		"['%s','-NoProfile','-Command','<line>'].", sh.name, sh.name)
	return []string{sh.path, "-NoProfile", "-Command", command}, note, true
}
