package tools

// unix_shell.go: o host 'local' é Windows e o exec roda por cmd.exe. A LLM, vinda de ambiente Unix, manda linhas
// estilo bash ('grep -rnE ... | wc -l', 'find . -printf', 'sort -rn | head', 'cd /w/Data/...') e recebia "no result
// (exit 1)" ou uma mensagem de erro do cmd sem dizer o que fazer. Aqui ficam duas correções, ambas com aviso:
//
//  1. nota (nunca no lugar do erro) quando um estágio do comando usa ferramenta Unix que não existe neste host, ou
//     usa o homônimo do Windows (find.exe/sort.exe do System32) com sintaxe Unix;
//  2. tradução de caminho estilo Git Bash (/w/..., /c/...) para a unidade Windows (W:\...) antes de rodar.
//
// A detecção é conservadora de propósito: só age em host local Windows, em comando que falhou sem produzir saída, e
// só fala de ferramenta que realmente não resolve neste host (sondagem real de PATH). Comando que funcionou não é
// tocado. Nenhum parâmetro novo: a correção é interna.

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

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

// bashOnHost: existe bash (Git for Windows) aqui? Decide se a nota oferece o caminho de bash ou só as alternativas
// nativas.
var bashOnHost = func() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return toolExists("bash")
}

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
