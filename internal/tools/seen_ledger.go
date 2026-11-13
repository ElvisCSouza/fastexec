// seen_ledger.go: o que o MODELO viu (arquivos que abriu, buscas que rodou), por canal/conversa.
//
// Serve a uma regra só: depois de um rename automático, a resposta diz ao modelo apenas o que muda a conduta dele.
// Um arquivo que ele nunca abriu e que o rename tocou não precisa ser mencionado (mencionar o induz a lê-lo, e a
// busca dele depois vai dar zero de qualquer jeito); um arquivo que ele abriu precisa do aviso de "releia". E a busca
// que ele já fez é refeita e respondida na mesma moeda em que ele ia conferir.
//
// Como read_count.go, é só leitura de conduta: nunca bloqueia nem altera dado, tem teto, e o que o servidor não
// enxerga (um cat por exec, outro canal) só faz o aviso aparecer a mais, nunca a menos do que o modelo precisa — o
// edit_file exige texto exato e falha alto com cópia velha.
package tools

import (
	"strings"
	"sync"
)

type grepNote struct {
	label        string
	lines, files int
}

var (
	seenMu    sync.Mutex
	seenFiles = map[string]bool{}
	seenGreps = map[string][]grepNote{}
)

// noteSeen registra que ident abriu (leu ou editou) host:path.
func noteSeen(ident, host, path string) {
	if ident == "" || path == "" {
		return
	}
	key := ident + "|" + sessionKey(host, path)
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seenFiles) > 4000 {
		seenFiles = map[string]bool{}
	}
	seenFiles[key] = true
}

// seenBy diz se ident abriu host:path.
func seenBy(ident, host, path string) bool {
	key := ident + "|" + sessionKey(host, path)
	seenMu.Lock()
	defer seenMu.Unlock()
	return seenFiles[key]
}

// noteGrep guarda uma busca de texto de ident com o que ela devolveu (antes de qualquer corte).
func noteGrep(ident, label, stdout string) {
	if ident == "" || strings.TrimSpace(stdout) == "" {
		return
	}
	files := map[string]bool{}
	lines := 0
	for _, ln := range strings.Split(stdout, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		lines++
		// "caminho:linha:texto" (grep -n, findstr /n); o caminho do Windows pode ter "C:" no começo.
		rest := ln
		prefix := 0
		if len(rest) > 2 && rest[1] == ':' && (rest[2] == '\\' || rest[2] == '/') {
			prefix = 2
		}
		if i := strings.Index(rest[prefix:], ":"); i > 0 {
			files[rest[:prefix+i]] = true
		}
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seenGreps) > 500 {
		seenGreps = map[string][]grepNote{}
	}
	g := append(seenGreps[ident], grepNote{label: normGrep(label), lines: lines, files: len(files)})
	if len(g) > 8 {
		g = g[len(g)-8:]
	}
	seenGreps[ident] = g
}

// normGrep tira as barras de escape: grep 'SelfLog\.WriteLine' e findstr /c:"SelfLog.WriteLine" são a mesma busca.
func normGrep(s string) string { return strings.ToLower(strings.ReplaceAll(s, `\.`, ".")) }

// grepFor devolve a última busca de ident cujo texto traz needle.
func grepFor(ident, needle string) (grepNote, bool) {
	if ident == "" {
		return grepNote{}, false
	}
	n := strings.ToLower(needle)
	seenMu.Lock()
	defer seenMu.Unlock()
	g := seenGreps[ident]
	for i := len(g) - 1; i >= 0; i-- {
		if g[i].lines > 0 && strings.Contains(g[i].label, n) {
			return g[i], true
		}
	}
	return grepNote{}, false
}
