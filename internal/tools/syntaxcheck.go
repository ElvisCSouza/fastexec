package tools

// syntaxcheck.go: camada 1 da validação do que a LLM escreve. Sintaxe EXATA (a do compilador), em milissegundos, sem
// projeto carregado e com o servidor de linguagem ainda frio. Só erros: warning, info e hint não existem aqui.
//
//	Go: go/parser da stdlib, no próprio processo.
//	C#: fx-roslyn (internal/lsp/roslyn.go), processo persistente com CSharpSyntaxTree.
//	Outras linguagens: sem camada 1 (a camada 2, o servidor de linguagem, responde por elas).

import (
	"encoding/json"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/lsp"
)

// syntaxIssue é um erro de sintaxe. id é o código do compilador ("CS1002") ou "" quando a linguagem não tem.
type syntaxIssue struct {
	id   string
	line int // 1-based
	col  int // 1-based
	msg  string
}

// syntaxState diz o que a lista significa.
type syntaxState int

const (
	syntaxNone    syntaxState = iota // linguagem sem camada 1: nada a dizer
	syntaxReady                      // a lista (talvez vazia) é a verdade sobre a sintaxe
	syntaxPending                    // C#: o helper está sendo preparado; sem resposta desta vez
	syntaxBroken                     // o helper não está disponível (sem SDK, falhou): sem resposta
)

// syntaxCheck valida o texto inteiro de um arquivo. O texto é o que vai para o disco, não um trecho.
func syntaxCheck(path, text string) ([]syntaxIssue, syntaxState) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goSyntax(path, text), syntaxReady
	case ".json":
		return jsonSyntax(text), syntaxReady
	case ".cs":
		issues, st, _ := lsp.RoslynSyntax(text)
		switch st {
		case lsp.RoslynReady:
			out := make([]syntaxIssue, 0, len(issues))
			for _, i := range issues {
				out = append(out, syntaxIssue{id: i.ID, line: i.Line, col: i.Col, msg: i.Msg})
			}
			return out, syntaxReady
		case lsp.RoslynBuilding:
			return nil, syntaxPending
		default:
			return nil, syntaxBroken
		}
	}
	return externalSyntax(path, text)
}

// syntaxLanguage diz se o arquivo tem camada 1 (para decidir, antes de pagar qualquer coisa, se vale validar).
func syntaxLanguage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".cs", ".json", ".js", ".mjs", ".cjs", ".zig", ".sh", ".bash", ".zsh", ".ksh", ".py", ".pyi":
		return true
	}
	return false
}

// jsonSyntax valida o texto com o parser da stdlib (determinístico, in-process). Devolve o primeiro erro, com a
// linha calculada a partir do offset que o encoding/json informa.
func jsonSyntax(text string) []syntaxIssue {
	var v any
	err := json.Unmarshal([]byte(text), &v)
	if err == nil {
		return nil
	}
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetLineCol(text, int(se.Offset))
		return []syntaxIssue{{line: line, col: col, msg: se.Error()}}
	}
	return []syntaxIssue{{line: 1, col: 1, msg: err.Error()}}
}

// offsetLineCol converte um offset em bytes para linha/coluna (1-based).
func offsetLineCol(text string, off int) (int, int) {
	if off > len(text) {
		off = len(text)
	}
	line, col := 1, 1
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// goSyntax usa o parser da stdlib. Só erros de sintaxe: o go/parser não faz análise de tipos, que é da camada 2.
func goSyntax(path, text string) []syntaxIssue {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filepath.Base(path), text, parser.AllErrors|parser.SkipObjectResolution)
	if err == nil {
		return nil
	}
	list, ok := err.(scanner.ErrorList)
	if !ok {
		return []syntaxIssue{{line: 1, col: 1, msg: err.Error()}}
	}
	list.Sort()
	out := make([]syntaxIssue, 0, len(list))
	for _, e := range list {
		out = append(out, syntaxIssue{line: e.Pos.Line, col: e.Pos.Column, msg: e.Msg})
	}
	return out
}

// newSyntaxIssues tira os erros que já existiam antes da edição (mesmo código e mesma mensagem: a linha pode ter
// andado, a mensagem não). Só entra o que a edição criou.
func newSyntaxIssues(now, before []syntaxIssue) []syntaxIssue {
	if len(before) == 0 {
		return now
	}
	seen := map[string]int{}
	for _, b := range before {
		seen[b.id+"|"+b.msg]++
	}
	var out []syntaxIssue
	for _, n := range now {
		k := n.id + "|" + n.msg
		if seen[k] > 0 {
			seen[k]--
			continue
		}
		out = append(out, n)
	}
	return out
}
