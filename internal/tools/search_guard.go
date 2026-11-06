// search_guard.go: guard das buscas de texto no exec. grep recursivo a partir da raiz ganha exclusões de diretórios
// de build/dependência (--exclude-dir repetido: funciona em bash, cmd.exe e argv direto, ao contrário de {a,b});
// saída de busca com mais de searchMaxLines linhas é cortada com dica de como estreitar. Desligar: FASTEXEC_NO_SEARCH_GUARD=1.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const searchMaxLines = 40

var (
	noiseDirs  = []string{".git", "node_modules", "dist", "build", "vendor", ".next", ".agent", "tmp", "cache", "__pycache__"}
	noiseFiles = []string{"*.min.js", "*.min.css", "*.map", "package-lock.json", "pnpm-lock.yaml"}

	searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "findstr": true}
	limitPrograms  = map[string]bool{"head": true, "tail": true, "wc": true, "select-object": true, "measure-object": true}
)

const searchExcludedNote = "[FastExec Guard] Skipped .git, node_modules, dist, build, vendor and similar dirs; pass --exclude-dir yourself to search them."

func searchGuardOff() bool { return os.Getenv("FASTEXEC_NO_SEARCH_GUARD") != "" }

// isNoiseDir: diretório de dependência/build/cache ou oculto (.idea, .vscode...), que não vale varrer.
func isNoiseDir(name string) bool {
	for _, d := range noiseDirs {
		if strings.EqualFold(name, d) {
			return true
		}
	}
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

func isNoiseFile(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range noiseFiles {
		if ok, _ := filepath.Match(pat, lower); ok {
			return true
		}
	}
	return false
}

func grepExcludeArgs() []string {
	out := make([]string, 0, len(noiseDirs)+len(noiseFiles))
	for _, d := range noiseDirs {
		out = append(out, "--exclude-dir="+d)
	}
	for _, f := range noiseFiles {
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

// needsGrepExcludes analisa os argumentos do grep: recursivo, sem --exclude-dir, e caminho ausente ou ".".
func needsGrepExcludes(tokens []string) bool {
	if len(tokens) == 0 || !isGrepProgram(tokens[0]) {
		return false
	}
	recursive, patternGiven, endFlags := false, false, false
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
				return false
			case t == "--recursive" || t == "--dereference-recursive":
				recursive = true
			case t == "--regexp" || t == "--file":
				patternGiven = true
				i++
			case strings.HasPrefix(t, "--regexp=") || strings.HasPrefix(t, "--file="):
				patternGiven = true
			case t == "--max-count" || t == "--context" || t == "--after-context" || t == "--before-context":
				i++
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			cluster := t[1:]
		flags:
			for j, c := range cluster {
				switch c {
				case 'r', 'R':
					recursive = true
				case 'e', 'f':
					patternGiven = true
					if j == len(cluster)-1 {
						i++
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
	if !recursive {
		return false
	}
	paths := positional
	if !patternGiven && len(paths) > 0 {
		paths = paths[1:]
	}
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		if p == "." || p == "./" || p == `.\` {
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
		case searchPrograms[prog], prog == "git" && len(tokens) > 1 && tokens[1] == "grep":
			found = true
		case found && limitPrograms[prog]:
			return false
		}
	}
	return found
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
