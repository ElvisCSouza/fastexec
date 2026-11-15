package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/transport"
)

// Leitura "embrulhada" pelo exec. O 'cat x' puro já vira read_file (tryRemediateCat); este caso cobre o que a
// telemetria mostrou escapando: 'pwsh -Command "Get-Content a -Raw; '===' ; Get-Content b"',
// 'bash -c "cd ~/workspace; cat x"', 'type a & echo --- & type b'. Quando o comando inteiro só troca de pasta,
// imprime separadores e lê arquivos inteiros, cada arquivo passa pelo read_file (grafo primeiro, corte de 32 KB e
// repetição para o texto completo, iguais aos do read_file). Qualquer outra coisa no comando (pipe, redirecionamento,
// variável, trecho de linhas, outro programa) e ele roda no shell como sempre.

type readStep struct {
	literal string // separador impresso (echo, string solta no pwsh)
	path    string // arquivo lido inteiro
}

var (
	reWrapPwsh = regexp.MustCompile(`(?is)^\s*(?:pwsh|powershell)(?:\.exe)?\s+((?:-[a-z]+(?:\s+(?:bypass|unrestricted|remotesigned|hidden))?\s+)*)-(?:command|c)\s+(.+)$`)
	reWrapSh   = regexp.MustCompile(`(?is)^\s*(?:bash|sh)\s+-c\s+(.+)$`)
)

// unwrapShell devolve o script e o shell que o interpreta ("pwsh", "sh" ou "cmd").
func unwrapShell(args ExecArgs, local bool) (script, shell string) {
	if len(args.Argv) > 0 {
		prog := strings.ToLower(strings.TrimSuffix(filepath.Base(args.Argv[0]), ".exe"))
		switch prog {
		case "pwsh", "powershell":
			for i, a := range args.Argv[1:] {
				if la := strings.ToLower(a); la == "-command" || la == "-c" {
					return strings.Join(args.Argv[i+2:], " "), "pwsh"
				}
			}
		case "bash", "sh":
			if len(args.Argv) == 3 && args.Argv[1] == "-c" {
				return args.Argv[2], "sh"
			}
		}
		return "", ""
	}
	cmd := strings.TrimSpace(args.Command)
	if m := reWrapPwsh.FindStringSubmatch(cmd); m != nil {
		return stripOuterQuotes(m[2]), "pwsh"
	}
	if m := reWrapSh.FindStringSubmatch(cmd); m != nil {
		return stripOuterQuotes(m[1]), "sh"
	}
	if local {
		return cmd, "cmd"
	}
	return cmd, "sh"
}

func stripOuterQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] && !strings.ContainsRune(s[1:len(s)-1], rune(s[0])) {
		return s[1 : len(s)-1]
	}
	return s
}

