package tools

// unix_shell.go: o host 'local' é Windows e o exec roda por cmd.exe. A LLM, vinda de ambiente Unix, manda linhas
// estilo bash ('grep -rnE ... | wc -l', 'find . -printf', 'sort -rn | head', 'cd /w/Data/...') e recebia "no result
// (exit 1)" ou uma mensagem de erro do cmd sem dizer o que fazer. Aqui ficam três correções, todas com aviso:
//
//  1. escolha do shell antes de rodar: linha claramente Unix + bash presente no host = a linha inteira vai para
//     'bash -lc', com os caminhos de unidade Windows traduzidos para o que o bash entende (uma execução só, nada de
//     rodar no cmd e reenviar depois);
//  2. nota (nunca no lugar do erro) quando um estágio do comando usa ferramenta Unix que não existe neste host, ou
//     usa o homônimo do Windows (find.exe/sort.exe do System32) com sintaxe Unix — e não há bash para resolver;
//  3. tradução de caminho estilo Git Bash (/w/..., /c/...) para a unidade Windows (W:\...) antes de rodar no cmd.
//
// A detecção é conservadora de propósito: só age em host local Windows; a classificação exige sinal Unix forte e
// nenhum sinal de cmd/PowerShell; a nota só aparece em comando que falhou sem produzir saída, e só fala de ferramenta
// que realmente não resolve neste host (sondagem real de PATH). Na dúvida, cmd.exe, como antes. Comando que funcionou
// não é tocado. Nenhum parâmetro novo: a correção é interna.

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"homelab/fastexec/internal/transport"
)

// unixOnlyTools: primeira palavra de um estágio que só existe em shell Unix. Faltando no host, o comando morre no
// cmd com "'x' não é reconhecido" e a linha inteira vira ruído para a LLM.
var unixOnlyTools = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"wc": true, "head": true, "tail": true, "sed": true, "awk": true, "xargs": true,
	"cat": true, "ls": true, "python": true, "python3": true, "py": true,
}

// unixShellExes: invocação explícita de shell Unix: aí o caminho Unix do argumento é o correto e não se traduz nada.
var unixShellExes = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "wsl": true, "wsl.exe": true}

// toolPath resolve o executável no PATH deste host (variável para os testes não dependerem do PATH da máquina).
var toolPath = func(name string) (string, error) { return exec.LookPath(name) }

func toolExists(name string) bool {
	_, err := toolPath(name)
	return err == nil
}

// bashPathOnHost: caminho do bash (Git for Windows) neste host, "" quando não existe. Sondagem real no PATH e nas
// instalações padrão do Git, com cache por processo (o resultado não muda durante a vida do servidor e a sondagem
// roda a cada exec). É o que decide se a linha Unix vai para o bash ou se fica no cmd com a nota.
var bashPathOnHost = sync.OnceValue(probeBashPathOnHost)

// gitBashCandidates: instalações usuais do Git for Windows, a partir das pastas de ambiente.
var gitBashCandidates = []string{
	`\Git\bin\bash.exe`,
	`\Git\usr\bin\bash.exe`,
	`\Programs\Git\bin\bash.exe`,
	`\Programs\Git\usr\bin\bash.exe`,
}

