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
	"strconv"
	"strings"
	"time"

	"homelab/fastexec/internal/codescan"
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
	// impactUsesPerFile é o teto de usos LISTADOS de cada arquivo. O teto de `impact` é em arquivos, e um
	// símbolo usado cem vezes num arquivo só não pode virar cem linhas: os primeiros já dizem onde mexer, e o
	// resto sai como contagem explícita — nunca resumido num "N refs" que esconde o segundo ponto de edição.
	impactUsesPerFile = 3
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
	rel    []string     // relações que o servidor resolveu (implementa/implementado por), se houver
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
	fn func(c *lsp.Client, abs string, d graph.DefSite) error) ([]graph.DefSite, string, error) {

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
		lang, ok := lsp.LanguageForPath(abs)
		if !ok {
			return nil, "", errNoAnchor
		}
		// Linguagem opcional (Markdown, JSON, YAML, Shell, PowerShell) cujo servidor não está instalado: a
		// resposta por nome também já é a resposta final. Sem esta linha, um `callers` em Markdown ganharia
		// uma nota mandando instalar um servidor que ninguém pediu — e o .md é indexado pelo grafo.
		if lang.Optional && !lsp.Installed(lang) {
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
	fn func(c *lsp.Client, abs string, d graph.DefSite) error) error {

	if err := c.WaitSettled(ctx, abs, settle); err != nil {
		return reasonErrFrom(c, err)
	}
	if d.Col == 0 {
		// O arquivo mudou desde a indexação, ou a declaração é gerada, e o nome não está na linha que o
		// índice aponta. Chutar a coluna 1 devolveria a resposta de outro símbolo.
		return lsp.Unavailable("declaration of '%s' is not where the index says (%s:%d)", name, d.File, d.Line)
	}
	return fn(c, abs, d)
}

// reasonErrFrom é o reasonErr com o servidor à mão: a nota de "não carregou" ganha a causa que o próprio
// servidor escreveu no stderr e a ação concreta daquela linguagem.
//
// Sem isso o usuário recebe o sintoma e nenhuma pista, e a pista existe: o servidor a imprimiu antes de
// desistir. Só a última linha que parece causa entra, e só neste ramo — nas outras degradações o FastExec
// já sabe dizer o motivo, e a fala do servidor ali só gastaria token.
//
// A ação vem do internal/lsp (LoadAction), que refaz na hora as checagens do lançamento: para C# isso é
// "restore needed: run dotnet restore (App.csproj)" ou o global.json que não bate, em vez de um "veja o
// projeto" que não diz o que fazer. As checagens só leem o disco, então a nota continua barata.
func reasonErrFrom(c *lsp.Client, err error) error {
	out := reasonErr(err)
	if c == nil || !errors.Is(err, lsp.ErrLoading) {
		return out
	}
	// O *LoadingError já traz a nota dele ("lsp loading C# solution…"): quem chega aqui como ErrLoading
	// puro é o servidor que parou de carregar sem anunciar progresso nenhum.
	var le *lsp.LoadingError
	if errors.As(err, &le) {
		return out
	}
	cause := lsp.StderrCause(c.Stderr())
	act := lsp.LoadAction(c.LanguageID(), c.Root())
	if cause == "" && act == "" {
		return out
	}
	note := "project did not load"
	if cause != "" {
		note += ": " + cause
	}
	if act != "" {
		note += "; " + act
	}
	return lsp.Unavailable("%s", note)
}

// reasonErr reduz o erro do servidor à frase curta que cabe na nota de degradação.
func reasonErr(err error) error {
	var ue *lsp.UnavailableError
	var le *lsp.LoadingError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ue):
		return err
	case errors.As(err, &le):
		// Um projeto AINDA carregando passa intacto: a nota que ele produz diz o que está acontecendo
		// ("lsp loading C# solution…; by name") e somezinha da próxima consulta, enquanto "não carregou"
		// mandaria o usuário procurar defeito onde não tem.
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
//
// Devolve false também para arquivo gerado (bin/obj de .NET, *.g.cs, *.designer.cs): o servidor responde
// por eles — o Roslyn os analisa —, mas o texto é saída de build, não código que alguém mantenha. O índice
// por nome já os ignora, e uma resposta "exata" que listasse obj/Debug/... contradiria a resposta por nome
// que ela vem substituir. O filtro é o do codescan, para os dois lados cortarem o mesmo conjunto.
func relSite(root string, l lsp.Location) (graph.Site, bool) {
	p := lsp.URItoPath(l.URI)
	if p == "" {
		return graph.Site{}, false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return graph.Site{}, false
	}
	rel = filepath.ToSlash(rel)
	if codescan.IsNoisePath(rel) {
		return graph.Site{}, false
	}
	return graph.Site{File: rel, Line: l.Range.Start.Line + 1}, true
}

// resolveUses devolve as referências exatas do símbolo.
func resolveUses(ctx context.Context, root, symbol string, acquire acquireFunc, settle time.Duration) (*exactAnswer, error) {
	var got []graph.Site
	var rel []string
	probe := newRelProbe(root)
	defs, server, err := runExact(ctx, root, symbol, acquire, settle,
		func(c *lsp.Client, abs string, d graph.DefSite) error {
			locs, err := c.References(ctx, abs, lsp.Position{Line: d.Line - 1, Character: d.Col - 1}, false)
			if err != nil {
				return reasonErrFrom(c, err)
			}
			for _, l := range locs {
				if s, ok := relSite(root, l); ok {
					got = append(got, s)
				}
			}
			// Relação é complemento, nunca motivo de falha: um servidor que não responde isto ainda respondeu
			// as referências, e a lista de usos continua exata.
			rel = append(rel, usesFacts(ctx, probe, c, abs, d)...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	out := &exactAnswer{name: graph.LastName(symbol), server: server, rel: dedupeLines(rel)}
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
	if len(ans.sites) == 0 {
		// An "exact" empty list is only believable when the index does not see a use
		// either. A server whose project failed to load (a solution it could not
		// restore, a target it has no pack for) answers every query with nothing,
		// and reporting that as "no references" would be a confident wrong answer:
		// the name-based answer is better, so degrade to it and say why.
		if byName, nerr := graph.Callers(root, symbol, 1, 1); nerr == nil && !strings.Contains(byName, "Total: 0 ") &&
			strings.Contains(byName, "Total: ") {
			return "", lsp.Unavailable("%s returned no references but the index finds uses by name (project not fully loaded?)", ans.server)
		}
	}
	if max <= 0 {
		max = graph.DefaultMax
	}
	infos := graph.AnnotateRefs(root, ans.name, ans.sites)

	var b strings.Builder
	files := map[string]bool{}
	tests := map[string]bool{}
	var kept []int
	for i, s := range ans.sites {
		files[s.File] = true
		if graph.IsTestPath(s.File) {
			tests[s.File] = true
		}
		if i >= max {
			continue
		}
		kept = append(kept, i)
	}
	if len(ans.sites) > groupCallersOver {
		writeGroupedSites(&b, ans.sites, infos, kept)
	} else {
		for _, i := range kept {
			s := ans.sites[i]
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
	}
	if n := len(ans.sites) - max; n > 0 {
		fmt.Fprintf(&b, "... %d more (raise 'max')\n", n)
	}

	head := fmt.Sprintf("=== callers of '%s' (exact, %s) ===\n", ans.name, ans.server)
	// A relação vem ANTES da lista: quem mexe em uma interface precisa saber quem a implementa antes de ler
	// as referências — em Go o tipo que a satisfaz não referencia o nome dela em lugar nenhum.
	head += strings.Join(ans.rel, "")
	if len(tests) > 0 {
		head += fmt.Sprintf("tests: %d files: %s\n", len(tests), strings.Join(sortedFiles(tests), ", "))
	}
	if len(ans.defs) >= 2 {
		head += fmt.Sprintf("NOTE: '%s' has %d declarations, all resolved: the list is exact (no 'Type.%s' needed).\n",
			ans.name, len(ans.defs), ans.name)
	}
	tail := fmt.Sprintf("Total: %d references in %d files.\n", len(ans.sites), len(files))
	return head + b.String() + tail, nil
}

// groupCallersOver: acima disto a lista exata sai agrupada por arquivo. Uma linha por uso repete o caminho dezenas
// de vezes; agrupada, a mesma informação (cada linha e o membro que a contém) ocupa menos da metade e já serve de
// lista de arquivos, a pergunta mais comum depois de "quem usa isto".
const groupCallersOver = 20

// writeGroupedSites escreve `- arquivo (N): 42,58 in Membro; 79 in Outro` para os usos mantidos, na ordem em que
// chegaram (já ordenados por arquivo e linha). Uma relação que não é a comum (`reference`) acompanha o grupo.
func writeGroupedSites(b *strings.Builder, sites []graph.Site, infos []graph.RefInfo, kept []int) {
	groupKey := func(i int) string {
		k := infos[i].Label
		if r := infos[i].Relation; r != "" && r != "reference" {
			k += " (" + r + ")"
		}
		return k
	}
	for a := 0; a < len(kept); {
		file := sites[kept[a]].File
		z := a
		for z < len(kept) && sites[kept[z]].File == file {
			z++
		}
		fmt.Fprintf(b, "- %s (%d): ", file, z-a)
		for k := a; k < z; {
			key := groupKey(kept[k])
			m := k
			var lines []string
			for m < z && groupKey(kept[m]) == key {
				lines = append(lines, strconv.Itoa(sites[kept[m]].Line))
				m++
			}
			if k > a {
				b.WriteString("; ")
			}
			b.WriteString(strings.Join(lines, ","))
			if key != "" {
				b.WriteString(" in " + key)
			}
			k = m
		}
		b.WriteString("\n")
		a = z
	}
}

// lspDefinition responde "onde isso é declarado" com o Definition do servidor, que resolve pelo tipo.
func lspDefinition(root, symbol string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lspBridgeTimeout)
	defer cancel()
	var locs []lsp.Location
	// O que o servidor sabe de cada declaração ancorada: a assinatura resolvida (o tipo concreto de um
	// genérico não está no texto indexado) e as relações com os tipos que implementam ou são implementados.
	sigOf := map[string]string{}
	factsOf := map[string][]string{}
	// O fim da faixa de cada declaração: o `def` responde o SÍMBOLO, e o símbolo tem tamanho. Sem isto a
	// resposta saía "344-344" para um método de 180 linhas, que é a mesma pergunta respondida pela metade.
	endOf := map[string]int{}
	probe := newRelProbe(root)
	defs, server, err := runExact(ctx, root, symbol, spawnAcquire, lspBridgeSettle,
		func(c *lsp.Client, abs string, d graph.DefSite) error {
			pos := lsp.Position{Line: d.Line - 1, Character: d.Col - 1}
			got, err := c.Definition(ctx, abs, pos)
			if err != nil {
				return reasonErrFrom(c, err)
			}
			locs = append(locs, got...)
			key := graph.SiteKey(graph.Site{File: d.File, Line: d.Line})
			sigOf[key] = defSignature(ctx, c, abs, pos, d.Sig)
			factsOf[key] = defFacts(ctx, probe, c, abs, d)
			endOf[key] = defEndLine(probe.treeOf(ctx, c, abs), pos)
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
		key := graph.SiteKey(s)
		if d, ok := byLine[key]; ok {
			sig := d.Sig
			if v := sigOf[key]; v != "" {
				sig = v
			}
			// O começo é a linha que o SERVIDOR devolveu (é a declaração, não o comentário acima dela); o fim
			// vem da árvore de símbolos e, sem ela, cai na âncora do índice — a linha única de antes.
			end := d.Line
			if v := endOf[key]; v > end {
				end = v
			}
			fmt.Fprintf(&b, "- %s:%d-%d (%s)\n  %s\n", s.File, s.Line, end, d.Kind, sig)
			for _, l := range factsOf[key] {
				b.WriteString("  " + l)
			}
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
	seen := map[string]bool{}
	byFile := map[string][]int{} // índices, em ans.sites, dos usos de cada arquivo
	for i, s := range ans.sites {
		if !seen[s.File] {
			seen[s.File] = true
			order = append(order, s.File)
		}
		byFile[s.File] = append(byFile[s.File], i)
	}
	sortFiles(order)
	// A linha sozinha ("- arquivo.cs (1 refs)") obriga a abrir o arquivo para saber ONDE mexer, o que é
	// exatamente o custo que a resposta exata existe para evitar. O formato passa a ser o do `callers`:
	// arquivo:linha, o símbolo que contém o uso e a relação dele. O agrupamento por arquivo continua (o teto
	// é em arquivos, como no grafo), e os usos de um mesmo arquivo saem um a um — o segundo uso de um símbolo
	// costuma estar em OUTRA função, e escondê-lo atrás de "2 refs" esconderia metade do impacto.
	infos := graph.AnnotateRefs(root, ans.name, ans.sites)

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
		idxs := byFile[f]
		extra := 0
		if n := len(idxs); n > impactUsesPerFile {
			idxs, extra = idxs[:impactUsesPerFile], n-impactUsesPerFile
		}
		for _, i := range idxs {
			s := ans.sites[i]
			label := ""
			if i < len(infos) && infos[i].Label != "" {
				label = " in " + infos[i].Label
			}
			rel := "reference"
			if i < len(infos) && infos[i].Relation != "" {
				rel = infos[i].Relation
			}
			fmt.Fprintf(&b, "- %s:%d%s (%s)\n", f, s.Line, label, rel)
		}
		if extra > 0 {
			word := "uses"
			if extra == 1 {
				word = "use"
			}
			fmt.Fprintf(&b, "... %d more %s in this file\n", extra, word)
		}
	}
	if n := len(order) - len(shown); n > 0 {
		fmt.Fprintf(&b, "... %d more files\n", n)
	}
	head := fmt.Sprintf("=== impact of '%s' (exact, %s) ===\n", ans.name, ans.server)
	head += strings.Join(ans.rel, "")
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
		return nil, "", reasonErrFrom(c, err)
	}
	locs, err := c.References(ctx, abs, lsp.Position{Line: line - 1, Character: col - 1}, false)
	if err != nil {
		return nil, "", reasonErrFrom(c, err)
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
