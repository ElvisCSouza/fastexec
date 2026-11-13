// search_guard.go: guard das buscas de texto no exec. grep recursivo a partir da raiz ganha exclusões de diretórios
// de build/dependência (--exclude-dir repetido: funciona em bash, cmd.exe e argv direto, ao contrário de {a,b});
// saída de busca com mais de searchMaxLines linhas é cortada com dica de como estreitar. Desligar: FASTEXEC_NO_SEARCH_GUARD=1.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/codescan"
)

const searchMaxLines = 40

var (
	searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "findstr": true, "select-string": true, "sls": true}
	limitPrograms  = map[string]bool{"head": true, "tail": true, "wc": true, "select-object": true, "select": true, "measure-object": true, "measure": true}
)

const searchExcludedNote = "[FastExec Guard] Skipped .git, node_modules, dist, build, vendor and similar dirs; pass --exclude-dir yourself to search them."

func searchGuardOff() bool { return os.Getenv("FASTEXEC_NO_SEARCH_GUARD") != "" }

func isNoiseDir(name string) bool  { return codescan.IsNoiseDir(name) }
func isNoiseFile(name string) bool { return codescan.IsNoiseFile(name) }

func grepExcludeArgs() []string {
	out := make([]string, 0, len(codescan.NoiseDirs)+len(codescan.NoiseFiles))
	for _, d := range codescan.NoiseDirs {
		out = append(out, "--exclude-dir="+d)
	}
	for _, f := range codescan.NoiseFiles {
		out = append(out, "--exclude="+f)
	}
	return out
}

// applyGrepExcludes injeta as exclusões logo depois do programa quando o comando é grep recursivo sobre a raiz
// ("." ou sem caminho) e não traz --exclude-dir próprio. Devolve true se mudou o comando.
func applyGrepExcludes(args *ExecArgs) bool {
	if searchGuardOff() {
		return false
	}
	if len(args.Argv) > 0 {
		if !needsGrepExcludes(args.Argv) {
			return false
		}
		argv := append([]string{args.Argv[0]}, grepExcludeArgs()...)
		args.Argv = append(argv, args.Argv[1:]...)
		return true
	}
	segs := shellSegments(args.Command)
	if len(segs) == 0 {
		return false
	}
	tokens := shellWords(segs[0])
	if !needsGrepExcludes(tokens) {
		return false
	}
	lead := len(args.Command) - len(strings.TrimLeft(args.Command, " \t"))
	if !strings.HasPrefix(args.Command[lead:], tokens[0]) {
		return false // programa entre aspas ou com prefixo: não arrisca reescrever
	}
	cut := lead + len(tokens[0])
	args.Command = args.Command[:cut] + " " + strings.Join(grepExcludeArgs(), " ") + args.Command[cut:]
	return true
}

// grepArgs é o que importa de uma linha de grep: recursivo, se já traz --exclude-dir e os caminhos varridos.
type grepArgs struct {
	recursive, excludeDir bool
	pattern               string // padrão buscado (-e/--regexp ou primeiro posicional)
	paths                 []string
}

// parseGrepArgs lê flags e posicionais do grep (o padrão é o primeiro posicional, salvo -e/-f).
func parseGrepArgs(tokens []string) (grepArgs, bool) {
	var g grepArgs
	if len(tokens) == 0 || !isGrepProgram(tokens[0]) {
		return g, false
	}
	patternGiven, endFlags := false, false
	var positional []string
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case endFlags:
			positional = append(positional, t)
		case t == "--":
			endFlags = true
		case strings.HasPrefix(t, "--"):
			switch {
			case strings.HasPrefix(t, "--exclude-dir"):
				g.excludeDir = true
			case t == "--recursive" || t == "--dereference-recursive":
				g.recursive = true
			case t == "--regexp" || t == "--file":
				patternGiven = true
				if t == "--regexp" && i+1 < len(tokens) && g.pattern == "" {
					g.pattern = tokens[i+1]
				}
				i++
			case strings.HasPrefix(t, "--regexp=") || strings.HasPrefix(t, "--file="):
				patternGiven = true
				if v, ok := strings.CutPrefix(t, "--regexp="); ok && g.pattern == "" {
					g.pattern = v
				}
			case t == "--max-count" || t == "--context" || t == "--after-context" || t == "--before-context":
				i++
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			cluster := t[1:]
		flags:
			for j, c := range cluster {
				switch c {
				case 'r', 'R':
					g.recursive = true
				case 'e', 'f':
					patternGiven = true
					val := cluster[j+1:]
					if j == len(cluster)-1 {
						if i+1 < len(tokens) {
							val = tokens[i+1]
						}
						i++
					}
					if c == 'e' && g.pattern == "" {
						g.pattern = val
					}
					break flags
				case 'A', 'B', 'C', 'm':
					if j == len(cluster)-1 {
						i++
					}
					break flags
				}
			}
		case strings.ContainsAny(t, "<>"):
			// redirecionamento (2>/dev/null): não é caminho
		default:
			positional = append(positional, t)
		}
	}
	g.paths = positional
	if !patternGiven && len(g.paths) > 0 {
		g.pattern = g.paths[0]
		g.paths = g.paths[1:]
	}
	return g, true
}

