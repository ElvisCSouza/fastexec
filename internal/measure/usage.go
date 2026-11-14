package measure

import "sort"

// KnownMarkKinds são os tipos de marca que o FastExec emite hoje. Aparecem no relatório mesmo com zero uso:
// funcionalidade sem uso precisa ser visível, não ausente.
var KnownMarkKinds = []string{"Auto-Remediation", "FastHint", "Hint", "Guard", "Graph", "Guidance", "Handoff", "Rewrite", "Warning", "FastExec", "args-refused", "auto-wait"}

// Use é o uso de uma ferramenta ou de um tipo de marca.
type Use struct {
	Name     string
	Calls    int64
	Channels int
}

// Usage é o uso por funcionalidade na janela.
type Usage struct {
	Tools []Use // por ferramenta
	Marks []Use // por tipo de marca (inclui os conhecidos com zero)
	Rules []Use // por aviso individual ("Hint:18f4"): cada id é uma regra
}

// FeatureUsage conta chamadas e canais distintos por ferramenta e por tipo de marca.
func FeatureUsage(calls []Call) Usage {
	tools, marks, rules := newCounter(), newCounter(), newCounter()
	for _, k := range KnownMarkKinds {
		marks.touch(k)
	}
	for _, c := range calls {
		tools.add(c.Tool, c.Channel)
		for _, m := range splitMarks(c.Marks) {
			marks.add(markKind(m), c.Channel)
			rules.add(m, c.Channel)
		}
	}
	return Usage{Tools: tools.uses(), Marks: marks.uses(), Rules: rules.uses()}
}

type counter struct {
	calls    map[string]int64
	channels map[string]map[string]bool
}

func newCounter() *counter {
	return &counter{calls: map[string]int64{}, channels: map[string]map[string]bool{}}
}

func (c *counter) touch(name string) {
	if _, ok := c.calls[name]; !ok {
		c.calls[name] = 0
		c.channels[name] = map[string]bool{}
	}
}

func (c *counter) add(name, channel string) {
	c.touch(name)
	c.calls[name]++
	c.channels[name][channel] = true
}

func (c *counter) uses() []Use {
	out := make([]Use, 0, len(c.calls))
	for n, v := range c.calls {
		out = append(out, Use{Name: n, Calls: v, Channels: len(c.channels[n])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}
