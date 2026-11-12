// doc.go: arquivos de documentação (Markdown). Eles NÃO são uma linguagem do codescan (LangFor devolve nil):
// quem os indexa no grafo é o parser próprio de internal/graph, e a listagem de declarações por árvore continua
// ignorando-os. Este predicado existe para o grafo decidir o que é documento sem confundir com código.
package codescan

import (
	"path/filepath"
	"strings"
)

// docExts: extensões tratadas como documentação.
var docExts = map[string]bool{".md": true, ".markdown": true}

// IsDoc diz se o caminho é um arquivo de documentação Markdown.
func IsDoc(path string) bool {
	return docExts[strings.ToLower(filepath.Ext(path))]
}

// DocSupported lista as extensões de documentação aceitas (mensagens de erro).
const DocSupported = ".md/.markdown"
