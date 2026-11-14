package telemetry

import (
	"strings"
	"testing"
)

// TestTotaisDoStatsIguaisAoPainel: o bloco de economia do `fastexec stats` usa a mesma soma do relatório do painel
// (invariante de docs/savings.md) — today, week (168h) e all têm que fechar iguais, campo a campo.
func TestTotaisDoStatsIguaisAoPainel(t *testing.T) {
	db := openTestDB(t)
	e := AuditEntry{Tool: "exec", Host: "local", Success: true, BytesOut: 3500, EstTokensOut: 1000, DurationMs: 200, CommandSummary: "go test ./...",
		Savings: []Saving{{Cat: CatClean, Bytes: 7000, Exact: true}}}
	if err := writeEntry(db, e); err != nil {
		t.Fatal(err)
	}
	e2 := AuditEntry{Tool: "exec", Host: "local", Success: true, BytesOut: 350, EstTokensOut: 100, DurationMs: 50, CommandSummary: "cat a.txt",
		Savings: []Saving{{Cat: CatRetry, Bytes: 700, Trips: 1, Ms: AvoidedRoundTripMs}}}
	if err := writeEntry(db, e2); err != nil {
		t.Fatal(err)
	}

	// janela fora do today para exercitar o corte por horas (o painel não tem essa janela)
	if err := writeSavings(db, 0, []Saving{{Cat: CatSchema, Bytes: 3500, Exact: true}}); err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		nome   string
		filtro StatsFilter
		period string
	}{
		{"today", StatsFilter{Today: true}, "today"},
		{"week (168h)", StatsFilter{PeriodH: 168}, "week"},
		{"all", StatsFilter{}, "all"},
	}
	for _, c := range casos {
		st, err := savingsTotalsForStats(db, c.filtro)
		if err != nil {
			t.Fatalf("%s: %v", c.nome, err)
		}
		r, err := buildSavingsReport(db, c.period)
		if err != nil {
			t.Fatalf("%s: %v", c.nome, err)
		}
		if st.Calls != r.Calls || st.FailedCalls != r.FailedCalls || st.SentTokens != r.SentTokens ||
			st.SavedTokens != r.SavedTokens || st.WithoutTokens != r.WithoutTokens || st.Percent != r.Percent ||
			st.TripsAvoided != r.TripsAvoided || st.FailuresAvoided != r.FailuresAvoided ||
			st.CostSavedUSD != r.CostSavedUSD {
			t.Errorf("%s: totais divergem do painel: stats %+v vs painel %+v", c.nome, st, SavingsTotals{
				Calls: r.Calls, FailedCalls: r.FailedCalls, SentTokens: r.SentTokens, SavedTokens: r.SavedTokens,
				WithoutTokens: r.WithoutTokens, Percent: r.Percent, TripsAvoided: r.TripsAvoided,
				FailuresAvoided: r.FailuresAvoided, CostSavedUSD: r.CostSavedUSD})
		}
		if len(st.Cats) != len(r.Cats) {
			t.Errorf("%s: %d categorias no stats, %d no painel", c.nome, len(st.Cats), len(r.Cats))
		}
	}

	// o corte por horas do CLI pega as linhas recentes (writeSavings é "agora")
	st, err := savingsTotalsForStats(db, StatsFilter{PeriodH: 1})
	if err != nil {
		t.Fatal(err)
	}
	if st.SavedTokens != 3200 || st.SentTokens != 1100 || st.FailuresAvoided != 1 {
		t.Errorf("janela de 1h: saved=%d sent=%d failures=%d", st.SavedTokens, st.SentTokens, st.FailuresAvoided)
	}
}

// TestBlocoDeEconomiaNoStats: a saída do stats ganha o bloco SAVINGS em inglês, com os números da soma compartilhada.
func TestBlocoDeEconomiaNoStats(t *testing.T) {
	db := openTestDB(t)
	if err := writeEntry(db, AuditEntry{Tool: "exec", Host: "local", Success: true, BytesOut: 3500, EstTokensOut: 1000, DurationMs: 200,
		Savings: []Saving{{Cat: CatClean, Bytes: 7000, Exact: true}}}); err != nil {
		t.Fatal(err)
	}

	out, err := renderStats(db, StatsFilter{Today: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SAVINGS (today)") || !strings.Contains(out, "Tokens read: ~1000") || !strings.Contains(out, "Tokens saved: ~2000") ||
		!strings.Contains(out, "Without FastExec: ~3000 (66.7%)") {
		t.Errorf("bloco de economia ausente ou errado:\n%s", out)
	}
}
