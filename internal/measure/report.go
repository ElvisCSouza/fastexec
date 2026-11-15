package measure

import (
	"sort"

	"homelab/fastexec/internal/ab"
)

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
	// Fase 4 (A/B): preenchido só quando há seção "ab" ligada no config.json.
	ABActive bool
	Arms     []ArmStat
}

// ArmSide é o que um braço do A/B somou para uma regra.
type ArmSide struct {
	Calls   int
	Tokens  int64 // tokens lidos pelo modelo
	Repeats int   // chamadas repetidas (mesmo canal e args_hash)
	Errors  int   // chamadas com falha
}

// ArmStat compara os dois braços de uma regra: On = regra ligada (tratado), Off = regra desligada (controle).
type ArmStat struct {
	Rule string
	On   ArmSide
	Off  ArmSide
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

// Build monta o relatório sem A/B (compatível com quem não usa a fase 4).
func Build(src Source, w Window, p BasisPolicy, ds []HarmDetector) (Report, error) {
	return BuildWithAB(src, w, p, ds, nil)
}

// BuildWithAB monta o relatório; com abc != nil e ligado, separa os braços por regra.
func BuildWithAB(src Source, w Window, p BasisPolicy, ds []HarmDetector, abc *ab.Config) (Report, error) {
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
	if abc != nil && abc.Enabled && len(abc.Rules) > 0 {
		r.ABActive = true
		r.Arms = armStats(calls, *abc)
	}
	return r, nil
}

// armStats separa as chamadas nos dois braços de cada regra do A/B. A identidade é a mesma da operação:
// "conv:<id>" quando o cliente informa o id, senão o canal. Regra fora da lista não é separada.
func armStats(calls []Call, c ab.Config) []ArmStat {
	by := map[string]*ArmStat{}
	var order []string
	for _, rule := range c.Rules {
		if _, ok := by[rule]; ok {
			continue
		}
		by[rule] = &ArmStat{Rule: rule}
		order = append(order, rule)
	}
	seen := map[string]int{}
	for _, cl := range calls {
		id := armIdentity(cl)
		key := cl.Channel + "|" + cl.ArgsHash
		seen[key]++
		rep := seen[key] > 1
		for _, rule := range order {
			st := by[rule]
			side := &st.On
			if c.Off(rule, id) {
				side = &st.Off
			}
			side.Calls++
			side.Tokens += cl.TokensOut
			if rep {
				side.Repeats++
			}
			if !cl.OK {
				side.Errors++
			}
		}
	}
	out := make([]ArmStat, 0, len(order))
	for _, rule := range order {
		out = append(out, *by[rule])
	}
	return out
}

func armIdentity(c Call) string {
	if c.Conversation != "" {
		return "conv:" + c.Conversation
	}
	return c.Channel
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
