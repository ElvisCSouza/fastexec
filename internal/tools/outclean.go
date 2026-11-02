package tools

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	NoResultText = "sem resultado (exit 1)"

	maxLineRunes  = 400
	outputCapSize = 64 * 1024
	outputHeadLen = 16 * 1024
	truncMarker   = "[truncado: "
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// noisePrefixes é a tabela extensível de prefixos de linhas de lixo de terminal (hints do git,
// banners de atualização, avisos de depreciação) que inflam a janela de contexto das LLMs sem agregar valor técnico.
var noisePrefixes = []string{
	"hint: ",
	"hint:",
	"npm notice ",
	"npm warn deprecated ",
	"[notice] A new release of pip",
	"[notice] To update, run:",
	"A new PowerShell prerelease",
	"A new release of PowerShell",
	"A new version of PowerShell",
	"Upgrade to the latest version by visiting",
}

func isTerminalNoise(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

func cleanOutput(s string) string {
	if s == "" {
		return s
	}
	s = ansiRegex.ReplaceAllString(s, "")

	rawLines := strings.Split(s, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, l := range rawLines {
		if !isTerminalNoise(l) {
			lines = append(lines, l)
		}
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		body := strings.TrimSuffix(lines[i], "\r")
		b.WriteString(capLine(body))
		if n := j - i; n > 1 {
			fmt.Fprintf(&b, " ×%d", n)
		}
		b.WriteString(lines[i][len(body):])
		if j < len(lines) {
			b.WriteByte('\n')
		}
		i = j
	}
	return b.String()
}

func capLine(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= maxLineRunes {
		return s
	}
	end := len(s)
	count := 0
	for i := range s {
		if count == maxLineRunes {
			end = i
			break
		}
		count++
	}
	return s[:end] + fmt.Sprintf("…(+%d chars)", n-maxLineRunes)
}

func capOutput(s string) string {
	if len(s) <= outputCapSize {
		return s
	}
	cut := outputHeadLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n%s%d bytes; só os primeiros 16 KB foram devolvidos]", s[:cut], truncMarker, len(s))
}

func IsNoResult(out string) bool {
	return out == NoResultText
}

func IsTruncated(out string) bool {
	return strings.Contains(out, "\n"+truncMarker)
}
