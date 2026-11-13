// text_signature.go: o snapshot de API (Serilog.approved.txt e parentes) guarda a assinatura do membro renomeado em
// texto. O rename do compilador não chega lá, e deixar a linha para o modelo é deixar uma edição e as leituras que ela
// puxa. Quando a linha de texto contém a assinatura EXATA da declaração que acabou de ser renomeada, não há dúvida
// sobre qual membro ela descreve, e a troca é determinística: o servidor a faz.
//
// Conservador de propósito: só assinatura de método (tem parênteses) com tamanho que não se confunde com outra coisa,
// só arquivos de texto conhecidos, só quando o total de linhas casadas é pequeno. Qualquer dúvida deixa a linha
// para o aviso "only work left", que já a entrega pronta.
package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sigMinLen   = 24 // abaixo disso a "assinatura" pode ser outra coisa
	sigMaxLines = 6  // acima disso a correspondência é ambígua demais para aplicar sozinho
)

// normSpaces colapsa qualquer sequência de espaços em um.
func normSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// sigKey é a linha da declaração, sem o que vem depois do parêntese de fechamento (chave, ponto e vírgula, corpo), e
// com espaços normalizados. "" quando a declaração não serve de chave.
func sigKey(declLine string) string {
	k := normSpaces(declLine)
	if i := strings.LastIndex(k, ")"); i >= 0 {
		k = k[:i+1]
	} else {
		return ""
	}
	if len(k) < sigMinLen || !strings.Contains(k, "(") {
		return ""
	}
	return k
}

// sigMentionSites acha, nos arquivos de texto, as linhas que contêm a chave. Devolve caminho absoluto -> índices
// (0-based) das linhas.
func sigMentionSites(root, key string, budget time.Duration) map[string][]int {
	deadline := time.Now().Add(budget)
	out := map[string][]int{}
	total := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || time.Now().After(deadline) || total > sigMaxLines {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return filepath.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "obj", "node_modules", "vendor", ".vs":
				return filepath.SkipDir
			}
			return nil
		}
		if !textMentionExt[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		if info, e := d.Info(); e != nil || info.Size() > 2<<20 {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		for i, ln := range strings.Split(string(b), "\n") {
			if strings.Contains(normSpaces(ln), key) {
				out[p] = append(out[p], i)
				total++
			}
		}
		return nil
	})
	if total > sigMaxLines || time.Now().After(deadline) {
		return nil // ambíguo ou sem tempo: nada é aplicado
	}
	return out
}

// applySigMentions troca o nome antigo pelo novo nas linhas dadas, preservando fim de linha e BOM (trabalha sobre o
// texto cru, linha a linha). Devolve os arquivos que mudaram.
func applySigMentions(sites map[string][]int, name, newName string) (changed []string, lines []string, err error) {
	for p, idx := range sites {
		b, e := os.ReadFile(p)
		if e != nil {
			return changed, lines, e
		}
		parts := strings.Split(string(b), "\n")
		touched := false
		for _, i := range idx {
			if i >= len(parts) {
				continue
			}
			if n := renameWord(parts[i], name, newName); n != parts[i] {
				parts[i] = n
				touched = true
				lines = append(lines, fmt.Sprintf("%s:%d", p, i+1))
			}
		}
		if !touched {
			continue
		}
		captureUndo(p)
		info, _ := os.Stat(p)
		mode := os.FileMode(0o644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		if e := os.WriteFile(p, []byte(strings.Join(parts, "\n")), mode); e != nil {
			return changed, lines, e
		}
		changed = append(changed, p)
	}
	return changed, lines, nil
}

// textSignatures é o passo do rename: atualiza sozinho as linhas de texto que descrevem exatamente o membro
// renomeado, e diz quais foram.
func (a *autoEdit) textSignatures() {
	if a.delta.site || a.delta.kind != declRenamed {
		return
	}
	key := sigKey(declLineAt(a.oldText, a.delta.line))
	if key == "" {
		return
	}
	sites := sigMentionSites(a.root, key, 400*time.Millisecond)
	if len(sites) == 0 {
		return
	}
	changed, lines, err := applySigMentions(sites, a.delta.name, a.delta.newName)
	if err != nil || len(changed) == 0 {
		return
	}
	for _, f := range changed {
		noteWrite(a.host, f)
	}
	rel := make([]string, 0, len(lines))
	for _, l := range lines {
		if i := strings.LastIndex(l, ":"); i > 0 {
			rel = append(rel, relToRoot(a.root, l[:i])+l[i:])
		}
	}
	a.extra = append(a.extra, fmt.Sprintf("[lsp] also updated text that held this member's exact signature (compiler cannot rename text; undo covers it): %s", joinFirst(rel, 3)))
}
