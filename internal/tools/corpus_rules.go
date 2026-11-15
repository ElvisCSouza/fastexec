package tools

import (
	"fmt"
	"strings"

	"homelab/fastexec/internal/corpus"
)

// CorpusRulesReport mede, offline e sem rodar comando nem modelo, o quanto as regras que dependem da saída de exec
// poupariam em amostras reais do corpus. Serve às regras raras (group, dedup), que não fecham no A/B.
func CorpusRulesReport(dir string) string {
	samples, skipped := corpus.Load(dir)
	var b strings.Builder
	fmt.Fprintf(&b, "Regras sobre o corpus (%d amostras, %d ilegíveis) em %s\n", len(samples), skipped, dir)
	if len(samples) == 0 {
		b.WriteString("  sem amostras.\n")
		return b.String()
	}
	groupReport(&b, samples)
	dedupReport(&b, samples)
	b.WriteString("  readcap: não se mede aqui (age em leitura de arquivo, não na saída de exec).\n")
	return b.String()
}

// groupReport aplica o reagrupamento real (regroupListingFor) a cada amostra de listagem e soma o que ele pouparia.
func groupReport(b *strings.Builder, samples []corpus.Sample) {
	applied, rawLines, groupedLines, saved := 0, 0, 0, 0
	for _, s := range samples {
		if parseListingKind(s.Label) == listingNone {
			continue
		}
		if grouped, _, ok := regroupListingFor("", s.Label, s.Raw); ok {
			applied++
			rawLines += savLines(s.Raw)
			groupedLines += savLines(grouped)
			saved += len(s.Raw) - len(grouped)
		}
	}
	fmt.Fprintf(b, "  group: %d amostra(s) casariam | %d linhas -> %d | %d B (~%d tokens)\n",
		applied, rawLines, groupedLines, saved, tokens(saved))
}

// dedupReport aproxima o dedup: mesma saída (com o nome do spill mascarado) do mesmo comando no mesmo host, acima do
// tamanho mínimo. O dedup real compara o texto já formatado; aqui a base é a saída crua gravada.
func dedupReport(b *strings.Builder, samples []corpus.Sample) {
	type key struct{ host, label, fp string }
	seen := map[key]bool{}
	dup, saved := 0, 0
	for _, s := range samples {
		if len(s.Raw) < execDedupMinBytes {
			continue
		}
		k := key{s.Host, s.Label, dedupFingerprintText(s.Raw)}
		if seen[k] {
			dup++
			if s.RawBytes > 200 {
				saved += s.RawBytes - 200
			}
		}
		seen[k] = true
	}
	fmt.Fprintf(b, "  dedup: %d amostra(s) repetiriam uma anterior | %d B virariam stub (~%d tokens)\n",
		dup, saved, tokens(saved))
}

func tokens(bytes int) int { return int(float64(bytes) / 3.5) }
