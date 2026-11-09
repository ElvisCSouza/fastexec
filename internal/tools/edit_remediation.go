package tools

import (
	"fmt"
	"strings"

	"homelab/fastexec/internal/graph"
)

type lineSpan struct {
	startByte int
	endByte   int
	fullEnd   int
}

func computeLineSpans(content string) []lineSpan {
	var spans []lineSpan
	start := 0
	n := len(content)
	for i := 0; i < n; i++ {
		if content[i] == '\n' {
			end := i
			if end > start && content[end-1] == '\r' {
				end--
			}
			spans = append(spans, lineSpan{
				startByte: start,
				endByte:   end,
				fullEnd:   i + 1,
			})
			start = i + 1
		}
	}
	if start < n {
		end := n
		if end > start && content[end-1] == '\r' {
			end--
		}
		spans = append(spans, lineSpan{
			startByte: start,
			endByte:   end,
			fullEnd:   n,
		})
	}
	return spans
}

func stripSpaces(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\r' && r != '\n' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchRemediation tenta localizar um target no arquivo que falhou por divergência
// de whitespace/indentação ou por descompasso de corpo de símbolo existente na AST.
func matchRemediation(path, content, target, replacement string) (string, string, bool) {
	if exact, desc, ok := matchFuzzyWhitespace(content, target); ok {
		return exact, desc, true
	}
	if exact, desc, ok := matchSymbolAST(path, content, target, replacement); ok {
		return exact, desc, true
	}
	return "", "", false
}

func matchFuzzyWhitespace(content, target string) (string, string, bool) {
	if target == "" || len(content) > 512*1024 {
		return "", "", false
	}
	spans := computeLineSpans(content)
	if len(spans) == 0 {
		return "", "", false
	}

	rawTargetLines := splitLines(target)
	for len(rawTargetLines) > 0 && strings.TrimSpace(rawTargetLines[len(rawTargetLines)-1]) == "" {
		rawTargetLines = rawTargetLines[:len(rawTargetLines)-1]
	}
	if len(rawTargetLines) == 0 {
		return "", "", false
	}

	// Alvo de uma linha só
	if len(rawTargetLines) == 1 {
		want := stripSpaces(rawTargetLines[0])
		if want == "" {
			return "", "", false
		}
		matchCount := 0
		matchIdx := -1
		for i, sp := range spans {
			lineText := stripSpaces(content[sp.startByte:sp.endByte])
			if lineText == want {
				matchCount++
				matchIdx = i
			}
		}
		if matchCount == 1 && matchIdx >= 0 {
			sp := spans[matchIdx]
			exact := content[sp.startByte:sp.endByte]
			if strings.HasSuffix(target, "\n") {
				exact = content[sp.startByte:sp.fullEnd]
			}
			desc := fmt.Sprintf("line %d (whitespace divergence)", matchIdx+1)
			return exact, desc, true
		}
		return "", "", false
	}

	// Alvo multilinha
	tb := make([]string, len(rawTargetLines))
	for i, l := range rawTargetLines {
		tb[i] = stripSpaces(l)
	}

	matchCount := 0
	bestAt := -1
	for at := 0; at+len(tb) <= len(spans); at++ {
		allMatch := true
		for j := range tb {
			lineText := stripSpaces(content[spans[at+j].startByte:spans[at+j].endByte])
			if lineText != tb[j] {
				allMatch = false
				break
			}
		}
		if allMatch {
			matchCount++
			bestAt = at
		}
	}

	if matchCount == 1 && bestAt >= 0 {
		startByte := spans[bestAt].startByte
		endByte := spans[bestAt+len(tb)-1].endByte
		if strings.HasSuffix(target, "\n") {
			endByte = spans[bestAt+len(tb)-1].fullEnd
		}
		exact := content[startByte:endByte]
		desc := fmt.Sprintf("lines %d-%d (indentation/whitespace divergence)", bestAt+1, bestAt+len(tb))
		return exact, desc, true
	}

	return "", "", false
}

func matchSymbolAST(path, content, target, replacement string) (string, string, bool) {
	if path == "" || target == "" || replacement == "" || len(content) > 512*1024 {
		return "", "", false
	}

	// Segurança: só aplica se o target e o replacement parecem blocos/corpos de função/classe/tipo
	targetHasBlock := strings.Contains(target, "\n") || strings.Contains(target, "{") || strings.Contains(target, ":")
	replHasBlock := strings.Contains(replacement, "\n") || strings.Contains(replacement, "{") || strings.Contains(replacement, ":")
	if !targetHasBlock || !replHasBlock {
		return "", "", false
	}

	decls, _, ok := graph.ParseFile(path, []byte(content))
	if !ok || len(decls) == 0 {
		return "", "", false
	}

	firstLine := ""
	for _, l := range splitLines(target) {
		t := strings.TrimSpace(l)
		if t != "" {
			firstLine = t
			break
		}
	}
	if firstLine == "" {
		return "", "", false
	}

	// Localiza qual símbolo é referenciado
	var candidate *int
	for i := range decls {
		d := &decls[i]
		if d.Name == "" || d.EndLine <= d.Line {
			continue
		}
		// O nome do símbolo deve estar na primeira linha do target e no replacement
		if !strings.Contains(firstLine, d.Name) || !strings.Contains(replacement, d.Name) {
			continue
		}
		// A primeira linha deve conter a assinatura ou o nome do símbolo com delimitadores de declaração
		sigNorm := collapseSpaces(d.Sig)
		firstNorm := collapseSpaces(firstLine)
		matchesSig := sigNorm != "" && (strings.HasPrefix(firstNorm, sigNorm) || strings.Contains(firstNorm, sigNorm))
		matchesDecl := strings.Contains(firstLine, "func ") || strings.Contains(firstLine, "def ") ||
			strings.Contains(firstLine, "class ") || strings.Contains(firstLine, "type ") ||
			strings.Contains(firstLine, "fn ") || strings.Contains(firstLine, "struct ")

		if matchesSig || matchesDecl {
			if candidate != nil {
				// Símbolo ambíguo no mesmo arquivo (sobrecarga ou mesmo nome): aborta com segurança
				return "", "", false
			}
			idx := i
			candidate = &idx
		}
	}

	if candidate == nil {
		return "", "", false
	}

	d := decls[*candidate]
	spans := computeLineSpans(content)
	if d.Line < 1 || d.EndLine > len(spans) || d.Line > d.EndLine {
		return "", "", false
	}

	startByte := spans[d.Line-1].startByte
	endByte := spans[d.EndLine-1].fullEnd
	exact := content[startByte:endByte]
	desc := fmt.Sprintf("symbol '%s' (%s, lines %d-%d via code_graph AST)", d.Name, d.Kind, d.Line, d.EndLine)
	return exact, desc, true
}
