package measure

import (
	"strings"
	"testing"
)

func TestPolicyDefaultAndUnknown(t *testing.T) {
	p := DefaultPolicy()
	casos := map[string]Basis{
		"clean": Measured, "cut": Measured,
		"slice": Counterfactual, "retry": Counterfactual, "timeout": Counterfactual, "schema": Counterfactual,
		"categoria_nova": Counterfactual, // desconhecida nunca infla o medido
	}
	for cat, want := range casos {
		if got := p.Basis(cat); got != want {
			t.Errorf("%s: %v, esperado %v", cat, got, want)
		}
	}
	p.Register("categoria_nova", Measured)
	if p.Basis("categoria_nova") != Measured {
		t.Error("Register não vale")
	}
}

func TestSplitSavings(t *testing.T) {
	rows := []SavingRow{
		{Cat: "clean", Bytes: 3500}, {Cat: "clean", Bytes: 3500},
		{Cat: "slice", Bytes: 35000, Trips: 0},
		{Cat: "retry", Bytes: 700, Trips: 1},
	}
	s := SplitSavings(rows, DefaultPolicy())
	if s.MeasuredTokens != 2000 || s.EstimatedTokens != 10200 {
		t.Fatalf("medido %d, estimativa %d", s.MeasuredTokens, s.EstimatedTokens)
	}
	if s.Cats[0].Cat != "slice" || s.Cats[0].Events != 1 || s.Cats[1].Cat != "clean" || s.Cats[1].Events != 2 {
		t.Errorf("ordem/contagem: %+v", s.Cats)
	}
}

func call(id int64, ch, hash, marks string, trunc bool) Call {
	return Call{ID: id, Channel: ch, ArgsHash: hash, Marks: marks, Truncated: trunc, Tool: "exec", CmdMs: CmdUnknown}
}

func TestRetryAfterCut(t *testing.T) {
	d := RetryAfterCut{Window: 3}
	casos := []struct {
		nome  string
		calls []Call
		want  int
	}{
		{"repete logo depois do corte", []Call{call(1, "a", "h1", "", true), call(2, "a", "h2", "", false), call(3, "a", "h1", "", false)}, 1},
		{"repete fora da janela", []Call{call(1, "a", "h1", "", true), call(2, "a", "x", "", false), call(3, "a", "y", "", false), call(4, "a", "z", "", false), call(5, "a", "h1", "", false)}, 0},
		{"canal diferente não conta", []Call{call(1, "a", "h1", "", true), call(2, "b", "h1", "", false)}, 0},
		{"sem corte não conta", []Call{call(1, "a", "h1", "", false), call(2, "a", "h1", "", false)}, 0},
		{"hash vazio não conta", []Call{call(1, "a", "", "", true), call(2, "a", "", "", false)}, 0},
	}
	for _, c := range casos {
		if got := len(d.Detect(c.calls)); got != c.want {
			t.Errorf("%s: %d sinais, esperado %d", c.nome, got, c.want)
		}
	}
}

func TestRawFlag(t *testing.T) {
	calls := []Call{
		{ID: 1, Summary: "go test ./... --raw"}, {ID: 2, Summary: "echo --rawness"}, {ID: 3, Summary: "--raw ls"}, {ID: 4, Summary: "ls"},
	}
	got := RawFlag{}.Detect(calls)
	if len(got) != 2 || got[0].CallID != 1 || got[1].CallID != 3 {
		t.Errorf("sinais: %+v", got)
	}
}

func TestRepeatedHint(t *testing.T) {
	d := RepeatedHint{MinRepeats: 3}
	calls := []Call{
		call(1, "a", "", "Hint:18f4", false), call(2, "a", "", "Hint:18f4,Guard:zz", false), call(3, "a", "", "Hint:18f4", false),
		call(4, "a", "", "Guard:zz", false), call(5, "a", "", "Guard:zz", false), call(6, "a", "", "Guard:zz", false), // guard não é dica
		call(7, "b", "", "Hint:18f4", false), // outro canal: sozinho
	}
	got := d.Detect(calls)
	if len(got) != 2 || got[0].CallID != 2 || got[1].CallID != 3 {
		t.Errorf("sinais: %+v", got)
	}
	if got := (RepeatedHint{MinRepeats: 4}).Detect(calls); len(got) != 0 {
		t.Errorf("abaixo do mínimo gerou sinais: %+v", got)
	}
}

