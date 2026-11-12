// lsp_bridge.go: o único lugar em que uma tool pergunta a um servidor de linguagem.
//
// O grafo por nome responde "quem usa o nome X" casando texto, e por isso não distingue dois métodos com o
// mesmo nome em tipos diferentes: ele diz "pode ser". O servidor de linguagem responde a mesma pergunta com
// o tipo checado, e diz "é". Este arquivo troca uma resposta pela outra quando o servidor está disponível,
// marca qual das duas está sendo entregue e — quando o servidor não pode responder — volta para o grafo com
// uma linha dizendo por quê. Nunca entrega uma resposta parcial como se fosse exata.
//
// A divisão de trabalho é sempre a mesma: o ÍNDICE diz onde está a declaração (o servidor precisa de uma
// posição), o SERVIDOR diz o conjunto de referências (é o que ele sabe fazer e o grafo não), e o ÍNDICE
// volta a dizer o texto de cada linha (rótulo e relação), que é barato e não muda o conjunto.
package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

const (
	// lspBridgeTimeout é o teto de uma consulta exata vinda de uma tool. O primeiro pedido paga o
	// carregamento do módulo (segundos), então o teto é generoso de propósito; quem estoura degrada.
	lspBridgeTimeout = 45 * time.Second
	// lspBridgeSettle é quanto se espera o servidor analisar o documento antes de acreditar em uma lista
	// vazia. Ver lsp.WaitSettled: vazio pode ser "ainda não carregou", e isso não é "não tem uso".
	lspBridgeSettle = 20 * time.Second
	// editImpactLSPBudget é o teto do caminho exato dentro do rodapé de edição. A edição já está gravada
	// em disco quando isto roda: o rodapé pode perder a precisão, nunca o tempo.
	editImpactLSPBudget = 120 * time.Millisecond
	// lspImpactMaxFiles limita a listagem por arquivo do impacto (o grafo usa 'max'; aqui o teto protege o
	// tamanho da resposta quando um nome é usado em centenas de arquivos).
	lspImpactMaxFiles = 60
)

// errNoAnchor: não há o que o servidor possa responder — o índice não conhece declaração com esse nome, ou
// a linguagem do arquivo não tem servidor configurado. NÃO é degradação: a resposta por nome É a resposta,
// e anunciar "lsp unavailable" a cada consulta de um repositório de linguagem sem servidor seria ruído puro.
var errNoAnchor = errors.New("no anchor for a language server")

// acquireFunc obtém um cliente para o arquivo. Duas políticas, e a diferença entre elas é a diretriz
// inteira: iniciar-sob-demanda (tools, que podem esperar) e só-quente (o rodapé de edição, que não pode).
type acquireFunc func(ctx context.Context, file string) (*lsp.Client, func(), error)

func spawnAcquire(ctx context.Context, file string) (*lsp.Client, func(), error) {
	return lsp.Manager().AcquireFile(ctx, file)
}

func warmAcquire(_ context.Context, file string) (*lsp.Client, func(), error) {
	return lsp.Manager().WarmFile(file)
}

// exactAnswer é a resposta exata de um símbolo.
type exactAnswer struct {
	name   string
	server string
	defs   []graph.Site // onde o índice diz que ele é declarado
	sites  []graph.Site // onde o servidor diz que ele é usado, já ordenado
}

