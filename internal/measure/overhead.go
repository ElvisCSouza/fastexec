package measure

import "sort"

// Overhead é o tempo que o FastExec acrescenta a um exec: tempo total menos o tempo do comando.
type Overhead struct {
	N          int   // chamadas exec com tempo do comando medido
	Unmeasured int   // chamadas exec sem o tempo do comando (anteriores à medição)
	P50Ms      int64 // mediana
	P95Ms      int64
	MaxMs      int64
}

// OverheadOf calcula o overhead só das chamadas exec com CmdMs conhecido.
func OverheadOf(calls []Call) Overhead {
	var o Overhead
	var v []int64
	for _, c := range calls {
		if c.Tool != "exec" {
			continue
		}
		if c.CmdMs == CmdUnknown {
			o.Unmeasured++
			continue
		}
		d := c.DurMs - c.CmdMs
		if d < 0 {
			d = 0
		}
		v = append(v, d)
	}
	o.N = len(v)
	if o.N == 0 {
		return o
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	o.P50Ms, o.P95Ms, o.MaxMs = pct(v, 0.50), pct(v, 0.95), v[len(v)-1]
	return o
}

// pct devolve o percentil (método do posto mais próximo) de uma lista ordenada e não vazia.
func pct(sorted []int64, p float64) int64 {
	i := int(p*float64(len(sorted))+0.5) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
