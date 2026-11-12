// edit_proof.go: a resposta do edit_file prova a edição. Sem isso o modelo relê o arquivo (read_file com pattern,
// grep -n, file/grep -c $'\r') só para conferir se a troca caiu na linha certa e se o CRLF sobreviveu — um turno
// inteiro a mais por edição (medido no ds-full5, tarefa T9: 4 turnos contra 3 do nativo).
package tools

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// editProofMaxLines é o teto de linhas citadas; acima disso só a contagem.
	editProofMaxLines = 5
	// editProofByteBudget é o teto do bloco extra da resposta, em caracteres.
	editProofByteBudget = 300
	// editProofQuoteMax é o tamanho da linha citada; encolhe quando há muitas trocas (editProofQuoteLen).
	editProofQuoteMax = 120
	// utf8BOM é a marca de ordem de bytes que o arquivo pode ter no começo, preservada pela edição.
	utf8BOM = "\uFEFF"
)

// editProof monta o bloco que segue a primeira frase: uma linha "L<n>: <texto>" por troca aplicada (a primeira
// ocorrência da posição onde o replacement caiu) e, no fim, o estilo de fim de linha e a codificação preservados.
// applied é a posição inicial de cada troca já ajustada ao conteúdo final, na ordem das ops.
func editProof(newContent string, applied []int, enc string) string {
	if len(applied) == 0 {
		return ""
	}
	shown := min(len(applied), editProofMaxLines)
	lines := editProofLineNumbers(newContent, applied[:shown])
	quote := editProofQuoteLen(shown, len(applied)-shown)

	var b strings.Builder
	for i := 0; i < shown; i++ {
		b.WriteString(fmt.Sprintf("\nL%d: %s", lines[i], clipAt(lineAt(newContent, applied[i]), quote)))
	}
	if more := len(applied) - shown; more > 0 {
		b.WriteString(fmt.Sprintf("\n(and %d more)", more))
	}
	b.WriteString("\n" + endingsKept(newContent, enc))
	return b.String()
}

// editProofLineNumbers devolve a linha (1-based) de cada posição, numa passada só sobre o conteúdo, mesmo quando as
// posições não estão em ordem (uma troca pode cair antes de outra já anotada).
func editProofLineNumbers(content string, offs []int) []int {
	order := make([]int, len(offs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return offs[order[a]] < offs[order[b]] })

	out := make([]int, len(offs))
	line, prev := 1, 0
	for _, i := range order {
		at := min(max(offs[i], prev), len(content))
		line += strings.Count(content[prev:at], "\n")
		prev = at
		out[i] = line
	}
	return out
}

// lineAt devolve a linha do conteúdo que contém a posição, sem o \r do CRLF.
func lineAt(content string, at int) string {
	at = min(max(at, 0), len(content))
	start := strings.LastIndexByte(content[:at], '\n') + 1
	end := strings.IndexByte(content[at:], '\n')
	if end < 0 {
		end = len(content)
	} else {
		end += at
	}
	return strings.Trim(content[start:end], "\r")
}

// editProofQuoteLen encolhe a citação quando há muitas trocas, para o bloco inteiro caber em editProofByteBudget:
// desconta o custo fixo de cada citação, o da linha "(and N more)" e o do aviso de fim de linha/codificação.
func editProofQuoteLen(shown, more int) int {
	if shown <= 1 {
		return editProofQuoteMax
	}
	const perLine = len("\nL9999: ")
	const ending = len("\nline endings kept: mixed; encoding kept: Windows-1252") // o aviso mais longo possível
	reserve := ending
	if more > 0 {
		reserve += len("\n(and 999 more)")
	}
	quote := (editProofByteBudget - reserve - perLine*shown) / shown
	return max(min(quote, editProofQuoteMax), 24)
}

// endingsKept diz o estilo de fim de linha do arquivo como ele ficou e a codificação que foi preservada. O modelo
// deixa de precisar de "file" ou "grep -c $'\r'" para saber se o CRLF sobreviveu.
func endingsKept(content, enc string) string {
	crlf := strings.Count(content, "\r\n")
	lf := strings.Count(content, "\n") - crlf
	style := "none"
	switch {
	case crlf > 0 && lf > 0:
		style = "mixed"
	case crlf > 0:
		style = "CRLF"
	case lf > 0:
		style = "LF"
	}
	note := "line endings kept: " + style
	switch {
	case enc != "":
		note += "; encoding kept: " + enc
	case strings.HasPrefix(content, utf8BOM):
		note += "; encoding kept: UTF-8 BOM"
	}
	return note
}