// runExact ancora o servidor em cada declaração indexada do símbolo e chama fn uma vez por declaração.
//
// A âncora é a declaração porque o servidor só aceita posição, e quem sabe a posição é o índice. Nome
// declarado em K tipos (homônimos) rende K âncoras: a união das K respostas é, por construção, o conjunto
// exato de referências ao nome — que é exatamente o que o grafo tenta adivinhar.
//
// Qualquer falha em QUALQUER âncora aborta o resultado inteiro. Um subconjunto de K respostas apresentado
// como exato seria pior do que a resposta por nome que ele viria substituir.
func runExact(ctx context.Context, root, symbol string, acquire acquireFunc, settle time.Duration,
	fn func(c *lsp.Client, abs string, pos lsp.Position) error) ([]graph.DefSite, string, error) {

	// FASTEXEC_LSP=off é escolha do usuário, não degradação: a resposta por nome é a resposta, sem nota e
	// sem nem tocar no índice (é o que mantém o comportamento idêntico ao de antes do LSP).
	if lsp.Disabled() {
		return nil, "", errNoAnchor
	}
	defs := graph.DefSites(root, symbol)
	if len(defs) == 0 {
		return nil, "", errNoAnchor
	}
	name := graph.LastName(symbol)
	server := ""
	done := map[string]bool{}
	for _, d := range defs {
		abs := filepath.Join(root, filepath.FromSlash(d.File))
		// Linguagem sem servidor configurado: a resposta por nome não é degradação, é o esperado.
		if _, ok := lsp.LanguageForPath(abs); !ok {
			return nil, "", errNoAnchor
		}
		key := graph.SiteKey(graph.Site{File: d.File, Line: d.Line})
		if done[key] {
			continue
		}
		done[key] = true

		c, release, err := acquire(ctx, abs)
		if err != nil {
			return nil, "", err
		}
		if server == "" {
			server = c.Name()
		}
		// Servidor rodando em projeto ADIVINHADO (tsserver sem tsconfig/jsconfig): ele responde, mas
		// a resposta cross-file pode estar incompleta, e uma lista incompleta marcada "exact" é pior
		// que a resposta por nome. Degrada com o motivo, que é o que o usuário precisa ler para
		// criar o config e passar a receber o exato.
		warn := c.ProjectWarning()
		if warn != "" {
			release()
			return nil, "", lsp.Unavailable("%s", warn)
		}
		err = stepExact(ctx, c, abs, name, d, settle, fn)
		release()
		if err != nil {
			return nil, "", err
		}
	}
	return defs, server, nil
}

// stepExact faz a parte de uma âncora: documento analisado, coluna válida, chamada.
func stepExact(ctx context.Context, c *lsp.Client, abs, name string, d graph.DefSite, settle time.Duration,
	fn func(c *lsp.Client, abs string, pos lsp.Position) error) error {

	if err := c.WaitSettled(ctx, abs, settle); err != nil {
		return reasonErr(err)
	}
	if d.Col == 0 {
		// O arquivo mudou desde a indexação, ou a declaração é gerada, e o nome não está na linha que o
		// índice aponta. Chutar a coluna 1 devolveria a resposta de outro símbolo.
		return lsp.Unavailable("declaration of '%s' is not where the index says (%s:%d)", name, d.File, d.Line)
	}
	return fn(c, abs, lsp.Position{Line: d.Line - 1, Character: d.Col - 1})
}

// reasonErr reduz o erro do servidor à frase curta que cabe na nota de degradação.
func reasonErr(err error) error {
	var ue *lsp.UnavailableError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ue):
		return err
	case errors.Is(err, lsp.ErrLoading):
		return lsp.Unavailable("project did not load")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return lsp.Unavailable("timed out")
	case errors.Is(err, lsp.ErrUnsupported):
		return lsp.Unavailable("server does not answer this request")
	}
	return lsp.Unavailable("%v", err)
}

// relSite converte uma localização do servidor em site do grafo (arquivo relativo à raiz, linha 1-based).
// Devolve false para o que cai fora da raiz: o resultado exato é sobre este repositório.
func relSite(root string, l lsp.Location) (graph.Site, bool) {
	p := lsp.URItoPath(l.URI)
	if p == "" {
		return graph.Site{}, false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return graph.Site{}, false
	}
	return graph.Site{File: filepath.ToSlash(rel), Line: l.Range.Start.Line + 1}, true
}

