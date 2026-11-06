package telemetry

import (
	"strings"
	"testing"
)

func TestParsePeriod(t *testing.T) {
	cases := map[string]int{"24h": 24, "7d": 168, "30D": 720, "1d": 24}
	for in, want := range cases {
		if got, ok := ParsePeriod(in); !ok || got != want {
			t.Errorf("ParsePeriod(%q) = %d,%v; want %d,true", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "d", "7", "0d", "-3d", "7x", "abc"} {
		if _, ok := ParsePeriod(in); ok {
			t.Errorf("ParsePeriod(%q) aceitou entrada inválida", in)
		}
	}
}

func TestRenderStatsPeriodFiltersByWindow(t *testing.T) {
	db := openTestAudit(t)
	writeEntry(db, AuditEntry{Tool: "recent", Success: true})
	// Registro antigo (30 dias atrás) em formato CURRENT_TIMESTAMP (UTC).
	if _, err := db.Exec(`INSERT INTO audit (session_id, tool, success, timestamp) VALUES ('old', 'old_tool', 1, datetime('now', '-30 days'))`); err != nil {
		t.Fatal(err)
	}

	total, err := renderStats(db, StatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(total, "Histórico Total") || !strings.Contains(total, "old_tool") {
		t.Fatalf("histórico total deveria incluir tudo:\n%s", total)
	}

	week, err := renderStats(db, StatsFilter{PeriodH: 7 * 24})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(week, "Histórico Total") {
		t.Errorf("stats 7d não deveria mais dizer Histórico Total:\n%s", week)
	}
	if !strings.Contains(week, "Últimos 7 dias") {
		t.Errorf("cabeçalho de 7d ausente:\n%s", week)
	}
	if strings.Contains(week, "old_tool") {
		t.Errorf("stats 7d incluiu registro de 30 dias atrás:\n%s", week)
	}
	if !strings.Contains(week, "recent") {
		t.Errorf("stats 7d perdeu registro recente:\n%s", week)
	}

	// Janela de 1 hora só com o registro antigo: nada a mostrar.
	oldOnly := openTestAudit(t)
	if _, err := oldOnly.Exec(`INSERT INTO audit (session_id, tool, success, timestamp) VALUES ('old', 'old_tool', 1, datetime('now', '-30 days'))`); err != nil {
		t.Fatal(err)
	}
	empty, err := renderStats(oldOnly, StatsFilter{PeriodH: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty, "Nenhum registro de telemetria encontrado (na última hora).") {
		t.Errorf("mensagem de janela vazia inesperada:\n%s", empty)
	}
}
