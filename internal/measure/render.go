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

	r.renderAB(&b)
	return b.String()
}

// renderAB mostra, por regra sob A/B, os dois braços (regra ligada x desligada). É a leitura do experimento:
// a regra só vence se reduzir tokens lidos sem subir repetidas nem erros.
func (r Report) renderAB(b *strings.Builder) {
	b.WriteString("\n6. A/B POR REGRA: metade das conversas roda COM a regra, metade SEM; o resto do FastExec é igual nos dois lados\n")
	if !r.ABActive {
		b.WriteString("   Desligado: sem seção \"ab\" ligada no config.json (nenhuma regra muda de lado).\n")
		return
	}
	b.WriteString("   COM a regra = ela agiu (menos tokens, risco de esconder). SEM a regra = saída crua, só para comparar (linha de base).\n")
	dano := func(s ArmSide) string {
		rc, raw, rep := s.Dano["retry_after_cut"], s.Dano["raw_flag"], s.Dano["repeated_hint"]
		return fmt.Sprintf("%d (%d/%d/%d)", rc+raw+rep, rc, raw, rep)
	}
	fmt.Fprintf(b, "   %-10s %-4s %8s %13s %9s %6s  %-19s %s\n", "regra", "caso", "chamadas", "tokens lidos", "repetidas", "erros", "dano rc/raw/rep", "cmd ms (n)")
	events := map[string]int64{}
	for _, c := range r.Savings.Cats {
		events[c.Cat] = c.Events
	}
	for _, a := range r.Arms {
		fmt.Fprintf(b, "   %-10s %-7s %8d %13s %9d %6d  %-19s %d (%d)\n", a.Rule, "COM", a.On.Calls, num(a.On.Tokens), a.On.Repeats, a.On.Errors, dano(a.On), a.On.CmdMs, a.On.CmdN)
		fmt.Fprintf(b, "   %-10s %-7s %8d %13s %9d %6d  %-19s %d (%d)\n", "", "SEM", a.Off.Calls, num(a.Off.Tokens), a.Off.Repeats, a.Off.Errors, dano(a.Off), a.Off.CmdMs, a.Off.CmdN)
		fmt.Fprintf(b, "     -> coletas na janela: %s | %s\n", collectState(events[a.Rule]), armVerdict(a))
	}
	fmt.Fprintf(b, "   Regra: decide com %d coletas da regra (eventos na janela, sem piso de tempo); vence reduzindo tokens lidos por chamada sem subir dano, repetidas ou erros; empate = manter. Contar só o A/B ligado (confira a janela).\n", ArmMinSamples)
}

// armVerdict compara os braços por chamada (tamanhos de braço diferentes) e emite o veredito mecânico do ADR-002.
func armVerdict(a ArmStat) string {
	if a.On.Calls == 0 || a.Off.Calls == 0 {
		return "sem amostra em um braço"
	}
	onTok, offTok := a.On.MeanTok, a.Off.MeanTok
	onDano, offDano := perCall(int64(danoTotal(a.On)), a.On.Calls), perCall(int64(danoTotal(a.Off)), a.Off.Calls)
	onRep, offRep := perCall(int64(a.On.Repeats), a.On.Calls), perCall(int64(a.Off.Repeats), a.Off.Calls)
	verdict := "empate: manter"
	switch {
	case onTok > offTok*1.02:
		verdict = "rever: ligada lê mais"
	case onTok < offTok*0.98 && onDano <= offDano && onRep <= offRep:
		verdict = "vence: manter"
	}
	bal := "ok"
	if mn, mx := a.On.Convs, a.Off.Convs; mn > 0 && mx > 0 {
		if mn > mx {
			mn, mx = mx, mn
		}
		if float64(mn)/float64(mx) < 0.6 {
			bal = "desequilibrado"
		}
	}
	return fmt.Sprintf("%s | equilíbrio %s (%d x %d) | tokens/conversa %.0f vs %.0f | dano/chamada %.2f vs %.2f | repetidas/chamada %.2f vs %.2f",
		verdict, bal, a.On.Convs, a.Off.Convs, onTok, offTok, onDano, offDano, onRep, offRep)
}

// collectState diz se a regra já juntou coletas bastantes (ArmMinSamples eventos da regra na janela). O evento
// só existe no braço tratado (regra ligada); como o sorteio é 50%, ~metade das ocasiões gera evento.
func collectState(n int64) string {
	if n >= ArmMinSamples {
		return fmt.Sprintf("ok (%d)", n)
	}
	return fmt.Sprintf("faltam %d (%d)", int64(ArmMinSamples)-n, n)
}

func perCall(total int64, calls int) float64 {
	if calls == 0 {
		return 0
	}
	return float64(total) / float64(calls)
}

func danoTotal(s ArmSide) int {
	n := 0
	for _, v := range s.Dano {
		n += v
	}
	return n
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