// resolveUses devolve as referências exatas do símbolo.
func resolveUses(ctx context.Context, root, symbol string, acquire acquireFunc, settle time.Duration) (*exactAnswer, error) {
	var got []graph.Site
	defs, server, err := runExact(ctx, root, symbol, acquire, settle,
		func(c *lsp.Client, abs string, pos lsp.Position) error {
			locs, err := c.References(ctx, abs, pos, false)
			if err != nil {
				return reasonErr(err)
			}
			for _, l := range locs {
				if s, ok := relSite(root, l); ok {
					got = append(got, s)
				}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	out := &exactAnswer{name: graph.LastName(symbol), server: server}
	seen := map[string]bool{}
	for _, s := range got {
		if k := graph.SiteKey(s); !seen[k] {
			seen[k] = true
			out.sites = append(out.sites, s)
		}
	}
	graph.SortSites(out.sites)
	for _, d := range defs {
		out.defs = append(out.defs, graph.Site{File: d.File, Line: d.Line})
	}
	return out, nil
}

// lspCallers responde "quem chama" com as referências exatas, no mesmo formato do grafo.
// depth>1 continua no grafo: o fecho transitivo teria de resolver cada chamador por nome, que é justamente
// o palpite que o servidor existe para evitar.
func lspCallers(root, symbol string, max, depth int) (string, error) {
	if depth > 1 {
		return "", errNoAnchor
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspBridgeTimeout)
	defer cancel()
	ans, err := resolveUses(ctx, root, symbol, spawnAcquire, lspBridgeSettle)
	if err != nil {
		return "", err
	}
	if max <= 0 {
		max = graph.DefaultMax
	}
	infos := graph.AnnotateRefs(root, ans.name, ans.sites)

	var b strings.Builder
	files := map[string]bool{}
	tests := map[string]bool{}
	for i, s := range ans.sites {
		files[s.File] = true
		if graph.IsTestPath(s.File) {
			tests[s.File] = true
		}
		if i >= max {
			continue
		}
		label := ""
		if infos[i].Label != "" {
			label = " in " + infos[i].Label
		}
		rel := infos[i].Relation
		if rel == "" {
			rel = "reference"
		}
		fmt.Fprintf(&b, "- %s:%d%s (%s)\n", s.File, s.Line, label, rel)
	}
	if n := len(ans.sites) - max; n > 0 {
		fmt.Fprintf(&b, "... %d more (raise 'max')\n", n)
	}

	head := fmt.Sprintf("=== callers of '%s' (exact, %s) ===\n", ans.name, ans.server)
	if len(tests) > 0 {
		head += fmt.Sprintf("tests: %d files: %s\n", len(tests), strings.Join(sortedFiles(tests), ", "))
	}
	if len(ans.defs) >= 2 {
		head += fmt.Sprintf("NOTE: '%s' is declared in %d places; the language server resolved each one, so this list is exact (no need for 'Type.%s').\n",
			ans.name, len(ans.defs), ans.name)
	}
	tail := fmt.Sprintf("Total: %d references in %d files.\n", len(ans.sites), len(files))
	return head + b.String() + tail, nil
}

// lspDefinition responde "onde isso é declarado" com o Definition do servidor, que resolve pelo tipo.
func lspDefinition(root, symbol string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lspBridgeTimeout)
	defer cancel()
	var locs []lsp.Location
	defs, server, err := runExact(ctx, root, symbol, spawnAcquire, lspBridgeSettle,
		func(c *lsp.Client, abs string, pos lsp.Position) error {
			got, err := c.Definition(ctx, abs, pos)
			if err != nil {
				return reasonErr(err)
			}
			locs = append(locs, got...)
			return nil
		})
	if err != nil {
		return "", err
	}
	if len(locs) == 0 {
		return "", lsp.Unavailable("%s found no definition for '%s'", server, graph.LastName(symbol))
	}
	byLine := map[string]graph.DefSite{}
	for _, d := range defs {
		byLine[graph.SiteKey(graph.Site{File: d.File, Line: d.Line})] = d
	}
	seen := map[string]bool{}
	var sites []graph.Site
	for _, l := range locs {
		if s, ok := relSite(root, l); ok {
			if k := graph.SiteKey(s); !seen[k] {
				seen[k] = true
				sites = append(sites, s)
			}
		}
	}
	if len(sites) == 0 {
		return "", lsp.Unavailable("%s answered with locations outside the repository", server)
	}
	graph.SortSites(sites)

	name := graph.LastName(symbol)
	var b strings.Builder
	files := map[string]bool{}
	for _, s := range sites {
		files[s.File] = true
		if d, ok := byLine[graph.SiteKey(s)]; ok {
			fmt.Fprintf(&b, "- %s:%d-%d (%s)\n  %s\n", s.File, s.Line, d.Line, d.Kind, d.Sig)
			continue
		}
		fmt.Fprintf(&b, "- %s:%d\n", s.File, s.Line)
	}
	head := fmt.Sprintf("=== definition of '%s' (exact, %s) ===\n", name, server)
	tail := fmt.Sprintf("Total: %d definition(s) in %d file(s).\n", len(sites), len(files))
	return head + b.String() + tail, nil
}

// lspImpact dá o impacto de um SÍMBOLO com as referências exatas: os arquivos que o servidor diz que usam
// o nome, agrupados. Impacto de ARQUIVO continua no grafo (errNoAnchor): ali a pergunta é "quem referencia
// este arquivo", que é do índice por definição.
func lspImpact(root, target string, max int) (string, error) {
	// "impact" aceita também um ARQUIVO como alvo, e aí a pergunta é "quem referencia este arquivo" — que é
	// do índice por definição, não do servidor de linguagem. Um alvo com separador de caminho é arquivo.
	if strings.ContainsAny(target, `/\`) {
		return "", errNoAnchor
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspBridgeTimeout)
	defer cancel()
	ans, err := resolveUses(ctx, root, target, spawnAcquire, lspBridgeSettle)
	if err != nil {
		return "", err
	}
	var order []string
	per := map[string]int{}
	for _, s := range ans.sites {
		if _, ok := per[s.File]; !ok {
			order = append(order, s.File)
		}
		per[s.File]++
	}
	sortFiles(order)

	limit := lspImpactMaxFiles
	if max > 0 && max < limit {
		limit = max
	}
	shown := order
	if len(shown) > limit {
		shown = shown[:limit]
	}
	var b strings.Builder
	for _, f := range shown {
		fmt.Fprintf(&b, "- %s (%d refs)\n", f, per[f])
	}
	if n := len(order) - len(shown); n > 0 {
		fmt.Fprintf(&b, "... %d more files\n", n)
	}
	head := fmt.Sprintf("=== impact of '%s' (exact, %s) ===\n", ans.name, ans.server)
	tail := fmt.Sprintf("Total: %d files.\n", len(order))
	return head + b.String() + tail, nil
}

// warmUses são as referências exatas de um símbolo que acabou de ser editado, obtidas SEM iniciar servidor.
//
// A âncora aqui não vem do índice (o índice ainda tem o texto de antes da edição, e consultá-lo custaria uma
// varredura do repositório inteiro no meio de uma edição já gravada): vem da declaração NOVA, que quem chama
// acabou de parsear. O orçamento é de milissegundos e o ctx manda: um servidor quente responde, um frio ou
// ocupado degrada na hora para a resposta por nome.
func warmUses(ctx context.Context, root, abs, name string, line, col int) ([]graph.Site, string, error) {
	if col <= 0 {
		return nil, "", errNoAnchor
	}
	c, release, err := warmAcquire(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	defer release()
	server := c.Name()
	if err := c.WaitSettled(ctx, abs, editImpactLSPBudget); err != nil {
		return nil, "", reasonErr(err)
	}
	locs, err := c.References(ctx, abs, lsp.Position{Line: line - 1, Character: col - 1}, false)
	if err != nil {
		return nil, "", reasonErr(err)
	}
	var out []graph.Site
	seen := map[string]bool{}
	for _, l := range locs {
		if s, ok := relSite(root, l); ok {
			if k := graph.SiteKey(s); !seen[k] {
				seen[k] = true
				out = append(out, s)
			}
		}
	}
	graph.SortSites(out)
	return out, server, nil
}

// courseWithGraph roda o caminho exato e, quando ele não fecha, entrega a resposta por nome somada da nota
// de degradação — que só aparece se havia um servidor para ser usado (ver errNoAnchor).
func courseWithGraph(root, symbol string, exact func() (string, error), byName func() (string, error)) (string, error) {
	out, err := exact()
	if err == nil {
		return out, nil
	}
	fallback, ferr := byName()
	if ferr != nil {
		return "", ferr
	}
	return fallback + lspNote(err), nil
}

// lspNote é a linha honesta que acompanha a resposta por nome. Silêncio quando não havia âncora: um
// repositório de linguagem sem servidor não está degradado, está apenas sendo respondido pelo grafo.
func lspNote(err error) string {
	if err == nil || errors.Is(err, errNoAnchor) {
		return ""
	}
	return "\n" + lsp.Degraded(err)
}

// sortFiles ordena nomes de arquivo como o grafo ordena sites: testes no fim, depois alfabético.
func sortFiles(files []string) {
	sites := make([]graph.Site, 0, len(files))
	for _, f := range files {
		sites = append(sites, graph.Site{File: f})
	}
	graph.SortSites(sites)
	for i, s := range sites {
		files[i] = s.File
	}
}

// sortedFiles devolve uma cópia ordenada de um conjunto de arquivos.
func sortedFiles(set map[string]bool) []string {
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sortFiles(files)
	return files
}
