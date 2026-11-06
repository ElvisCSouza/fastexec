// line_trim.go: corte de linhas gigantes (bundle minificado, dado sem quebra de linha) na saída do exec.
// Linha acima de lineLimit caracteres vira começo + "… [N chars omitted] …" + fim; numa busca (grep, rg, findstr,
// git grep), se o termo buscado está no trecho omitido, devolve o termo com contexto dos dois lados. Assim o modelo
// vê a ocorrência sem recorrer a scripts com indexOf/substring.
package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	lineLimit = 500 // linha até aqui passa intacta (logs e código legíveis)
	headKeep  = 200
	tailKeep  = 100
	ctxKeep   = 150 // contexto de cada lado do termo buscado
)

// capLineAround corta a linha se passar de lineLimit caracteres, preservando o termo buscado quando houver.
func capLineAround(s, needle string) string {
	r := []rune(s)
	n := len(r)
	if n <= lineLimit {
		return s
	}
	if needle != "" {
		nr := []rune(needle)
		if pos := indexFold(r, nr); pos >= 0 && pos+len(nr) > headKeep && pos < n-tailKeep {
			start := max(0, pos-ctxKeep)
			end := min(n, pos+len(nr)+ctxKeep)
			var b strings.Builder
			if start > 0 {
				fmt.Fprintf(&b, "… [%d chars omitted] ", start)
			}
			b.WriteString(string(r[start:end]))
			if end < n {
				fmt.Fprintf(&b, " [%d chars omitted] …", n-end)
			}
			return b.String()
		}
	}
	return string(r[:headKeep]) + fmt.Sprintf(" … [%d chars omitted] … ", n-headKeep-tailKeep) + string(r[n-tailKeep:])
}

// indexFold acha needle em s sem diferenciar maiúsculas (posição em runas; -1 se não houver).
func indexFold(s, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(s) {
		return -1
	}
	first := unicode.ToLower(needle[0])
outer:
	for i := 0; i+len(needle) <= len(s); i++ {
		if unicode.ToLower(s[i]) != first {
			continue
		}
		for j := 1; j < len(needle); j++ {
			if unicode.ToLower(s[i+j]) != unicode.ToLower(needle[j]) {
				continue outer
			}
		}
		return i
	}
	return -1
}

// searchNeedle extrai do comando o termo literal buscado (grep, git grep, rg, findstr); "" se não for busca.
func searchNeedle(label string) string {
	for _, seg := range shellSegments(label) {
		tokens := shellWords(seg)
		if len(tokens) == 0 {
			continue
		}
		prog := strings.ToLower(strings.TrimSuffix(filepath.Base(tokens[0]), ".exe"))
		pat := ""
		switch {
		case isGrepProgram(tokens[0]):
			g, _ := parseGrepArgs(tokens)
			pat = g.pattern
		case prog == "git" && len(tokens) > 1 && tokens[1] == "grep":
			g, _ := parseGrepArgs(append([]string{"grep"}, tokens[2:]...))
			pat = g.pattern
		case prog == "rg":
			pat = rgPattern(tokens[1:])
		case prog == "findstr":
			for _, a := range tokens[1:] {
				if v, ok := strings.CutPrefix(strings.ToLower(a), "/c:"); ok {
					pat = a[len(a)-len(v):]
					break
				}
				if !strings.HasPrefix(a, "/") {
					pat = a
					break
				}
			}
		}
		if n := literalNeedle(pat); n != "" {
			return n
		}
	}
	return ""
}

func rgPattern(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--regexp":
			if i+1 < len(args) {
				return args[i+1]
			}
		case rgFlagsWithValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// literalNeedle transforma o padrão num termo literal: sem metacaractere de regex, o próprio padrão (sem escapes);
// com metacaractere, o trecho de palavra mais longo (3+ caracteres), que é o que aparece no texto achado.
func literalNeedle(p string) string {
	var b strings.Builder
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) && !unicode.IsLetter(rs[i+1]) && !unicode.IsDigit(rs[i+1]) {
			i++
		}
		b.WriteRune(rs[i])
	}
	s := b.String()
	if !strings.ContainsAny(s, `.*+?[](){}|^$\`) {
		if len([]rune(s)) >= 2 {
			return s
		}
		return ""
	}
	best, cur := "", []rune{}
	flush := func() {
		if len(cur) > len([]rune(best)) {
			best = string(cur)
		}
		cur = cur[:0]
	}
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' {
			cur = append(cur, c)
		} else {
			flush()
		}
	}
	flush()
	if len([]rune(best)) < 3 {
		return ""
	}
	return best
}