// splitReadScript corta em ; && quebra de linha (e & no cmd) fora de aspas. ok=false se houver pipe,
// redirecionamento, variável ou substituição: aí não é só leitura.
func splitReadScript(script, shell string) ([]string, bool) {
	if strings.ContainsAny(script, "$`%") {
		return nil, false
	}
	var segs []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == '|' || c == '>' || c == '<' || c == '(' || c == ')' || c == '{' || c == '}':
			return nil, false
		case c == ';' || c == '\n' || c == '\r':
			flush()
		case c == '&':
			if i+1 < len(script) && script[i+1] == '&' {
				i++
				flush()
			} else if shell == "cmd" {
				flush()
			} else {
				return nil, false // '&' do pwsh (chamada) ou do sh (segundo plano)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return segs, len(segs) > 0
}

// parseReadScript: os passos, se o script for só cd + separadores + leituras inteiras (com ao menos uma leitura).
func parseReadScript(script, shell string, join func(dir, p string) string, cwd string) ([]readStep, bool) {
	segs, ok := splitReadScript(script, shell)
	if !ok {
		return nil, false
	}
	var steps []readStep
	reads := 0
	for _, seg := range segs {
		w := shellWords(seg)
		if len(w) == 0 {
			continue
		}
		// string solta no pwsh é impressa
		if shell == "pwsh" && len(w) == 1 && (seg[0] == '\'' || seg[0] == '"') {
			steps = append(steps, readStep{literal: w[0]})
			continue
		}
		prog := strings.ToLower(strings.TrimSuffix(w[0], ".exe"))
		rest := w[1:]
		switch prog {
		case "cd", "chdir", "set-location", "sl", "pushd":
			var dirs []string
			for i := 0; i < len(rest); i++ {
				a := strings.ToLower(rest[i])
				switch {
				case a == "/d" || a == "-path" || a == "-literalpath":
				case strings.HasPrefix(a, "-"):
					return nil, false
				default:
					dirs = append(dirs, rest[i])
				}
			}
			if len(dirs) != 1 {
				return nil, false
			}
			cwd = join(cwd, dirs[0])
		case "echo", "write-output", "write-host":
			if shell == "cmd" && len(rest) == 0 {
				return nil, false // 'echo' sozinho no cmd imprime o estado do echo
			}
			steps = append(steps, readStep{literal: strings.Join(rest, " ")})
		case "cat", "type", "get-content", "gc":
			var paths []string
			for i := 0; i < len(rest); i++ {
				a := strings.ToLower(rest[i])
				switch {
				case a == "-raw":
				case (a == "-encoding" || a == "-path" || a == "-literalpath") && i+1 < len(rest):
					if a != "-encoding" {
						paths = append(paths, rest[i+1])
					}
					i++
				case strings.HasPrefix(a, "-") || (shell == "cmd" && strings.HasPrefix(a, "/") && len(a) <= 3):
					return nil, false // -n, -TotalCount, -Tail...: não é a leitura inteira
				default:
					paths = append(paths, rest[i])
				}
			}
			if len(paths) != 1 || strings.ContainsAny(paths[0], "*?") {
				return nil, false
			}
			if cwd == "" && !isAbsAny(paths[0]) {
				return nil, false
			}
			steps = append(steps, readStep{path: join(cwd, paths[0])})
			reads++
		default:
			return nil, false
		}
	}
	return steps, reads > 0
}

func isAbsAny(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.HasPrefix(p, `\\`) || isWindowsPath(p)
}

// tryRemediateReadScript roda os passos pelo read_file. handled=false: o comando segue para o shell.
func tryRemediateReadScript(args ExecArgs) (string, bool) {
	if !autofixOn(identity(args.Caller, args.Conv)) {
		return "", false
	}
	target, err := transport.ResolveHost(args.Host)
	if err != nil || target.IsSSH {
		return "", false
	}
	script, shell := unwrapShell(args, target.IsLocal)
	if script == "" {
		return "", false
	}
	var join func(dir, p string) string
	cwd := args.Cwd
	if target.IsLocal {
		join = func(dir, p string) string {
			if dir == "" || filepath.IsAbs(p) || isWindowsPath(p) {
				return p
			}
			return filepath.Join(dir, p)
		}
	} else {
		join = func(dir, p string) string {
			if dir == "" || isAbsAny(p) {
				return p
			}
			return strings.TrimRight(dir, "/") + "/" + p
		}
		if cwd == "" {
			cwd = target.DefaultCwd
		} else if !isAbsAny(cwd) && target.DefaultCwd != "" {
			cwd = join(target.DefaultCwd, cwd)
		}
	}
	steps, ok := parseReadScript(script, shell, join, cwd)
	if !ok {
		return "", false
	}
	var b strings.Builder
	outlines := 0
	for _, s := range steps {
		if s.path == "" {
			b.WriteString(s.literal + "\n")
			continue
		}
		content, err := HandleReadFile(ReadFileArgs{Host: args.Host, Path: s.path})
		if err != nil {
			return "", false // o shell mostra o erro original
		}
		if strings.HasPrefix(content, GraphMarkOutline) {
			outlines++
		}
		b.WriteString(strings.TrimRight(content, "\r\n") + "\n")
	}
	head := "[FastExec Auto-Remediation] This command only reads files, so each file went through 'read_file' (no process)."
	if outlines > 0 {
		head += " Big source files came as an outline; repeat this same command for their full text, or read_file(path, start_line, end_line) for a part."
	}
	return head + "\n---\n" + strings.TrimRight(b.String(), "\n"), true
}
