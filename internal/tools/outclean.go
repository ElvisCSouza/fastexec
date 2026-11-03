package tools

import (
	"bytes"
	"encoding/json"
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

var (
	ansiRegex       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	multiSpaceRegex = regexp.MustCompile(` {3,}`)
)

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

var codePrefixes = []string{
	"diff --git ", "index ", "--- a/", "+++ b/", "@@ ",
	"def ", "class ", "import ", "from ", "package ", "func ",
	"pub fn ", "fn ", "public ", "private ", "interface ",
	"type ", "const ", "let ", "var ", "return ",
}

var tableHeaderKeywords = []string{
	"PID", "CONTAINER ID", "FILESYSTEM", "COMMAND", "STATUS", "IMAGE", "PORTS",
	"ACTIVE", "SUB", "LOAD", "SIZE", "USED", "AVAIL", "PROTO", "LOCAL ADDRESS",
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

// resolveCarriageReturns resolve animações de terminal baseadas em \r sem \n
// (ex: curl/wget/npm barras de progresso sobrescrevendo a mesma linha).
func resolveCarriageReturns(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	hasLoneCR := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' && (i+1 == len(s) || s[i+1] != '\n') {
			hasLoneCR = true
			break
		}
	}
	if !hasLoneCR {
		return s
	}

	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.Grow(len(s))
	for idx, line := range lines {
		if strings.Contains(line, "\r") {
			parts := strings.Split(line, "\r")
			last := ""
			for _, p := range parts {
				if p != "" {
					last = p
				}
			}
			b.WriteString(last)
		} else {
			b.WriteString(line)
		}
		if idx < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// tryCompactJSON detecta se a saída é um JSON válido e aplica minificação determinística.
func tryCompactJSON(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 {
		return s, false
	}
	first, last := trimmed[0], trimmed[len(trimmed)-1]
	if (first != '{' && first != '[') || (last != '}' && last != ']') {
		return s, false
	}
	if !json.Valid([]byte(trimmed)) {
		return s, false
	}
	var b bytes.Buffer
	b.Grow(len(trimmed))
	if err := json.Compact(&b, []byte(trimmed)); err != nil {
		return s, false
	}
	return b.String(), true
}

func isCodeOrDiff(lines []string) bool {
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		for _, cp := range codePrefixes {
			if strings.HasPrefix(trimmed, cp) {
				return true
			}
		}
	}
	return false
}

func isTableOutput(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	for i := 0; i < len(lines) && i < 3; i++ {
		upper := strings.ToUpper(lines[i])
		matchCount := 0
		for _, kw := range tableHeaderKeywords {
			if strings.Contains(upper, kw) {
				matchCount++
			}
		}
		if matchCount >= 2 {
			return true
		}
	}
	return false
}

func compressTableLine(line string) string {
	indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
	if indentLen == len(line) {
		return line
	}
	indent := line[:indentLen]
	body := line[indentLen:]
	body = multiSpaceRegex.ReplaceAllString(body, "  ")
	return indent + body
}

func cleanOutput(s string) string {
	if s == "" {
		return s
	}

	// 1. Resolve sobrescritas de carriage return (\r de barras de progresso sem \n)
	s = resolveCarriageReturns(s)

	// 2. Remove sequências ANSI
	s = ansiRegex.ReplaceAllString(s, "")

	// 3. Se for JSON válido, aplica compactação nativa direta
	if compacted, ok := tryCompactJSON(s); ok {
		return capOutput(compacted)
	}

	// 4. Divisão de linhas e filtragem de ruído de terminal
	rawLines := strings.Split(s, "\n")
	filtered := make([]string, 0, len(rawLines))
	for _, l := range rawLines {
		if !isTerminalNoise(l) {
			filtered = append(filtered, l)
		}
	}

	// 5. Detecção consciente do conteúdo (Código vs Tabela vs Texto comum)
	isCode := isCodeOrDiff(filtered)
	isTable := !isCode && isTableOutput(filtered)

	lines := make([]string, len(filtered))
	for idx, l := range filtered {
		if isTable {
			lines[idx] = compressTableLine(l)
		} else {
			lines[idx] = l
		}
	}

	// 6. Colapso de repetições consecutivas idênticas e teto por linha (preservando CRLF)
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
