// callsite_rename.go: o modelo troca um nome de membro num ponto de uso (SelfLog.WriteLine -> SelfLog.Write) em vez de
// pedir o rename pela declaração. A intenção é a mesma, e o caminho exato (rename do compilador) já existe: aqui só
// se reconhece a intenção. Sem isso o modelo repete a troca arquivo por arquivo (20 edições, 158k tokens no CS4).
//
// Tudo aqui é conservador e silencioso: se o servidor não aceita, se o nome novo já existe no tipo dono ou se a edição
// tem qualquer coisa além da troca do identificador, nada acontece e a edição do modelo vale como foi pedida.
package tools

import (
	"context"
	"os"
	"strings"
	"unicode/utf8"

	"homelab/fastexec/internal/lsp"
)

// identTokens quebra uma linha em palavras (identificador) e o resto, preservando o texto.
func identTokens(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		j := i
		if isIdentStart(s[i]) {
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
		} else {
			j = i + 1
			for j < len(s) && !isIdentStart(s[j]) {
				j++
			}
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdentPart(c byte) bool  { return isIdentStart(c) || c >= '0' && c <= '9' }

// utf16Col devolve a coluna 1-based (unidades UTF-16) do byte off de line, que é o que o LSP usa.
func utf16Col(line string, off int) int {
	n := 0
	for _, r := range line[:off] {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	_ = utf8.RuneError
	return n + 1
}

// callSiteDelta reconhece a edição cuja única diferença é UM identificador trocado por outro, sempre depois de um
// ponto (acesso a membro), em uma ou mais linhas, com o mesmo par em todas. Devolve o delta ancorado no primeiro
// uso, no texto VELHO.
func callSiteDelta(oldText, newText string) (declDelta, bool) {
	if oldText == "" || newText == "" || oldText == newText {
		return declDelta{}, false
	}
	ol, nl := strings.Split(oldText, "\n"), strings.Split(newText, "\n")
	if len(ol) != len(nl) {
		return declDelta{}, false
	}
	var from, to string
	var d declDelta
	for i := range ol {
		if ol[i] == nl[i] {
			continue
		}
		ot, nt := identTokens(ol[i]), identTokens(nl[i])
		if len(ot) != len(nt) {
			return declDelta{}, false
		}
		off := 0
		hit := false
		for k := range ot {
			if ot[k] != nt[k] {
				if !isIdentStart(ot[k][0]) || !isIdentStart(nt[k][0]) {
					return declDelta{}, false
				}
				// acesso a membro: o token anterior termina em "."
				if k == 0 || !strings.HasSuffix(ot[k-1], ".") {
					return declDelta{}, false
				}
				if from == "" {
					from, to = ot[k], nt[k]
				} else if from != ot[k] || to != nt[k] {
					return declDelta{}, false
				}
				if !hit && d.line == 0 {
					qual := ""
					if k >= 2 {
						qual = ot[k-2]
					}
					d = declDelta{kind: declRenamed, name: ot[k], newName: nt[k], line: i + 1, endLine: i + 1,
						col: utf16Col(ol[i], off), qual: qual, site: true}
				}
				hit = true
			}
			off += len(ot[k])
		}
		if !hit {
			return declDelta{}, false
		}
	}
	if d.line == 0 || d.name == d.newName || !maskProof(oldText, newText, d.name, d.newName) {
		return declDelta{}, false
	}
	return d, true
}

// declaresName: algum dos arquivos onde o símbolo é declarado já tem o nome novo como palavra inteira. Aí a troca do
// modelo pode ser a escolha de OUTRO membro (Log.Info -> Log.Debug), e renomear a declaração seria destruir o outro
// caminho. Sem resposta do servidor também não se arrisca.
func (a *autoEdit) declaresName(ctx context.Context, pos lsp.Position) bool {
	locs, err := a.client.Definition(ctx, a.abs, pos)
	if err != nil || len(locs) == 0 {
		return true
	}
	for _, l := range locs {
		p := lsp.URItoPath(l.URI)
		if p == "" {
			return true
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return true
		}
		if s := string(b); maskWord(s, a.delta.newName) != s {
			return true
		}
	}
	return false
}