func TestFeatureUsageIncludesUnused(t *testing.T) {
	calls := []Call{
		{Tool: "exec", Channel: "a", Marks: "Hint:1,Guard:2"}, {Tool: "exec", Channel: "b", Marks: "Hint:1"}, {Tool: "read_file", Channel: "a"},
	}
	u := FeatureUsage(calls)
	if u.Tools[0].Name != "exec" || u.Tools[0].Calls != 2 || u.Tools[0].Channels != 2 {
		t.Errorf("ferramentas: %+v", u.Tools)
	}
	byName := map[string]Use{}
	for _, m := range u.Marks {
		byName[m.Name] = m
	}
	if byName["Hint"].Calls != 2 || byName["Guard"].Calls != 1 {
		t.Errorf("marcas: %+v", u.Marks)
	}
	if m, ok := byName["Rewrite"]; !ok || m.Calls != 0 {
		t.Errorf("marca conhecida sem uso tem que aparecer com zero: %+v", m)
	}
}

func TestOverhead(t *testing.T) {
	calls := []Call{
		{Tool: "exec", DurMs: 110, CmdMs: 100}, {Tool: "exec", DurMs: 130, CmdMs: 100}, {Tool: "exec", DurMs: 500, CmdMs: 100},
		{Tool: "exec", DurMs: 50, CmdMs: 80},               // negativo vira 0
		{Tool: "exec", DurMs: 999, CmdMs: CmdUnknown},      // antigo
		{Tool: "read_file", DurMs: 999, CmdMs: CmdUnknown}, // não é exec
	}
	o := OverheadOf(calls)
	if o.N != 4 || o.Unmeasured != 1 || o.P50Ms != 10 || o.MaxMs != 400 {
		t.Errorf("overhead: %+v", o)
	}
	if o := OverheadOf(nil); o.N != 0 || o.P95Ms != 0 {
		t.Errorf("vazio: %+v", o)
	}
}

type fakeSource struct {
	calls  []Call
	saving []SavingRow
}

func (f fakeSource) Calls(Window) ([]Call, error)        { return f.calls, nil }
func (f fakeSource) Savings(Window) ([]SavingRow, error) { return f.saving, nil }

func TestBuildAndRender(t *testing.T) {
	src := fakeSource{
		calls: []Call{
			{ID: 1, Channel: "c1", Tool: "exec", TokensOut: 1000, Marks: "Hint:1", Summary: "go test --raw", CmdMs: 90, DurMs: 100},
			{ID: 2, Channel: "c2", Tool: "exec", TokensOut: 500, CmdMs: CmdUnknown},
		},
		saving: []SavingRow{{Cat: "clean", Bytes: 7000}, {Cat: "slice", Bytes: 3500}},
	}
	r, err := Build(src, Window{Today: true}, DefaultPolicy(), DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	if r.Calls != 2 || r.Channels != 2 || r.SentTokens != 1500 || r.ConversationCalls != 0 {
		t.Fatalf("relatório: %+v", r)
	}
	out := r.Render()
	for _, want := range []string{
		"Medição (hoje): 2 chamadas em 2 canais",
		"Medida:     2.000 tokens", "Estimativa: 1.000 tokens",
		"raw_flag", "retry_after_cut", "repeated_hint", // todos os detectores aparecem, mesmo com zero
		"1 exec medidos", "1 exec antigos sem medição",
		"aviso      Rewrite", // funcionalidade sem uso visível
		"não informa id de conversa",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	casos := map[Window]string{
		{Today: true}: "hoje", {Hours: 24}: "últimas 24 horas", {Hours: 168}: "últimos 7 dias", {Hours: 5}: "últimas 5 horas", {}: "todo o histórico",
	}
	for w, want := range casos {
		if got := w.Label(); got != want {
			t.Errorf("%+v: %q, esperado %q", w, got, want)
		}
	}
}