func probeBashPathOnHost() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if p, err := toolPath("bash"); err == nil && p != "" {
		return p
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		for _, rel := range gitBashCandidates {
			p := root + rel
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

// bashOnHost: existe bash aqui? Decide se a nota oferece o caminho de bash ou só as alternativas nativas.
var bashOnHost = func() bool { return bashPathOnHost() != "" }

// driveExists: a unidade existe? Evita traduzir '/usr/...' (que viraria 'U:\sr\...') por engano.
var driveExists = func(letter byte) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	_, err := os.Stat(strings.ToUpper(string(letter)) + ":\\")
	return err == nil
}

// shellStages quebra a linha nos separadores de shell fora de aspas. Basta para achar a primeira palavra de cada
// estágio ('a | b && c'); pedaços vazios ou lixo ('2>&1' vira '1') não incomodam porque não são nome de ferramenta.
func shellStages(cmd string) []string {
	var stages []string
	var cur strings.Builder
	var quote rune
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == '|' || r == ';' || r == '&' || r == '\n':
			stages = append(stages, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(stages, cur.String())
}

// stageWords: palavras do estágio, já sem atribuições de ambiente à frente ('FOO=1 grep x').
func stageWords(stage string) []string {
	fields := strings.Fields(strings.TrimSpace(stage))
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
		fields = fields[1:]
	}
	return fields
}

// stageTool: nome da ferramenta do estágio, sem diretório nem aspas.
func stageTool(words []string) string {
	if len(words) == 0 {
		return ""
	}
	name := strings.Trim(words[0], `"'`)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// unixFindFlags: flags que só o find do Unix aceita. O find.exe do Windows não conhece nenhuma delas.
var unixFindFlags = []string{"-name", "-iname", "-type", "-printf", "-exec", "-mtime", "-maxdepth", "-mindepth",
	"-path", "-newer", "-size", "-delete", "-print", "-not"}

func hasUnixFindFlags(args []string) bool {
	for _, a := range args {
		for _, f := range unixFindFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// unixToolIn: primeiro estágio cuja ferramenta é Unix-only, ou homônimo do Windows usado com sintaxe Unix.
// Devolve o nome e se é o caso de "homônimo errado" (ferramenta existe, mas é a do Windows).
func unixToolIn(label string) (name string, wrongFlavor bool, ok bool) {
	for _, stage := range shellStages(label) {
		words := stageWords(stage)
		tool := stageTool(words)
		switch {
		case tool == "find" && hasUnixFindFlags(words[1:]):
			return "find", true, true
		case tool == "sort" && hasUnixFlags(words[1:]):
			return "sort", true, true
		case unixOnlyTools[tool]:
			return tool, false, true
		}
	}
	return "", false, false
}

// hasUnixFlags: qualquer flag com '-' no sort (o sort.exe do Windows só aceita '/R', '+n').
func hasUnixFlags(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			return true
		}
	}
	return false
}

// isWindowsFlavorTool: a ferramenta resolvida neste host é a do próprio Windows (System32). Sem isso a nota não
// afirma qual binário rodou — se o find/sort que resolveu é o do Git Bash, a nota se cala.
func isWindowsFlavorTool(name string) bool {
	p, err := toolPath(name)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(p), "system32")
}

// unixToolAlternatives: a alternativa nativa de cada ferramenta, em uma linha curta — é o que a LLM precisa para
// acertar na chamada seguinte. Ferramenta sem entrada aqui cai no texto genérico.
var unixToolAlternatives = map[string]string{
	"grep":  "read_file with pattern/ranges or code_outline; to regex over many files at once use argv ['pwsh','-NoProfile','-Command','-'] with Select-String in 'input'",
	"egrep": "read_file with pattern/ranges; to regex over many files at once use argv ['pwsh','-NoProfile','-Command','-'] with Select-String in 'input'",
	"fgrep": "read_file with pattern/ranges; to regex over many files at once use argv ['pwsh','-NoProfile','-Command','-'] with Select-String in 'input'",
	"rg":    "read_file with pattern/ranges or code_outline; to regex over many files at once use argv ['pwsh','-NoProfile','-Command','-'] with Select-String in 'input'",
	"find":  "file_ops list or code_outline; to walk with a filter use argv ['pwsh','-NoProfile','-Command','-'] with Get-ChildItem in 'input'",
	"ls":    "file_ops list (action 'list')",
	"cat":   "read_file (offset/limit give the same slice)",
	"head":  "read_file (limit gives the same slice)",
	"tail":  "read_file (offset gives the last lines)",
	"wc":    "read_file (it reports the line count) or argv ['pwsh','-NoProfile','-Command','-'] with the equivalent script in 'input'",
	"sort":  "argv ['pwsh','-NoProfile','-Command','-'] with Sort-Object in 'input'",
	"sed":   "read_file / edit_file, or argv ['pwsh','-NoProfile','-Command','-'] with the equivalent script in 'input'",
	"awk":   "argv ['pwsh','-NoProfile','-Command','-'] with the equivalent script in 'input'",
	"xargs": "argv ['pwsh','-NoProfile','-Command','-'] with the equivalent script in 'input'",
}

const unixToolAltGeneric = "the file tools (read_file with pattern/ranges, code_outline, file_ops) or argv ['pwsh','-NoProfile','-Command','-'] with the script in 'input'"

func unixToolAlt(tool string) string {
	if alt, ok := unixToolAlternatives[tool]; ok {
		return alt
	}
	return unixToolAltGeneric
}

// unixShellHint: nota pedagógica para o comando Unix que morreu no cmd.exe. Vazia quando a ferramenta existe aqui,
// quando não há estágio Unix, quando a linha já entrega o texto a um shell Unix (bash/sh/wsl) ou quando o homônimo do
// Windows não é o que rodou. A alternativa sugerida é a da ferramenta que falhou, não um texto genérico.
func unixShellHint(label string) string {
	if hasUnixShellInvocation(label) {
		return "" // a linha vai para bash/sh: ali o comando Unix está certo, quem falhou foi o shell, não a ferramenta
	}
	name, wrongFlavor, ok := unixToolIn(label)
	if !ok {
		return ""
	}
	var hint string
	switch {
	case wrongFlavor:
		if !isWindowsFlavorTool(name) {
			return ""
		}
		hint = fmt.Sprintf("[FastExec Hint] On host 'local' (Windows) '%s' is the Windows %s.exe from System32, "+
			"a different tool that takes different flags. Use %s.", name, name, unixToolAlt(name))
	case toolExists(name):
		return "" // a ferramenta existe aqui: o erro é outro, e a nota não deve chutar
	default:
		hint = fmt.Sprintf("[FastExec Hint] '%s' is not installed on host 'local' (Windows: commands run through "+
			"cmd.exe, not a Unix shell). Use %s.", name, unixToolAlt(name))
	}
	if bashOnHost() {
		hint += " bash exists on this host: argv ['bash','-lc','<the whole line>'] runs it unchanged."
	}
	return hint
}

// gitBashPathRe: caminho estilo Git Bash ('/w/Data', '/c/tmp/a.sh') precedido de início de linha ou separador.
var gitBashPathRe = regexp.MustCompile(`(^|[\s"'=(|&;>])/([A-Za-z])/([^\s"')|&;>]*)`)

// gitBashToWindows traduz os caminhos estilo Git Bash de uma parte do comando e devolve um exemplo do que mudou
// (vazio quando nada mudou). Só traduz quando a unidade existe de fato.
func gitBashToWindows(s string) (string, string) {
	matches := gitBashPathRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s, ""
	}
	var b strings.Builder
	var example string
	last := 0
	for _, m := range matches {
		letter, rest := s[m[4]:m[5]], s[m[6]:m[7]]
		if !driveExists(letter[0]) {
			continue
		}
		novo := strings.ToUpper(letter) + `:\` + strings.ReplaceAll(rest, "/", `\`)
		b.WriteString(s[last:m[3]]) // até o fim do separador (m[3] é o início da barra); o separador faz parte do texto
		b.WriteString(novo)
		last = m[1] // fim do casamento inteiro: sem isso o caminho antigo voltaria colado no novo
		if example == "" {
			example = fmt.Sprintf("'/%s/%s' -> '%s'", letter, rest, novo)
		}
	}
	if example == "" {
		return s, ""
	}
	b.WriteString(s[last:])
	return b.String(), example
}

// windowsPathForNativeRead: as remediações nativas de leitura (read_file, file_ops) trabalham com o caminho, não com a
// linha. Numa linha que vai para o bash o 'command' fica de propósito em forma POSIX — é o que o bash entende — então
// quem extrai o caminho da linha traduz aqui: é o mesmo que remediateGitBashPaths fazia na linha inteira antes de a
// escolha de shell existir. Sem isso 'cat /c/x/p.txt' deixaria de ser lido nativamente e falharia no read_file.
// Caminho que não é estilo Git Bash (ou cuja unidade não existe) volta intacto.
func windowsPathForNativeRead(p string) string {
	out, _ := gitBashToWindows(p)
	return out
}

// hasUnixShellInvocation: a linha já entrega o texto a um shell Unix ('bash -lc ...', 'wsl ...'): ali o caminho Unix
// está certo e traduzir quebraria o comando.
func hasUnixShellInvocation(label string) bool {
	for _, stage := range shellStages(label) {
		if unixShellExes[stageTool(stageWords(stage))] {
			return true
		}
	}
	return false
}

// remediateGitBashPaths: no host local Windows, troca caminho estilo Git Bash pela unidade Windows antes de rodar e
// devolve command/cwd corrigidos e o aviso para a LLM (vazio quando nada mudou). Serve exec e job.
func remediateGitBashPaths(host, command, cwd string) (string, string, string) {
	if runtime.GOOS != "windows" || (command == "" && cwd == "") {
		return command, cwd, ""
	}
	t, err := transport.ResolveHost(host)
	if err != nil || !t.IsLocal {
		return command, cwd, ""
	}
	if hasUnixShellInvocation(command) || hasUnixShellInvocation(cwd) {
		return command, cwd, ""
	}
	var exemplos []string
	if novo, ex := gitBashToWindows(command); ex != "" {
		command, exemplos = novo, append(exemplos, ex)
	}
	if novo, ex := gitBashToWindows(cwd); ex != "" {
		cwd, exemplos = novo, append(exemplos, ex)
	}
	if len(exemplos) == 0 {
		return command, cwd, ""
	}
	return command, cwd, "[FastExec Auto-Remediation] This host is Windows and cmd.exe does not understand Git Bash paths; " +
		"translated " + strings.Join(exemplos, ", ") + " before running. Next time write the Windows path (e.g. W:\\Data\\x); " +
		"when bash is the right tool here, use argv ['bash','-lc','<the whole line>']."
}

// --- Escolha do shell antes de rodar (linha Unix no host Windows) -------------------------------------------------
//
// O benchmark ds-full4 mediu o custo de deixar a linha Unix morrer no cmd.exe: o modelo gasta 4 a 6 turnos
// corrigindo a sintaxe (R1 +77%, T8 +23%, E4 +41% de tokens contra o nativo). Com bash no host, rodar a linha certa
// de primeira é mais barato do que qualquer nota. A escolha é feita ANTES de rodar, uma execução só, e a saída diz
// que o shell foi trocado.

// windowsLineTools: primeira palavra de um estágio que é ferramenta do cmd/PowerShell e não existe em shell Unix.
// Presente, a linha fica no cmd.exe (comportamento de sempre): converter seria adivinhar. Homônimos (find/sort/type)
// ficam de fora de propósito — quem decide neles é o sinal da sintaxe, não o nome.
var windowsLineTools = map[string]bool{
	"dir": true, "findstr": true, "type": true, "copy": true, "move": true, "del": true, "erase": true, "ren": true,
	"md": true, "rd": true, "cls": true, "where": true, "start": true, "tasklist": true, "taskkill": true,
	"icacls": true, "robocopy": true, "xcopy": true, "attrib": true, "fc": true, "chkdsk": true, "net": true,
	"sc": true, "reg": true, "wmic": true, "certutil": true, "schtasks": true, "powershell": true, "pwsh": true,
	"cmd": true,
}

// unixLineTools: primeira palavra de um estágio que só existe em shell Unix — o mesmo conjunto do benchmark, mais os
// filtros de pipeline. Ferramenta que existe nos dois lados (python, curl, node) fica de fora: não é sinal.
var unixLineTools = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"wc": true, "head": true, "tail": true, "sort": true, "uniq": true, "cut": true, "sed": true, "awk": true,
	"xargs": true, "tee": true, "tr": true, "cat": true, "ls": true, "find": true, "which": true,
}

var (
	// powershellCmdletRe: verbo-substantivo do PowerShell (Get-Content, Select-String, Sort-Object) — nunca aparece
	// numa linha Unix, sempre numa linha de PowerShell. Só verbos conhecidos: 'My-File.txt' não é cmdlet.
	powershellCmdletRe = regexp.MustCompile(`\b(Add|Block|Checkpoint|Clear|Compare|Complete|Confirm|ConvertFrom|` +
		`ConvertTo|Copy|Debug|Deny|Disable|Dismount|Enable|Enter|Exit|Export|ForEach|Format|Get|Group|Grant|Hide|` +
		`Import|Invoke|Join|Lock|Measure|Mount|Move|New|Optimize|Out|Pop|Protect|Push|Read|Receive|Register|Remove|` +
		`Rename|Repair|Reset|Resolve|Restart|Restore|Resume|Revoke|Save|Select|Send|Set|Show|Sort|Split|Start|Stop|` +
		`Submit|Suspend|Switch|Test|Trace|Unblock|Undo|Unlock|Unprotect|Unpublish|Unregister|Update|Use|Wait|Write)-[A-Z][a-zA-Z]*\b`)
	// cmdVarRe: variável do cmd (%TEMP%, %~dp0).
	cmdVarRe = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%|%~[a-z]*[0-9]`)
	// cmdForRe/cmdIfRe/cmdSetRe/cmdNulRe/cmdFlagRe/cmdExtRe: construções que só o cmd tem. A flag solta
	// ('dir /s', 'findstr /i') exige fim de token: '/d/' é unidade montada no bash, não flag.
	cmdForRe  = regexp.MustCompile(`(?i)(^|[\s&|(])for\s+/[rfdl]\b`)
	cmdIfRe   = regexp.MustCompile(`(?i)(^|[\s&|(])if\s+(exist|not)\b`)
	cmdSetRe  = regexp.MustCompile(`(?i)(^|[\s&|(])set\s+[A-Za-z_][A-Za-z0-9_]*=`)
	cmdNulRe  = regexp.MustCompile(`(?i)>\s*nul\b`)
	cmdFlagRe = regexp.MustCompile(`(?i)(^|\s)/[dsbfiv](\s|$|")`)
	cmdExtRe  = regexp.MustCompile(`(?i)\.(bat|cmd)\b`)
	// posixAssignRe: linha que começa com atribuição POSIX (VAR=valor). O cmd.exe não tem essa sintaxe — lê o primeiro
	// token como nome de comando — e é aí que o P3 do bench ciclo1b travava: 'start=$(date +%s%N); ...' virava o
	// builtin START do cmd, que engole o resto da linha e nunca termina (a exec só voltava no timeout de 300 s).
	posixAssignRe = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*=`)
)

// hasWindowsSignal: a linha carrega algo que só o cmd/PowerShell entende. Vence qualquer sinal Unix: na dúvida,
// cmd.exe (como antes desta correção).
func hasWindowsSignal(label string) bool {
	for _, stage := range shellStages(label) {
		if windowsLineTools[stageTool(stageWords(stage))] {
			return true
		}
	}
	for _, re := range []*regexp.Regexp{powershellCmdletRe, cmdVarRe, cmdForRe, cmdIfRe, cmdSetRe, cmdNulRe, cmdFlagRe, cmdExtRe} {
		if re.MatchString(label) {
			return true
		}
	}
	return false
}

// hasGitBashPath: a linha traz caminho estilo Git Bash ('cd /w/Data') de uma unidade que existe de fato. É sinal
// forte: no cmd.exe esse caminho não leva a lugar nenhum.
func hasGitBashPath(label string) bool {
	for _, m := range gitBashPathRe.FindAllStringSubmatchIndex(label, -1) {
		if driveExists(label[m[4]]) {
			return true
		}
	}
	return false
}

// hasUnixSignal: sinal forte de linha Unix. Cada um é sintaxe que o cmd.exe não interpreta como a LLM espera.
func hasUnixSignal(label string) bool {
	if strings.Contains(label, "$(") || strings.Contains(label, "/dev/null") || hasGitBashPath(label) {
		return true
	}
	// aspas simples: no cmd são literais; no shell Unix citam. Uma aspas solta (apóstrofo em texto) não conta.
	if strings.Count(label, "'") >= 2 {
		return true
	}
	for _, stage := range shellStages(label) {
		words := stageWords(stage)
		switch tool := stageTool(words); {
		case tool == "find":
			if hasUnixFindFlags(words[1:]) {
				return true
			}
		case tool == "sort":
			if hasUnixFlags(words[1:]) {
				return true
			}
		case unixLineTools[tool]:
			return true
		}
	}
	return false
}

// unixStyleLine: linha claramente Unix (sinal forte) e sem nenhum sinal de cmd/PowerShell. A linha que já entrega o
// texto a um shell Unix (bash/sh/wsl) não entra: esse caminho continua intocado.
func unixStyleLine(label string) bool {
	if strings.TrimSpace(label) == "" || hasUnixShellInvocation(label) {
		return false
	}
	// Atribuição POSIX no começo vence o veto de cmd/PowerShell: nenhuma linha válida do cmd.exe começa com 'VAR='.
	// Sem isso, 'start=$(date +%s%N); ...' ficava no cmd.exe (o '%s%' de '+%s%N' parece variável do cmd) e travava.
	if posixAssignRe.MatchString(label) && hasUnixSignal(label) {
		return true
	}
	return !hasWindowsSignal(label) && hasUnixSignal(label)
}

// winDrivePathInLineRe: caminho de unidade Windows ('W:\ReposHomeLab', 'c:/tmp/x') precedido de início de linha ou
// separador — mesmo formato do winDrivePathRe de host_autolocal.go, com a letra e o resto em grupos próprios.
var winDrivePathInLineRe = regexp.MustCompile(`(^|[\s"'=(|&;>])([A-Za-z]):[\\/]([^\s"')|&;>]*)`)

// windowsPathsToPosix traduz os caminhos de unidade Windows de uma linha para o que o bash entende ('/w/...') e
// devolve um exemplo do que mudou (vazio quando nada mudou). Sem isso o bash come as barras invertidas de 'W:\x' sem
// aspas e o comando recebe 'W:x'. Só traduz quando a unidade existe de fato.
func windowsPathsToPosix(s string) (string, string) {
	matches := winDrivePathInLineRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s, ""
	}
	var b strings.Builder
	var example string
	last := 0
	for _, m := range matches {
		letter, rest := s[m[4]:m[5]], s[m[6]:m[7]]
		if !driveExists(letter[0]) {
			continue
		}
		orig := s[m[3]:m[1]]
		novo := "/" + strings.ToLower(letter) + "/" + strings.ReplaceAll(rest, `\`, "/")
		b.WriteString(s[last:m[3]]) // até o começo do caminho; o separador faz parte do texto
		b.WriteString(novo)
		last = m[1]
		if example == "" {
			example = fmt.Sprintf("'%s' -> '%s'", orig, novo)
		}
	}
	if example == "" {
		return s, ""
	}
	b.WriteString(s[last:])
	return b.String(), example
}

// bashPlanFor: a linha vai para o bash antes de rodar? Devolve o argv pronto ('bash -lc <linha>'), a nota para a LLM
// e ok=false quando o caminho de sempre (cmd.exe) vale — sem bash no host, host remoto, linha não-Unix ou linha que
// já chama um shell Unix. A nota diz o que rodou e como pedir o mesmo na próxima chamada.
func bashPlanFor(host, command string) ([]string, string, bool) {
	if runtime.GOOS != "windows" || strings.TrimSpace(command) == "" {
		return nil, "", false
	}
	t, err := transport.ResolveHost(host)
	if err != nil || !t.IsLocal {
		return nil, "", false
	}
	if !unixStyleLine(command) {
		return nil, "", false
	}
	bash := bashPathOnHost()
	if bash == "" {
		return nil, "", false // sem bash: fica a nota acionável de unixShellHint
	}
	line, ex := windowsPathsToPosix(command)
	note := fmt.Sprintf("[FastExec Auto-Remediation] Unix-style command ran in bash (%s) instead of cmd.exe on this "+
		"Windows host", bash)
	if ex != "" {
		note += ", with " + ex + " translated for bash"
	}
	note += ". Next time pass the line as argv ['bash','-lc','<the whole line>']."
	return []string{bash, "-lc", line}, note, true
}

// remediateLocalWindowsPaths: no host local Windows, decide quem cuida dos caminhos. Linha que vai para o bash: o
// caminho Unix do 'command' já está certo e só o 'cwd' precisa da forma de unidade (o CreateProcess exige diretório
// real); senão, traduz como antes. Devolve command/cwd e o aviso para a LLM.
func remediateLocalWindowsPaths(host, command, cwd string) (string, string, string) {
	if _, _, toBash := bashPlanFor(host, command); toBash {
		_, novoCwd, note := remediateGitBashPaths(host, "", cwd)
		return command, novoCwd, note
	}
	return remediateGitBashPaths(host, command, cwd)
}

// unixShellNoteFor: a nota do comando Unix que morreu, só quando o comando de fato falhou sem saída no host local.
// O erro original nunca é escondido: a nota é anexada ao texto que já trazia o erro.
func unixShellNoteFor(label string, res *transport.ExecResult) string {
	if !res.Local || res.TimedOut || res.ExitCode == 0 || nonEmptyLines(res.Stdout) > 0 {
		return ""
	}
	return unixShellHint(label)
}

// joinNotes junta duas notas de aviso (host, tradução) na ordem em que aconteceram.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}
