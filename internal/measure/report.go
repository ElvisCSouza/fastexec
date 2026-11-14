package measure

import "sort"

// Report é o resultado da medição de uma janela.
type Report struct {
	Window   Window
	Calls    int
	Channels int
	// ConversationCalls: chamadas em que o cliente informou id de conversa. Zero = "por conversa" não é medível.
	ConversationCalls int
	SentTokens        int64
	Savings           SavingsSplit
	Harm              []HarmCount
	Usage             Usage
	Overhead          Overhead
	PerChannel        []ChannelTokens
	Templates         map[string]string // id do aviso -> texto-modelo
}

// HarmCount é a contagem de sinais de um detector (todos os detectores ligados aparecem, mesmo com zero).
type HarmCount struct {
	Detector string
	Count    int
	Samples  []Signal // até 3 exemplos
}

// ChannelTokens é o que um canal (processo MCP) recebeu na janela.
type ChannelTokens struct {
	Channel string
	Calls   int
	Tokens  int64
}

// Build lê a fonte e monta o relatório. Não tem efeito colateral.
func Build(src Source, w Window, p BasisPolicy, ds []HarmDetector) (Report, error) {
	calls, err := src.Calls(w)
	if err != nil {
		return Report{}, err
	}
	rows, err := src.Savings(w)
	if err != nil {
		return Report{}, err
	}
	templates, err := src.MarkTemplates()
	if err != nil {
		return Report{}, err
	}
	r := Report{Window: w, Calls: len(calls), Savings: SplitSavings(rows, p), Usage: FeatureUsage(calls), Overhead: OverheadOf(calls), Templates: templates}
	perCh := map[string]*ChannelTokens{}
	var order []string
	for _, c := range calls {
		r.SentTokens += c.TokensOut
		if c.HasConversation {
			r.ConversationCalls++
		}
		ct, ok := perCh[c.Channel]
		if !ok {
			ct = &ChannelTokens{Channel: c.Channel}
			perCh[c.Channel] = ct
			order = append(order, c.Channel)
		}
		ct.Calls++
		ct.Tokens += c.TokensOut
	}
	r.Channels = len(perCh)
	for _, ch := range order {
		r.PerChannel = append(r.PerChannel, *perCh[ch])
	}
	sortChannels(r.PerChannel)
	for _, d := range ds {
		sigs := d.Detect(calls)
		hc := HarmCount{Detector: d.Name(), Count: len(sigs)}
		if len(sigs) > 3 {
			hc.Samples = sigs[:3]
		} else {
			hc.Samples = sigs
		}
		r.Harm = append(r.Harm, hc)
	}
	return r, nil
}

// sortChannels: maior consumo primeiro; empate pelo nome para a saída ser estável.
func sortChannels(c []ChannelTokens) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Tokens != c[j].Tokens {
			return c[i].Tokens > c[j].Tokens
		}
		return c[i].Channel < c[j].Channel
	})
}
