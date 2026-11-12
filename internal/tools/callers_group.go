package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Chamadores agrupados por arquivo.
//
// `callers` devolve uma linha por ponto de chamada. Para um símbolo muito usado são dezenas de linhas, e a pergunta
// que quase sempre vem junto é "em quais arquivos?". No ciclo6 (C4, cobra) o código_graph respondeu certo em 3,1 KB
// (61 linhas, 7 arquivos) sem listar os arquivos, e o agente gastou três turnos com `grep -rl` para obtê-los.
// Com muitos pontos, o texto passa a ser uma linha por arquivo, com função e linhas: menor e já responde a pergunta.
//
// Só agrupa quando não perde informação: sem "via" (tipo do receptor, no caso de nomes iguais) e com a mesma relação
// em todas as linhas. Qualquer outra coisa segue como está.

const callerGroupMin = 15

var callerSiteRe = regexp.MustCompile(`^- (.+?):(\d+)(?: in (.+?))? \(([^()]+)\)$`)

type callerSite struct {
	file  string
	line  int
	label string
}

func groupCallerSites(text string) string {
	lines := strings.Split(text, "\n")
	var sites []callerSite
	firstIdx := -1
	rel := ""
	for i, l := range lines {
		m := callerSiteRe.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			continue
		}
		if strings.Contains(m[3], " via ") {
			return text // receptor e tipo por linha: informação que o agrupamento esconderia
		}
		if rel == "" {
			rel = m[4]
		} else if m[4] != rel {
			return text
		}
		n, _ := strconv.Atoi(m[2])
		sites = append(sites, callerSite{file: m[1], line: n, label: m[3]})
		if firstIdx < 0 {
			firstIdx = i
		}
	}
	if len(sites) < callerGroupMin {
		return text
	}

	var order []string
	byFile := map[string][]callerSite{}
	for _, s := range sites {
		if _, seen := byFile[s.file]; !seen {
			order = append(order, s.file)
		}
		byFile[s.file] = append(byFile[s.file], s)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "(%d sites, all '%s', grouped by file: function then line numbers)\n", len(sites), rel)
	for _, f := range order {
		ss := byFile[f]
		fmt.Fprintf(&b, "- %s (%d): ", f, len(ss))
		// funções consecutivas iguais viram uma só; sem rótulo, só as linhas
		var parts []string
		for i := 0; i < len(ss); {
			j := i
			var nums []string
			for j < len(ss) && ss[j].label == ss[i].label {
				nums = append(nums, strconv.Itoa(ss[j].line))
				j++
			}
			if ss[i].label != "" {
				parts = append(parts, ss[i].label+" "+strings.Join(nums, ","))
			} else {
				parts = append(parts, strings.Join(nums, ","))
			}
			i = j
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString("\n")
	}
	grouped := strings.TrimRight(b.String(), "\n")

	var out []string
	for i, l := range lines {
		if callerSiteRe.MatchString(strings.TrimRight(l, "\r")) {
			if i == firstIdx {
				out = append(out, grouped)
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
