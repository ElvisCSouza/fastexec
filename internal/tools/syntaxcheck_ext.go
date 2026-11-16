package tools

// syntaxcheck_ext.go: camada 1 por toolchain, BEST-EFFORT. Se o binário da linguagem existe, valida a sintaxe com
// ele; se não existe (ou falha/tempo esgotado), devolve syntaxNone — silêncio, nunca bloqueia nem mente. Sem
// servidor de linguagem e sem modelo. Cobre o que o parser in-process (Go/JSON) e o helper (C#) não cobrem.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var extToolCache sync.Map // nome -> caminho utilizável ("" = não encontrado ou não funciona)

// extProbe valida que o binário FUNCIONA (não só existe no PATH). O python.exe da Microsoft Store é um stub que
// "existe" mas falha ao rodar; sem o probe, todo arquivo .py ganharia um erro falso.
var extProbe = map[string][]string{
	"node":    {"--version"},
	"zig":     {"version"},
	"bash":    {"--version"},
	"python":  {"--version"},
	"python3": {"--version"},
	"tsc":     {"--version"},
}

func extToolPath(name string) string {
	if v, ok := extToolCache.Load(name); ok {
		return v.(string)
	}
	p := ""
	if found, err := exec.LookPath(name); err == nil {
		usable := true
		if args, ok := extProbe[name]; ok {
			out, ran := extRun(found, args, "")
			low := strings.ToLower(out)
			if !ran || strings.Contains(low, "not found") || strings.Contains(low, "not recognized") {
				usable = false
			}
		}
		if usable {
			p = found
		}
	}
	extToolCache.Store(name, p)
	return p
}

// externalSyntax escolhe o validador pela extensão. Sem binário => syntaxNone (silêncio).
func externalSyntax(path, text string) ([]syntaxIssue, syntaxState) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return fileChecker("node", []string{"--check"}, path, text)
	case ".zig":
		return fileChecker("zig", []string{"ast-check"}, path, text)
	case ".sh", ".bash", ".zsh", ".ksh":
		return fileChecker("bash", []string{"-n"}, path, text)
	case ".py", ".pyi":
		return stdinChecker([]string{"python", "python3"}, []string{"-c", "import sys,ast; ast.parse(sys.stdin.read())"}, text)
	case ".ts", ".tsx", ".mts", ".cts":
		return tsChecker(path, text)
	}
	return nil, syntaxNone
}

// extReTsc casa a linha do compilador: "arq.ts(1,19): error TS1109: Expression expected."
var extReTsc = regexp.MustCompile(`\((\d+),(\d+)\): error TS(\d+): (.*)`)

// tsChecker valida TS/TSX com o próprio compilador (tsc --noEmit), mas só reporta diagnósticos SINTÁTICOS. Erro de
// tipo (TS2xxx), de módulo (TS2307) e os contextuais (TS13xx, top-level await; TS12xx de opção) ficam de fora — são
// camada 2, e num arquivo solto viram ruído. Sem tsc no PATH => silêncio (best-effort).
func tsChecker(path, text string) ([]syntaxIssue, syntaxState) {
	exe := extToolPath("tsc")
	if exe == "" {
		return nil, syntaxNone
	}
	f, err := os.CreateTemp("", "fx-syntax-*"+filepath.Ext(path))
	if err != nil {
		return nil, syntaxNone
	}
	name := f.Name()
	_, _ = f.WriteString(text)
	_ = f.Close()
	defer os.Remove(name)
	args := []string{"--noEmit", "--skipLibCheck"}
	if strings.EqualFold(filepath.Ext(path), ".tsx") {
		args = append(args, "--jsx", "preserve")
	}
	out, ok := extRun(exe, append(args, name), "")
	if !ok {
		return nil, syntaxNone
	}
	if iss := tsFirstSyntax(out); iss != nil {
		return []syntaxIssue{*iss}, syntaxReady
	}
	return nil, syntaxReady
}

// tsFirstSyntax devolve o primeiro diagnóstico sintático da saída do tsc, ou nil.
func tsFirstSyntax(out string) *syntaxIssue {
	for _, l := range strings.Split(out, "\n") {
		m := extReTsc.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		code, _ := strconv.Atoi(m[3])
		if !tsSyntaxCode(code) {
			continue
		}
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return &syntaxIssue{id: "TS" + m[3], line: line, col: col, msg: oneLine(m[4])}
	}
	return nil
}

// tsSyntaxCode diz se o código do tsc é de sintaxe. O parser emite 1xxx (exceto 12xx/13xx, que dependem de opções ou
// de tipos) e a gramática de JSX em 17xxx.
func tsSyntaxCode(code int) bool {
	if code >= 17000 && code < 18000 {
		return true
	}
	if code >= 1000 && code < 1200 {
		return true
	}
	switch code {
	case 1200, 1206: // parser em 12xx: fim de linha antes de '=>', decorator em contexto inválido
		return true
	}
	return false
}

func fileChecker(tool string, flags []string, path, text string) ([]syntaxIssue, syntaxState) {
	exe := extToolPath(tool)
	if exe == "" {
		return nil, syntaxNone
	}
	f, err := os.CreateTemp("", "fx-syntax-*"+filepath.Ext(path))
	if err != nil {
		return nil, syntaxNone
	}
	name := f.Name()
	_, _ = f.WriteString(text)
	_ = f.Close()
	defer os.Remove(name)
	out, ok := extRun(exe, append(flags, name), "")
	if !ok {
		return nil, syntaxNone
	}
	if out == "" {
		return nil, syntaxReady
	}
	return []syntaxIssue{{line: extLine(out, name), col: 1, msg: extMsg(out, name)}}, syntaxReady
}

func stdinChecker(tools []string, args []string, text string) ([]syntaxIssue, syntaxState) {
	for _, t := range tools {
		if exe := extToolPath(t); exe != "" {
			out, ok := extRun(exe, args, text)
			if !ok {
				return nil, syntaxNone
			}
			if out == "" {
				return nil, syntaxReady
			}
			return []syntaxIssue{{line: extLine(out, ""), col: 1, msg: extMsg(out, "")}}, syntaxReady
		}
	}
	return nil, syntaxNone
}

// runTool roda o validador com orçamento curto. Devolve (saída, ok); ok=false é falha de execução/tempo (silêncio).
func extRun(exe string, args []string, stdin string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", false
	}
	if err == nil {
		return "", true
	}
	return buf.String(), true
}

var (
	extReLineCol = regexp.MustCompile(":(\\d+):(\\d+)")
	extReLine    = regexp.MustCompile(":(\\d+)")
	extReWordLn  = regexp.MustCompile("(?i)line (\\d+)")
)

func extLine(out, name string) int {
	if name != "" {
		if m := regexp.MustCompile(regexp.QuoteMeta(name) + ":([0-9]+)").FindStringSubmatch(out); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	for _, re := range []*regexp.Regexp{extReLineCol, extReLine, extReWordLn} {
		if m := re.FindStringSubmatch(out); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 1
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func extMsg(out, name string) string {
	if name != "" {
		out = strings.ReplaceAll(out, name, "")
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		if strings.Contains(low, "error") {
			return oneLine(l)
		}
	}
	return oneLine(strings.TrimSpace(out))
}
