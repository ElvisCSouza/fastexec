package tools

import (
	"fmt"
	"os"
	"strings"
)

// Reescrita inteira de arquivo: a telemetria mostrou write_file sobrescrevendo arquivos de código que já existiam
// para mudar poucas linhas (270 vezes e 1,2 MB em 3 dias). É o token mais caro (escrito pelo modelo) e ainda fica no
// contexto. Quando a sobrescrita local mantém a maior parte das linhas, a resposta diz quanto foi enviado e quanto
// um edit_file teria enviado. Só avisa: o arquivo é gravado igual. RewriteMark alimenta a telemetria (graph='rewrite').

const (
	RewriteMark       = "[FastExec Rewrite]"
	rewriteMinBytes   = 2048 // arquivo pequeno: reescrever é tão barato quanto editar
	rewriteMinKept    = 0.6  // fração das linhas novas que já existiam
	rewriteMinSaveKB  = 1    // economia mínima para valer o aviso
	rewriteMaxOldSize = 4 << 20
)

// oldContentForRewrite lê o conteúdo atual de um arquivo local antes da sobrescrita ("" se não existe ou é grande).
func oldContentForRewrite(path string) string {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 || st.Size() > rewriteMaxOldSize {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// rewriteHint compara o conteúdo antigo e o novo linha a linha (multiconjunto: robusto a blocos movidos).
// Estimativa do edit_file: as linhas novas que não existiam, mais uma linha de contexto antes e depois de cada
// bloco alterado, contadas duas vezes (target e replacement) de forma conservadora.
func rewriteHint(oldText, newText string) string {
	if oldText == "" || len(newText) < rewriteMinBytes {
		return ""
	}
	norm := func(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }
	oldL, newL := norm(oldText), norm(newText)
	have := make(map[string]int, len(oldL))
	for _, l := range oldL {
		have[l]++
	}
	kept, changedBytes, blocks := 0, 0, 0
	inBlock := false
	for _, l := range newL {
		if have[l] > 0 {
			have[l]--
			kept++
			inBlock = false
			continue
		}
		changedBytes += len(l) + 1
		if !inBlock {
			blocks++
			inBlock = true
		}
	}
	if len(newL) == 0 || float64(kept)/float64(len(newL)) < rewriteMinKept {
		return ""
	}
	avgLine := len(newText) / len(newL)
	editBytes := 2*changedBytes + blocks*4*avgLine + 64*blocks
	saveKB := (len(newText) - editBytes) / 1024 // inteiro: é o que a telemetria lê em "(avoidable N KB)"
	if saveKB < rewriteMinSaveKB {
		return ""
	}
	kb := func(n int) float64 { return float64(n) / 1024 }
	return fmt.Sprintf("%s This overwrite kept %d of %d lines unchanged: you sent %.1f KB to change about %d line(s) in %d place(s); edit_file would have sent about %.1f KB (avoidable %d KB). "+
		"Next time use edit_file with edits=[{target, replacement}, ...]. The file was written as sent.",
		RewriteMark, kept, len(newL), kb(len(newText)), len(newL)-kept, blocks, kb(editBytes), saveKB)
}