func isDotPath(p string) bool { return p == "." || p == "./" || p == `.\` }

// needsGrepExcludes: grep recursivo, sem --exclude-dir próprio, sobre a raiz (caminho ausente ou ".").
func needsGrepExcludes(tokens []string) bool {
	g, ok := parseGrepArgs(tokens)
	if !ok || !g.recursive || g.excludeDir {
		return false
	}
	if len(g.paths) == 0 {
		return true
	}
	for _, p := range g.paths {
		if isDotPath(p) {
			return true
		}
	}
	return false
}

func isGrepProgram(prog string) bool {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(prog), ".exe"))
	return base == "grep" || base == "egrep" || base == "fgrep"
}

// isTextSearch: algum trecho do pipeline é busca de texto e nenhum trecho seguinte já limita a saída (head, wc...).
func isTextSearch(label string) bool {
	found := false
	for _, seg := range shellSegments(label) {
		tokens := shellWords(seg)
		if len(tokens) == 0 {
			continue
		}
		prog := strings.ToLower(strings.TrimSuffix(filepath.Base(tokens[0]), ".exe"))
		switch {
		case (prog == "pwsh" || prog == "powershell") && pwshInlineSearch(tokens):
			found = true
		case searchPrograms[prog], prog == "git" && len(tokens) > 1 && tokens[1] == "grep":
			found = true
		case found && limitPrograms[prog] && limitsOutput(prog, tokens):
			return false
		}
	}
	return found
}

// limitsOutput: o trecho realmente limita a saída? Select-Object só limita com -First/-Last/-Index; sem isso
// (-ExpandProperty Path, -Property ...) ele só projeta colunas e a busca continua ilimitada.
func limitsOutput(prog string, tokens []string) bool {
	if prog != "select-object" && prog != "select" {
		return true
	}
	for _, t := range tokens[1:] {
		lt := strings.ToLower(t)
		if strings.HasPrefix(lt, "-first") || strings.HasPrefix(lt, "-last") || strings.HasPrefix(lt, "-index") {
			return true
		}
	}
	return false
}

// pwshInlineSearch: "pwsh [-NoProfile] -Command <script>" cujo script é busca (Select-String, findstr...).
func pwshInlineSearch(tokens []string) bool {
	for i, t := range tokens {
		if lt := strings.ToLower(t); (lt == "-command" || lt == "-c") && i+1 < len(tokens) {
			return isTextSearch(strings.Join(tokens[i+1:], " "))
		}
	}
	return false
}

// truncateSearchOutput corta a saída em searchMaxLines linhas; devolve o texto, o total de linhas e se cortou.
func truncateSearchOutput(stdout string) (string, int, bool) {
	body := strings.TrimRight(stdout, "\r\n")
	if body == "" {
		return stdout, 0, false
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= searchMaxLines {
		return stdout, len(lines), false
	}
	return strings.Join(lines[:searchMaxLines], "\n") + "\n", len(lines), true
}

func searchTruncatedHint(total int) string {
	return fmt.Sprintf("[FastExec Guard] Search returned %d lines; showing the first %d to protect your context. Narrow it (--include='*.go', a subfolder, -l) or use 'code_outline' to find declarations.", total, searchMaxLines)
}

// shellSegments corta a linha nos separadores de comando (| ; & e quebra de linha) fora de aspas.
func shellSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	var quote rune
	for _, c := range cmd {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteRune(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteRune(c)
		case c == '|' || c == ';' || c == '&' || c == '\n':
			if s := strings.TrimSpace(cur.String()); s != "" {
				segs = append(segs, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		segs = append(segs, s)
	}
	return segs
}

// shellWords separa por espaço fora de aspas e remove as aspas (suficiente para ler flags e caminhos).
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == ' ' || c == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// tokenSpan é a extensão de um token na linha (aspas incluídas), para tirar um token do comando por posição sem
// reescrever o resto.
type tokenSpan struct{ Start, End int }

// shellTokenSpans devolve a extensão de cada token na mesma regra de shellWords (espaço fora de aspas separa).
func shellTokenSpans(s string) []tokenSpan {
	var out []tokenSpan
	start := -1
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
			if start < 0 {
				start = i
			}
		case c == ' ' || c == '\t':
			if start >= 0 {
				out = append(out, tokenSpan{start, i})
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, tokenSpan{start, len(s)})
	}
	return out
}

// reCallSearch: o padrão buscado é um identificador seguido de '(' (ou '\(' escapado), isto é, procura de chamadas.
var reCallSearch = regexp.MustCompile(`[A-Za-z_]\w{2,}\\?\(`)

const callSearchHint = "[FastExec FastHint] For call sites use tool 'code_graph' (action 'callers'): graph lookup, no file reading."

// Busca ampla: texto buscado recursivamente que devolve muitas linhas costuma ser busca por conceito ("fee|taxa").
// O code_outline com pasta + symbol (substring do nome) lista só as declarações com a palavra, numa chamada.
var reRecursiveSearch = regexp.MustCompile(`(?i)-recurse\b|\bgrep\s+-[a-z]*r|\bgrep\s+--recursive|\brg\s|git\s+grep|findstr\s+(?:/\w+\s+)*/s\b`)

const broadSearchMinLines = 10

const broadSearchHint = "[FastExec FastHint] Concept search: code_outline(path=<repo or folder>, symbol='<word>') lists the declarations whose name contains the word, with line numbers, in one call."

var reSearchProgram = regexp.MustCompile(`(?i)\bselect-string\b|\bsls\b|\bgrep\b|\brg\b|\bfindstr\b`)

// isBroadSearch: busca de texto recursiva (não é busca de declaração nem de chamada, que têm dica própria). Vale mesmo
// com limite do próprio agente (Select-Object -First 40): a busca continua ampla, só a saída foi cortada.
func isBroadSearch(label string) bool {
	return reSearchProgram.MatchString(label) && reRecursiveSearch.MatchString(label) && !reDeclSearch.MatchString(label) && !reCallSearch.MatchString(label)
}

func nonEmptyLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// Agregação de arquivo grande por script (Get-Content | group, cat | sort | uniq -c): o read_file com pattern já devolve
// a contagem por mensagem distinta, numa chamada e sem ler o arquivo para o contexto.
var (
	reAggRead = regexp.MustCompile(`(?i)\bGet-Content\b|\bgc\s|\bcat\s|\btype\s|\bSelect-String\b|\bgrep\b`)
	reAggDo   = regexp.MustCompile(`(?i)\bGroup-Object\b|\|\s*group\b|\bsort\b.*\buniq\b|\buniq\s+-c\b|\bMeasure-Object\b|\|\s*measure\b`)
)

const aggregateHint = "[FastExec FastHint] To count or summarize lines of a file, read_file(path, pattern='regex') returns counts per distinct message with first/last line, in one call."

func isFileAggregate(label string) bool {
	return reAggRead.MatchString(label) && reAggDo.MatchString(label)
}

// Python ausente (no Windows o alias da Microsoft Store responde 9009): o agente perde um turno tentando de novo.
const pythonMissingHint = "[FastExec FastHint] Python is not installed on this host: use pwsh (argv ['pwsh','-NoProfile','-Command','-'] with the script in 'input') or the file tools (read_file pattern/ranges, code_outline, edit_file)."

func isPythonMissing(stdout string) bool {
	return strings.Contains(stdout, "Python was not found") || strings.Contains(stdout, "python: command not found") || strings.Contains(stdout, "python3: command not found")
}

// Edição de arquivo por script (ler, substituir, gravar): o edit_file faz o mesmo numa chamada, sem script, mantendo
// a quebra de linha do arquivo e atualizando o grafo de código.
var (
	reEditRead    = regexp.MustCompile(`(?i)\bGet-Content\b|\bgc\s|ReadAllText|ReadAllLines`)
	reEditWrite   = regexp.MustCompile(`(?i)\bSet-Content\b|\bsc\s|WriteAllText|WriteAllLines|\bOut-File\b`)
	reEditReplace = regexp.MustCompile(`(?i)-c?replace\b|\.Replace\(`)
	reSedInPlace  = regexp.MustCompile(`\bsed\s+(?:-[a-zA-Z]*\s+)*-i|\bperl\s+-[a-z]*p[a-z]*i`)
)

const editScriptHint = "[FastExec FastHint] To change a file use tool 'edit_file' with symbol='name' (AST replacement) or 'edits': no script escaping, keeps line endings, refreshes the code graph."

func isEditScript(label string) bool {
	if reSedInPlace.MatchString(label) {
		return true
	}
	return reEditRead.MatchString(label) && reEditWrite.MatchString(label) && reEditReplace.MatchString(label)
}

// reDeclSearch: o padrão buscado parece declaração ("void Position", "struct Row", "func Foo", "def bar").
var reDeclSearch = regexp.MustCompile(`\b(?:func|def|class|struct|enum|interface|void|bool|static|HRESULT|LRESULT|BOOL|DWORD)\s+[A-Za-z_]\w*`)

const declSearchHint = "[FastExec FastHint] To find where something is declared use tool 'code_outline' (path = file or folder, symbol = name): line numbers, no file reading."

// isDeclSearch: busca de texto que procura onde algo é declarado.
func isDeclSearch(label string) bool {
	return isTextSearch(label) && reDeclSearch.MatchString(label)
}

// reMemberSearch: o padrão buscado é um membro qualificado ("SelfLog.WriteLine"): quem busca assim costuma estar
// prestes a renomeá-lo ou a mudar seus chamadores.
var reMemberSearch = regexp.MustCompile(`\b[A-Z]\w+\\?\.[A-Z]\w+\b`)

const memberSearchHint = "[FastExec FastHint] Call sites: tool 'code_graph' (action 'callers'). To rename it: edit_file on its declaration file with symbol='Old', replacement='New'; callers are updated and checked automatically; no sed."

// isMemberSearch: busca de texto por Tipo.Membro. Vale mesmo com '| head': quem busca um membro para ver os
// chamadores está a um passo de renome-lo, e é aí que a dica ainda chega a tempo. Só o padrão conta, não um
// caminho ("test/Serilog.ApprovalTests/..." tem Pascal.Pascal e não é membro nenhum).
func isMemberSearch(label string) bool {
	for _, seg := range shellSegments(label) {
		tokens := shellWords(seg)
		if len(tokens) == 0 {
			continue
		}
		prog := strings.ToLower(strings.TrimSuffix(filepath.Base(tokens[0]), ".exe"))
		if !searchPrograms[prog] && !(prog == "git" && len(tokens) > 1 && tokens[1] == "grep") {
			continue
		}
		for _, tk := range tokens[1:] {
			if strings.HasPrefix(tk, "-") {
				continue
			}
			if len(tk) > 3 && strings.EqualFold(tk[:3], "/c:") {
				tk = tk[3:] // findstr /c:"texto"
			}
			if tk = strings.ReplaceAll(tk, `\.`, "."); strings.ContainsAny(tk, `/\`) {
				continue
			}
			if reMemberSearch.MatchString(tk) {
				return true
			}
		}
	}
	return false
}

// isCallSearch: busca de texto cujo padrão parece chamada de função.
func isCallSearch(label string) bool {
	return isTextSearch(label) && reCallSearch.MatchString(label)
}
