package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestToolsListSize trava o tamanho do tools/list: o schema inteiro vai ao LLM em TODO turno, então cada byte aqui
// é imposto fixo (medido no bench ds-full2: ~2.4k tokens por turno a mais que as tools nativas). O teto é o que a
// política de descrição curta permite (ver docs/tools.md, "Tool descriptions" e tool_schemas.go); estourar aqui é
// regressão de custo, não de comportamento.
//
// O alvo do trabalho de 2026-11-12 era -30% sobre a linha de base abaixo; o caso que importa é o core (o que todo
// turno paga). Os módulos sob demanda entram só quando alguém liga, e por isso têm teto próprio, medido.
// Rodar com -v para ver a tabela por ferramenta.
const (
	coreBaselineBytes = 8796  // tools/list sem módulos, antes da redução
	allBaselineBytes  = 11227 // com docker, sql e hosts ativos, antes da redução

	coreBytesCeiling = 6200 // medido 6060 (-31%); folga para uma linha de descrição a mais
	allBytesCeiling  = 8700 // medido 8491
)

// measureToolsList devolve o tamanho no fio e, por ferramenta, os bytes do objeto JSON dela.
func measureToolsList(t *testing.T, line []byte) (int, []struct {
	Name  string
	Bytes int
}) {
	t.Helper()
	var resp struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("tools/list inválido: %v", err)
	}
	rows := make([]struct {
		Name  string
		Bytes int
	}, 0, len(resp.Result.Tools))
	for _, raw := range resp.Result.Tools {
		var head struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &head)
		rows = append(rows, struct {
			Name  string
			Bytes int
		}{head.Name, len(raw)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bytes > rows[j].Bytes })
	return len(line), rows
}

func reportToolsListSize(t *testing.T, label string, wire int, rows []struct {
	Name  string
	Bytes int
}) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s: %d bytes (~%d tokens, bytes/4)\n", label, wire, wire/4)
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-14s %5d bytes (~%4d tokens)\n", r.Name, r.Bytes, r.Bytes/4)
	}
	t.Log(b.String())
}

func reductionPct(baseline, now int) float64 {
	return 100 * float64(baseline-now) / float64(baseline)
}

// TestToolsListSizeCore: o tools/list de todo turno (sem módulos ativos).
func TestToolsListSizeCore(t *testing.T) {
	wire, rows := measureToolsList(t, listToolsLine(t, &Server{}))
	reportToolsListSize(t, "tools/list (core)", wire, rows)
	if wire > coreBytesCeiling {
		t.Errorf("tools/list core = %d bytes (~%d tokens), teto %d: descrição curta é o orçamento; enxugue antes de subir",
			wire, wire/4, coreBytesCeiling)
	}
	if pct := reductionPct(coreBaselineBytes, wire); pct < 30 {
		t.Errorf("tools/list core = %d bytes: redução de %.1f%% sobre a linha de base (%d bytes); o alvo é 30%%",
			wire, pct, coreBaselineBytes)
	} else {
		t.Logf("tools/list core: %d -> %d bytes (%.1f%% a menos, %d -> %d tokens)",
			coreBaselineBytes, wire, pct, coreBaselineBytes/4, wire/4)
	}
}

// TestToolsListSizeAllModules: com docker, sql e hosts ativos (quem liga módulo paga por eles).
func TestToolsListSizeAllModules(t *testing.T) {
	for _, m := range []string{"docker", "sql", "hosts"} {
		setModule(t, "enable", m)
	}
	t.Cleanup(func() {
		for _, m := range []string{"docker", "sql", "hosts"} {
			setModule(t, "disable", m)
		}
	})
	wire, rows := measureToolsList(t, listToolsLine(t, &Server{}))
	reportToolsListSize(t, "tools/list (all modules)", wire, rows)
	// Módulo sob demanda não entra no imposto por turno: aqui o teto só impede regressão, sem alvo de 30%
	// (as descrições deles também são o texto de ajuda do tool_manage search, então encurtar demais tem custo).
	if wire > allBytesCeiling {
		t.Errorf("tools/list com módulos = %d bytes (~%d tokens), teto %d", wire, wire/4, allBytesCeiling)
	}
	t.Logf("tools/list com módulos: %d -> %d bytes (%.1f%% a menos)", allBaselineBytes, wire, reductionPct(allBaselineBytes, wire))
}
