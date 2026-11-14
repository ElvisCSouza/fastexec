package measure

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Signal é um indício de que uma regra do FastExec atrapalhou ou não teve efeito.
type Signal struct {
	Detector string
	CallID   int64
	Channel  string
	Detail   string
}

// HarmDetector procura um tipo de sinal. Recebe as chamadas de TODOS os canais, em ordem de execução;
// cada detector decide como agrupar. Detector novo é um tipo novo: não mexe nos outros.
type HarmDetector interface {
	Name() string
	Detect(calls []Call) []Signal
}

// DefaultDetectors são os detectores ligados por padrão.
func DefaultDetectors() []HarmDetector {
	return []HarmDetector{RetryAfterCut{Window: 3}, RawFlag{}, RepeatedHint{MinRepeats: 3}}
}

// byChannel agrupa preservando a ordem de execução dentro de cada canal.
func byChannel(calls []Call) map[string][]Call {
	out := map[string][]Call{}
	for _, c := range calls {
		out[c.Channel] = append(out[c.Channel], c)
	}
	return out
}

// RetryAfterCut: depois de uma resposta cortada, o agente repetiu exatamente a mesma chamada.
// Indica que o corte escondeu o que ele precisava.
type RetryAfterCut struct{ Window int }

func (RetryAfterCut) Name() string { return "retry_after_cut" }

func (d RetryAfterCut) Detect(calls []Call) []Signal {
	var out []Signal
	for ch, list := range byChannel(calls) {
		for i, c := range list {
			if !c.Truncated || c.ArgsHash == "" {
				continue
			}
			for j := i + 1; j < len(list) && j <= i+d.Window; j++ {
				if list[j].ArgsHash == c.ArgsHash {
					out = append(out, Signal{Detector: d.Name(), CallID: list[j].ID, Channel: ch,
						Detail: fmt.Sprintf("repetiu a chamada %d, que foi cortada", c.ID)})
					break
				}
			}
		}
	}
	return sortSignals(out)
}

var rawFlagRe = regexp.MustCompile(`(^|\s)--raw(\s|$)`)

// RawFlag: o agente pediu a saída sem filtro. Cada uso é um filtro que ele não quis.
type RawFlag struct{}

func (RawFlag) Name() string { return "raw_flag" }

func (d RawFlag) Detect(calls []Call) []Signal {
	var out []Signal
	for _, c := range calls {
		if rawFlagRe.MatchString(c.Summary) {
			out = append(out, Signal{Detector: d.Name(), CallID: c.ID, Channel: c.Channel, Detail: "pediu saída sem filtro"})
		}
	}
	return sortSignals(out)
}

// hintKinds são as marcas que orientam o agente (e não corrigem nem protegem).
var hintKinds = map[string]bool{"Hint": true, "FastHint": true}

// RepeatedHint: a mesma dica apareceu várias vezes no mesmo canal. A partir da segunda, ela não
// mudou o comportamento (e custa tokens). Gera um sinal por repetição além da primeira, quando a
// dica aparece pelo menos MinRepeats vezes.
type RepeatedHint struct{ MinRepeats int }

func (RepeatedHint) Name() string { return "repeated_hint" }

func (d RepeatedHint) Detect(calls []Call) []Signal {
	var out []Signal
	for ch, list := range byChannel(calls) {
		seen := map[string][]int64{}
		for _, c := range list {
			for _, m := range splitMarks(c.Marks) {
				if hintKinds[markKind(m)] {
					seen[m] = append(seen[m], c.ID)
				}
			}
		}
		for mark, ids := range seen {
			if len(ids) < d.MinRepeats {
				continue
			}
			for _, id := range ids[1:] {
				out = append(out, Signal{Detector: d.Name(), CallID: id, Channel: ch, Detail: "dica " + mark + " repetida"})
			}
		}
	}
	return sortSignals(out)
}

func splitMarks(marks string) []string {
	if marks == "" {
		return nil
	}
	parts := strings.Split(marks, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// markKind devolve o tipo da marca ("Hint:ab12" -> "Hint").
func markKind(mark string) string {
	if i := strings.IndexByte(mark, ':'); i >= 0 {
		return mark[:i]
	}
	return mark
}

func sortSignals(s []Signal) []Signal {
	sort.Slice(s, func(i, j int) bool { return s[i].CallID < s[j].CallID })
	return s
}
