package graph

// homonyms.go: o grafo casa chamadas só pelo NOME do método. Quando o mesmo nome é declarado em mais de uma classe
// (CalculoSCFService.CalcularOperacao e CalculoMCMSService.CalcularOperacao), o agente não distinguia quem chama qual,
// e listava chamadores do tipo errado (T7 do benchmark: 2 métodos a mais, nível 3 contado como nível 2). Aqui: (1) a
// classe de quem chama vem sempre no rótulo; (2) só quando há homônimos, cada referência mostra o receptor da chamada
// ("via _calculoMCMSService") e para qual declaração ele aponta, pelo nome do receptor ("-> CalculoMCMSService");
// (3) "Tipo.método" em symbol restringe às referências desse tipo. Sem resolução de tipos: é heurística de nome, e
// quando não dá para decidir a referência fica "-> ?" (nunca é descartada em silêncio).

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// typeKinds são os kinds que DECLARAM outros símbolos (podem envolvê-los); typeKindsSQL é a mesma lista para o
// SQL, derivada dela para as duas nunca divergirem (enclosing.go decide em Go o que as consultas decidiam em SQL).
var typeKinds = []string{"class", "struct", "interface", "record", "enum", "object", "trait", "union"}

var typeKindsSQL = `'` + strings.Join(typeKinds, "','") + `'`

func isTypeKind(kind string) bool {
	for _, k := range typeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// declaringTypes devolve os tipos (classes) que declaram um método/função com esse nome, sem repetição e em ordem.
func declaringTypes(d *sql.DB, root, name string) []string {
	rows, err := d.Query(`SELECT COALESCE((SELECT t.name FROM code_symbols t
			WHERE t.repo_root = s.repo_root AND t.file_path = s.file_path AND t.kind IN (`+typeKindsSQL+`)
				AND t.start_line <= s.start_line AND t.end_line >= s.end_line AND t.id <> s.id
			ORDER BY t.end_line - t.start_line LIMIT 1), '')
		FROM code_symbols s WHERE s.repo_root = ? AND s.name = ? AND s.kind IN ('method','function')`, repoKey(root), name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil && t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// srcLines lê (uma vez por arquivo) as linhas de um arquivo do repositório; vazio se não der para ler.
type srcLines struct {
	root  string
	cache map[string][]string
}

func (s *srcLines) line(file string, n int) string {
	if s.cache == nil {
		s.cache = map[string][]string{}
	}
	ls, ok := s.cache[file]
	if !ok {
		if b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(file))); err == nil {
			ls = strings.Split(string(b), "\n")
		}
		s.cache[file] = ls
	}
	if n < 1 || n > len(ls) {
		return ""
	}
	return ls[n-1]
}

// receiverOf extrai de uma linha o receptor da chamada de `name` ("_calculoMCMSService" em
// "_calculoMCMSService.CalcularOperacao(x)"). found=false se a linha não tem a chamada; recv=="" é chamada sem
// receptor (this implícito) e "this"/"base" também contam como a própria classe.
func receiverOf(line, name string) (recv string, found bool) {
	q := regexp.QuoteMeta(name)
	if m := regexp.MustCompile(`([A-Za-z_]\w*(?:\??\.[A-Za-z_]\w*)*)\??\.` + q + `\s*[(<]`).FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	if regexp.MustCompile(`(^|[^\w.])` + q + `\s*[(<]`).MatchString(line) {
		return "", true
	}
	return "", false
}

// guessType decide, pelo nome do receptor, qual das declarações a chamada alcança. "" = indeciso.
func guessType(recv, enclosing string, decl []string) string {
	last := recv
	if i := strings.LastIndexAny(last, "."); i >= 0 {
		last = last[i+1:]
	}
	last = strings.TrimLeft(strings.ToLower(strings.TrimPrefix(last, "?")), "_")
	if last == "" || last == "this" || last == "base" {
		for _, t := range decl {
			if t == enclosing {
				return t
			}
		}
		return ""
	}
	var hit []string
	for _, t := range decl {
		tl := strings.ToLower(t)
		if last == tl || strings.HasSuffix(last, tl) || (len(last) >= 6 && strings.HasSuffix(tl, last)) {
			hit = append(hit, t)
		}
	}
	if len(hit) == 1 {
		return hit[0]
	}
	return ""
}

// qualifierType devolve o tipo pedido em "Tipo.método" (se for um dos que declaram o método).
func qualifierType(symbol string, decl []string) string {
	i := strings.LastIndex(symbol, ".")
	if i <= 0 {
		return ""
	}
	q := symbol[:i]
	if j := strings.LastIndex(q, "."); j >= 0 {
		q = q[j+1:]
	}
	for _, t := range decl {
		if strings.EqualFold(t, q) {
			return t
		}
	}
	return ""
}
