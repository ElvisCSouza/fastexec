// basis.go: a linha "basis:" que diz, em cada resposta do code_graph, de onde veio o número: provider, grau de
// resolução, fallback, frescor, completude e cache. Sem parâmetro novo: sai do texto da resposta e do estado
// colhido antes da chamada, e a telemetria lê a mesma linha (internal/mcp/graph_mark.go).
package tools

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

var (
	basisExactRe = regexp.MustCompile(`^=== \w+ of '[^']*' \(exact, ([a-z0-9._-]+)\) ===`)
	basisTotalRe = regexp.MustCompile(`(?m)^Total: (\d+) references in (\d+) files`)
	basisMoreRe  = regexp.MustCompile(`(?m)^\.\.\. (\d+) more`)
)

// graphCall é o que se sabe do pedido ANTES de executá-lo: o estado do cache de servidores (quente ou frio) e
// se a indexação de fundo ainda está em curso. Colher antes importa: depois da chamada o servidor já existe.
type graphCall struct {
	action   string
	depth    int
	lang     string
	warm     bool
	building bool
}

// newGraphCall colhe o contexto do pedido. A linguagem vem da declaração do símbolo (a primeira, quando há
// várias); sem declaração conhecida fica "unknown".
func newGraphCall(root, action, symbol string, depth int) graphCall {
	g := graphCall{action: action, lang: "unknown"}
	if action == "callers" {
		g.depth = depth
		if g.depth < 1 {
			g.depth = 1
		}
	}
	if defs := graph.DefSites(root, symbol); len(defs) > 0 {
		if l, ok := lsp.LanguageForPath(defs[0].File); ok {
			g.lang = l.ID
		} else if ext := strings.TrimPrefix(path.Ext(defs[0].File), "."); ext != "" {
			g.lang = ext // sem servidor para a linguagem: a resposta é por nome, rotulada pela extensão
		}
	}
	for _, s := range lsp.Manager().Servers() {
		if s.Root == root && s.Language == g.lang {
			g.warm = true
		}
	}
	_, _, g.building = graph.Building(root)
	return g
}

// withBasis insere a linha basis logo após o cabeçalho (a telemetria e a decisão exato/por nome leem a primeira
// linha). Resposta vazia volta como veio.
func withBasis(out string, g graphCall) string {
	if out == "" {
		return out
	}
	first, rest, _ := strings.Cut(out, "\n")
	return first + "\n" + g.line(out) + rest
}

// line monta "basis: k=v ..." a partir do texto da resposta. "exact=yes" só quando o grau é semântico, não há
// fallback, a revisão é conhecida (índice atual) e nada foi cortado: é a única combinação que o contrato chama
// de exato.
func (g graphCall) line(out string) string {
	provider, grade, fallback, cache := "index", "name", "none", "n/a"
	if m := basisExactRe.FindStringSubmatch(out); m != nil {
		provider, grade = m[1], "semantic"
		if g.warm {
			cache = "warm"
		} else {
			cache = "cold"
		}
	} else {
		switch {
		case strings.Contains(out, "\nlsp unavailable ("):
			fallback = "lsp_unavailable"
		case g.action == "callers" && g.depth > 1:
			fallback = "depth"
		default:
			fallback = "no_server"
		}
	}
	omitted := 0
	if m := basisMoreRe.FindStringSubmatch(out); m != nil {
		fmt.Sscanf(m[1], "%d", &omitted)
	}
	// Não estar construindo não prova que o índice por nome está na revisão atual nem que cobriu tudo.
	// Só o provider semântico ao vivo pode afirmar fresh/complete neste contrato; o restante é unknown,
	// exceto quando há evidência positiva de partial/stale.
	freshness := "unknown"
	if g.building {
		freshness = "stale"
	} else if grade == "semantic" {
		freshness = "fresh"
	}
	completeness := "unknown"
	if omitted > 0 || g.building {
		completeness = "partial"
	} else if grade == "semantic" {
		completeness = "complete"
	}
	exact := "no"
	if grade == "semantic" && fallback == "none" && completeness == "complete" && freshness == "fresh" {
		exact = "yes"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "basis: action=%s lang=%s provider=%s grade=%s fallback=%s exact=%s freshness=%s completeness=%s scope=repo cache=%s depth=%d",
		g.action, g.lang, provider, grade, fallback, exact, freshness, completeness, cache, g.depth)
	if m := basisTotalRe.FindStringSubmatch(out); m != nil {
		fmt.Fprintf(&b, " refs=%s files=%s", m[1], m[2])
	}
	fmt.Fprintf(&b, " omitted=%d\n", omitted)
	return b.String()
}
