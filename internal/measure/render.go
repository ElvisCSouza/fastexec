package measure

import (
	"fmt"
	"strings"
)

// maxChannelsShown limita a lista por canal: o relatório é lido por gente (e por LLM), não despejado.
const maxChannelsShown = 5

// Render devolve o relatório em texto, em português, tabelas curtas.
func (r Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Medição (%s): %d chamadas em %d canais\n", r.Window.Label(), r.Calls, r.Channels)

	b.WriteString("\n1. Economia: medida x estimativa\n")
	m, e := r.Savings.MeasuredTokens, r.Savings.EstimatedTokens
	fmt.Fprintf(&b, "   Medida:     %s tokens (bytes que o FastExec removeu de uma saída que a chamada produziu)\n", num(m))
	fmt.Fprintf(&b, "   Estimativa: %s tokens (supõe o que o modelo faria sem o FastExec; não foi observado)\n", num(e))
	fmt.Fprintf(&b, "   Lidos pelo modelo: %s tokens. Medida sobre o total sem filtro: %s\n", num(r.SentTokens), pctOf(m, r.SentTokens+m))
	for _, c := range r.Savings.Cats {
		fmt.Fprintf(&b, "   - %-8s %-10s %10s tokens  %5d eventos\n", c.Cat, c.Basis, num(c.Tokens), c.Events)
	}

	b.WriteString("\n2. Sinais de dano (regras que atrapalharam ou não tiveram efeito)\n")
	for _, h := range r.Harm {
		fmt.Fprintf(&b, "   - %-16s %5d\n", h.Detector, h.Count)
		for _, s := range h.Samples {
			fmt.Fprintf(&b, "       chamada %d: %s\n", s.CallID, s.Detail)
		}
	}

	b.WriteString("\n3. Uso por funcionalidade\n")
	for _, u := range r.Usage.Tools {
		fmt.Fprintf(&b, "   ferramenta %-14s %6d chamadas  %3d canais\n", u.Name, u.Calls, u.Channels)
	}
	for _, u := range r.Usage.Marks {
		fmt.Fprintf(&b, "   aviso      %-16s %6d chamadas  %3d canais\n", u.Name, u.Calls, u.Channels)
	}

	b.WriteString(r.renderRules())

	b.WriteString("\n\n4. Tempo que o FastExec acrescenta ao exec\n")
	o := r.Overhead
	if o.N == 0 {
		fmt.Fprintf(&b, "   Sem dados ainda (%d exec sem o tempo do comando registrado).\n", o.Unmeasured)
	} else {
		fmt.Fprintf(&b, "   %d exec medidos: mediana %d ms, p95 %d ms, máximo %d ms (%d exec antigos sem medição)\n",
			o.N, o.P50Ms, o.P95Ms, o.MaxMs, o.Unmeasured)
	}

	b.WriteString("\n5. Tokens por canal (um canal = um processo MCP)\n")
	shown := r.PerChannel
	if len(shown) > maxChannelsShown {
		shown = shown[:maxChannelsShown]
	}
	for _, c := range shown {
		fmt.Fprintf(&b, "   %-44s %6d chamadas %10s tokens\n", c.Channel, c.Calls, num(c.Tokens))
	}
	if r.ConversationCalls == 0 {
		b.WriteString("   Limite: o cliente não informa id de conversa; o canal é o mais próximo disponível.\n")
		b.WriteString("   Tipo de tarefa não é registrado: tokens por tipo de tarefa fica para a fase do A/B.\n")
	}
	return b.String()
}

// maxRulesShown e ruleTextMax mantêm a lista de avisos curta: o que importa é o topo.
const (
	maxRulesShown = 8
	ruleTextMax   = 70
)

// renderRules lista os avisos mais frequentes pelo id, com o texto-modelo quando o catálogo o conhece.
func (r Report) renderRules() string {
	var b strings.Builder
	b.WriteString("   Avisos mais frequentes (cada id é uma regra):\n")
	shown := r.Usage.Rules
	if len(shown) > maxRulesShown {
		shown = shown[:maxRulesShown]
	}
	for _, u := range shown {
		text, ok := r.Templates[u.Name]
		if !ok {
			text = "(texto desconhecido: id anterior ao catálogo)"
		} else if len([]rune(text)) > ruleTextMax {
			text = string([]rune(text)[:ruleTextMax]) + "…"
		}
		fmt.Fprintf(&b, "     %-22s %5d x %2d canais  %s\n", u.Name, u.Calls, u.Channels, text)
	}
	return strings.TrimRight(b.String(), "\n")
}

func num(n int64) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

func pctOf(part, total int64) string {
	if total <= 0 {
		return "n/d"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(total)*100)
}
