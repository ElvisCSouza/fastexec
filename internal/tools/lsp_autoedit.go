// lsp_autoedit.go: o que o VS Code faria depois de uma edição, feito pelo FastExec quando há servidor de
// linguagem para o arquivo.
//
// A régua é "se o VS pode, o FastExec tem que poder": quem edita uma declaração por nome espera que as outras
// ocorrências, os imports e os chamadores se ajustem sozinhos. Só que o FastExec não tem um humano olhando o
// diff: cada passo aqui é automático e por isso condicionado — o que o servidor devolve é aceito apenas quando
// bate com a edição que foi pedida, e o que não bate é reportado, nunca gravado.
//
// Duas fases, separadas pela gravação do arquivo editado, porque as perguntas ao servidor têm lados diferentes:
//
//	A (antes): renomear, mudar assinatura, listar órfãos de um símbolo removido, e tirar o "antes" dos
//	           diagnósticos. O disco ainda tem o texto velho, que é o que dá posição válida ao servidor.
//	B (depois): o que só faz sentido sobre o texto novo — import que faltou, erro que a edição criou — e a
//	            formatação do trecho.
//
// Nada aqui pode custar a edição: toda falha vira silêncio ou uma linha de aviso. Sem servidor, ou com
// FASTEXEC_LSP=off, a função devolve nil e o caminho antigo (aviso por nome) segue igual.
package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

const (
	// lspAutoRenameMaxFiles: acima disso o rename não é aplicado, só dimensionado. O servidor sabe renomear
	// 400 arquivos; a resposta ao modelo é que não caberia, e um rename desse tamanho merece uma decisão
	// explícita em vez de um efeito colateral de edit_file.
	lspAutoRenameMaxFiles = 100
	// lspAutoRenameBudget cobre spawn do servidor + carga do projeto + PrepareRename + Rename: em Go, o gopls
	// só responde rename depois de carregar os pacotes, e isso é segundos, não centenas de ms.
	lspAutoRenameBudget = 25 * time.Second
	// lspAutoRefBudget vale para pergunta simples (references) com servidor já de pé.
	lspAutoRefBudget = 10 * time.Second
	// lspAutoDiagBudget é o teto dos diagnósticos: passou disso, a resposta sai sem eles (a edição já está
	// gravada e não se espera por diagnóstico).
	lspAutoDiagBudget   = 1500 * time.Millisecond
	lspAutoFormatBudget = 250 * time.Millisecond
	// lspAutoBaseBudget é o "antes" dos diagnósticos quando o servidor nunca publicou nada deste arquivo. É
	// curto de propósito: é um pedido a um servidor que já está de pé, e esperar mais que isso atrasaria a
	// própria edição, que já foi gravada.
	lspAutoBaseBudget = 500 * time.Millisecond
	// lspAutoMaxLines: o rodapé inteiro cabe em três linhas.
	lspAutoMaxLines = 3
	lspAutoMaxSpots = 2
	lspAutoMaxDiags = 3
	lspAutoMaxQuick = 2
)

// autoWarm e autoSpawn são como esta fase obtém um servidor. São variáveis, e não chamadas diretas, para que o
// teste possa entregar um servidor roteirizado: o caminho de produção é sempre lsp.Manager() (lsp_bridge.go).
var (
	autoWarm  acquireFunc = warmAcquire
	autoSpawn acquireFunc = spawnAcquire
)

// declKind diz o que a edição fez com as declarações do arquivo. É o que decide qual recurso do servidor vale
// a pena pedir.
type declKind int

const (
	declNone    declKind = iota
	declRenamed          // só o identificador de UMA declaração mudou
	declSigned           // a assinatura de UMA declaração mudou, o nome não
	declRemoved          // UMA declaração sumiu
)

// declDelta é a diferença de declarações entre o texto velho e o novo. line/col são do texto VELHO — é nele
// que o servidor vai ser ancorado.
type declDelta struct {
	kind    declKind
	name    string
	newName string
	sig     string
	newSig  string
	line    int
	endLine int
	col     int // UTF-16 1-based, o que o LSP usa
	// extra: além do nome, a edição mexeu num trecho contíguo junto da declaração (o comentário dela, por exemplo).
	// O rename automático aplica o texto do servidor e depois repõe esse trecho (mergeResidual).
	extra bool
}

// autoEdit é o estado de uma edição assistida. nil é um estado válido e o normal: sem servidor, sem edição
// local, sem delta. Todos os métodos aguentam receptor nil para o chamador não ter que perguntar.
type autoEdit struct {
	host    string
	path    string
	root    string
	abs     string
	rel     string
	oldText string
	newText string

	delta   declDelta
	tract   [2]int // 0-based, inclusivo, com folga de uma linha
	client  *lsp.Client
	release func()

	beforeMsg map[string]bool // mensagens já publicadas antes da edição
	diagnosed bool            // o servidor já olhou este arquivo alguma vez
	post      string          // texto que o servidor quer no arquivo editado, quando ele aceitou a edição
	skip      map[string]bool // nomes que o aviso por nome NÃO deve repetir (o exato já respondeu)
	phaseB    bool            // a fase B começou: o diagnóstico novo pode usar a última linha
	undo      string          // patch anunciado ao modelo; precisa ser reescrito no fim (a formatação vem depois)
	lines     []string

	// validação em tempo real (validate.go)
	isNew          bool                // o arquivo não existia: todo erro conta como novo e o arquivo inteiro é validado
	noRoot         bool                // nem git nem marcador de projeto: só a camada 1 (sintaxe)
	lang           string              // id da linguagem (lsp.Language.ID)
	extra          []string            // linhas de erro/estado; não entram no teto das notas, são o que a LLM precisa ler
	deps           map[string]*depState // arquivos que a edição pode ter quebrado (D8), com o "antes" de cada um
	depsUnverified int                 // quantos desses o servidor não respondeu a tempo
}

// autoEditBegin abre uma edição assistida e roda a fase A. Devolve nil quando não há nada exato a fazer — e aí
// a resposta da tool fica idêntica à de antes desta tarefa.
func autoEditBegin(host, path, oldText, newText string) *autoEdit {
	if newText == "" || oldText == newText || lsp.Disabled() || !impactLocal(host, path) {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	// O texto que está sendo gravado já é o que o servidor pediu numa assistência anterior: não há nada a
	// completar. Sem esta saída, o write_file interno do edit_file (que reescreve o mesmo arquivo com o texto
	// do servidor) rodaria a fase A de novo e renomearia duas vezes o que já está renomeado.
	if isAssisted(host, abs, newText) {
		return nil
	}
	lang, ok := lsp.LanguageForPath(abs)
	if !ok {
		return nil
	}
	isNew := oldText == ""
	if isNew && lang.Optional && !lsp.Installed(lang) {
		return nil // Markdown, JSON, YAML...: sem servidor instalado não há nada a validar num arquivo novo
	}
	root, hasRoot := graph.ResolveRepoRoot(abs)
	if !hasRoot {
		// Sem git a pasta ainda pode ser, de forma determinística, um projeto: .sln/.csproj, go.mod, package.json...
		if r, ok := lsp.RootMarked(lang, abs); ok {
			root, hasRoot = r, true
		}
	}
	if !hasRoot && !syntaxLanguage(abs) {
		return nil
	}
	a := &autoEdit{host: host, path: path, abs: abs, root: root, oldText: oldText, newText: newText,
		isNew: isNew, noRoot: !hasRoot, lang: lang.ID}
	if !hasRoot {
		return a // fora de repositório só há a camada 1
	}
	a.rel = relToRoot(root, abs)
	if isNew {
		a.tract = [2]int{0, 1 << 30} // arquivo novo: tudo é trecho editado
		if c, release, err := autoWarm(context.Background(), abs); err == nil {
			a.lease(c, release)
		}
		return a
	}
	a.delta = declDeltaOf(a.rel, oldText, newText)
	a.tract = tractRange(oldText, newText)

	// Servidor quente primeiro: se já existe um para este projeto, o "antes" dos diagnósticos sai de graça.
	if c, release, err := autoWarm(context.Background(), abs); err == nil {
		a.lease(c, release)
		a.baseline()
	}
	if a.delta.kind != declNone && a.client == nil {
		if a.spawn(lspAutoRenameBudget) {
			a.baseline()
		}
	}
	switch a.delta.kind {
	case declRenamed:
		a.rename()
	case declSigned:
		a.signature()
	case declRemoved:
		a.orphans()
	}
	return a
}

func (a *autoEdit) lease(c *lsp.Client, release func()) {
	if a.release != nil {
		a.release()
	}
	a.client, a.release = c, release
}

// spawn sobe o servidor do projeto do arquivo. O "dono do projeto" do contrato é exatamente isto: o servidor
// subiu e carregou a raiz sem erro; se não subiu, nada é aplicado.
func (a *autoEdit) spawn(budget time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	c, release, err := autoSpawn(ctx, a.abs)
	if err != nil {
		return false
	}
	a.lease(c, release)
	// Esperar o primeiro publish aqui é o que separa um erro antigo de um erro novo mais adiante.
	_ = c.WaitSettled(ctx, a.abs, budget/2)
	return true
}

// baseline guarda o estado dos diagnósticos ANTES de qualquer byte mudar: sem ele, "erro novo" não tem
// significado.
func (a *autoEdit) baseline() {
	if a.client == nil || a.beforeMsg != nil {
		return
	}
	a.beforeMsg = map[string]bool{}
	for _, d := range a.client.Published(a.abs) {
		a.beforeMsg[d.Message] = true
	}
	a.diagnosed = a.client.Diagnosed(a.abs)
	// O push só cobre o que o servidor já abriu. Num arquivo que ele nunca viu não havia "antes" nenhum, e aí
	// um erro novo fora do trecho editado era descartado como se fosse antigo — o build quebrava e a resposta
	// não dizia nada. Perguntar agora, com o texto velho no disco, dá o "antes" que faltava — nos dois
	// caminhos: no pull o servidor responde na hora, no push a pergunta reenvia o documento e espera o
	// publish dele.
	if !a.diagnosed {
		ctx, cancel := context.WithTimeout(context.Background(), lspAutoBaseBudget)
		defer cancel()
		// Fresh: o "antes" de um publish que não é da versão atual do arquivo não é "antes" nenhum.
		if items, ok, err := a.client.DiagnosticsFresh(ctx, a.abs, lspAutoBaseBudget); err == nil && ok {
			a.diagnosed = true
			for _, d := range items {
				a.beforeMsg[d.Message] = true
			}
		}
	}
}

// Text devolve o texto que deve ir para o disco. Em geral é o que o chamador pediu; depois de um rename
// aceito, é a versão do servidor — que já traz renomeadas as ocorrências dentro do próprio arquivo.
func (a *autoEdit) Text(fallback string) string {
	if a == nil || a.post == "" {
		return fallback
	}
	return a.post
}

// SkipNames são os nomes que o aviso por nome não deve mais repetir, porque o exato já respondeu por eles.
func (a *autoEdit) SkipNames() map[string]bool {
	if a == nil {
		return nil
	}
	return a.skip
}

// Note devolve as linhas [lsp] já montadas, prontas para entrar na resposta da tool.
func (a *autoEdit) Note() string {
	if a == nil || len(a.lines)+len(a.extra) == 0 {
		return ""
	}
	return "\n" + strings.Join(append(append([]string(nil), a.lines...), a.extra...), "\n")
}

// Close devolve o servidor ao gerente. Nunca é o que decide se a edição fica: ela já foi gravada.
func (a *autoEdit) Close() {
	if a == nil || a.release == nil {
		return
	}
	a.release()
	a.release, a.client = nil, nil
}

func (a *autoEdit) note(format string, args ...any) {
	// A fase A reserva a última linha: o erro que a edição criou é o aviso que não pode ser cortado pelo teto.
	// Sem a reserva, "renomeou + arquivos mudados" já enchia as três linhas e o build quebrado saía calado.
	max := lspAutoMaxLines
	if a.client != nil && !a.phaseB {
		max = lspAutoMaxLines - 1
	}
	if len(a.lines) >= max {
		return
	}
	a.lines = append(a.lines, "[lsp] "+fmt.Sprintf(format, args...))
}

// rename é o item (1): aplicar o WorkspaceEdit do servidor, não só listar.
func (a *autoEdit) rename() {
	if a.client == nil {
		a.note("%s -> %s: no language server for this project; not renamed (by name)", a.delta.name, a.delta.newName)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspAutoRenameBudget)
	defer cancel()
	pos := lsp.Position{Line: a.delta.line - 1, Character: a.delta.col - 1}
	// PrepareRename primeiro: é ele que diz se o símbolo é renomeável aqui e qual o range exato. Ancorar no
	// que o servidor aponta evita renomear o homônimo errado.
	rng, _, err := a.client.PrepareRename(ctx, a.abs, pos)
	if err != nil {
		a.note("%s -> %s not applied: %s", a.delta.name, a.delta.newName, shortErr(err))
		return
	}
	if rng == nil {
		a.note("%s -> %s not applied: the server does not rename this symbol; by name", a.delta.name, a.delta.newName)
		return
	}
	we, err := a.client.Rename(ctx, a.abs, rng.Start, a.delta.newName)
	if err != nil || we == nil {
		if err != nil {
			a.note("%s -> %s not applied: %s", a.delta.name, a.delta.newName, shortErr(err))
		}
		return
	}

	// O servidor é aceito só quando a versão dele do arquivo editado é o texto novo a menos dos dois nomes.
	// É isso que protege o homônimo: se o servidor renomeou o símbolo errado, o texto dele não bate e nada é
	// gravado.
	post, ok, err := we.ResultFor(a.abs, a.oldText)
	if ok && err == nil && a.delta.extra {
		post, ok = mergeResidual(post, a.oldText, a.newText, a.delta.name, a.delta.newName, a.delta.line)
	}
	if err != nil || !ok || !maskProof(a.newText, post, a.delta.name, a.delta.newName) {
		a.note("%s -> %s not applied: the server's edit does not match the change asked; by name", a.delta.name, a.delta.newName)
		return
	}
	files := we.Files()
	if len(files) > lspAutoRenameMaxFiles {
		a.note("%s -> %s: %d files would change, above the limit of %d; not applied (by name)",
			a.delta.name, a.delta.newName, len(files), lspAutoRenameMaxFiles)
		return
	}

	// A sujeira pré-existente precisa ser lida ANTES de aplicar, senão o que o rename grava entra na conta.
	// E o que ESTA sessão já gravou não é "por fora": é trabalho do próprio FastExec.
	dirty := graph.GitDirty(a.root, files)
	var outside []string
	for _, f := range files {
		if !wroteThisSession(a.host, f) {
			if rel := relToRoot(a.root, f); rel != "" && dirty[rel] {
				outside = append(outside, rel)
			}
		}
	}

	n, err := we.Sizes()
	if err != nil {
		n = nil
	}
	a.captureDeps(a.sitesAbs(files)) // D8: depois do rename exato, os arquivos que ele tocou são conferidos
	for _, f := range files {
		captureUndo(f) // o desfazer em memória (lastedit.go) precisa do conteúdo de antes de cada arquivo que o rename vai gravar
	}
	res, err := a.client.ApplyEdit(we, lsp.ApplyOptions{Root: a.root})
	if err != nil {
		a.note("%s -> %s not applied: %s", a.delta.name, a.delta.newName, shortErr(err))
		return
	}
	a.post = post
	rememberAssisted(a.host, a.abs, post)
	for _, f := range files {
		noteWrite(a.host, f)
	}
	total := 0
	for _, c := range n {
		total += c
	}
	if total == 0 {
		total = len(files)
	}
	a.skip = map[string]bool{a.delta.name: true, a.delta.newName: true, "*": true}
	a.undo = res.PatchPath

	spots, _ := we.Spots()
	sites := relSites(a.root, spots)
	a.note("DONE %s -> %s: all %d uses in %d files updated by the compiler's rename (exact; same-named members of other types untouched): %s%s. Nothing else to change: do not grep or re-check. undo: exec '%s'",
		a.delta.name, a.delta.newName, total, len(files), joinSites(firstSites(sites, lspAutoMaxSpots)), more(sites, lspAutoMaxSpots), UndoCommand)
	a.changedLine(files, outside)
}

// changedLine avisa quais OUTROS arquivos mudaram — sem isso o modelo continua editando sobre uma cópia que
// não existe mais.
func (a *autoEdit) changedLine(files []string, outside []string) {
	if len(a.lines) >= lspAutoMaxLines-1 || len(files) <= 1 {
		return
	}
	var rel []string
	for _, f := range files {
		if r := relToRoot(a.root, f); r != "" && r != a.rel {
			rel = append(rel, r)
		}
	}
	if len(rel) == 0 {
		return
	}
	sort.Strings(rel)
	line := fmt.Sprintf("files changed: %s; re-read before editing them", joinFirst(rel, 3))
	if n := len(rel) - 3; n > 0 {
		line = fmt.Sprintf("files changed: %s, +%d more; re-read before editing them", joinFirst(rel, 3), n)
	}
	if len(outside) > 0 {
		sort.Strings(outside)
		line += fmt.Sprintf("; already modified outside FastExec: %s", joinFirst(outside, 3))
	}
	a.note("%s", line)
}

// signature é o item (2): aplicar a mudança de assinatura quando o servidor a oferece pronta; senão, entregar a
// lista exata de quem precisa de ajuste.
func (a *autoEdit) signature() {
	name := a.delta.name
	if a.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspAutoRefBudget)
	defer cancel()
	if a.signatureAction(ctx) {
		return
	}
	pos := lsp.Position{Line: a.delta.line - 1, Character: a.delta.col - 1}
	locs, err := a.client.References(ctx, a.abs, pos, false)
	if err != nil {
		return
	}
	// O chamador do PRÓPRIO arquivo conta: quem chama a assinatura antiga dentro do arquivo editado também
	// quebra o build, e antes era justamente ele que ficava de fora do aviso.
	sites := relSites(a.root, locs)
	sites = withoutDecl(sites, a.rel, a.delta.line)
	a.captureDeps(a.sitesAbs(siteFiles(sites))) // é o "antes" dos chamadores, para o D8 dizer depois o que quebrou
	a.skip = map[string]bool{name: true, "*": true}
	if len(sites) == 0 {
		a.note("%s: signature changed, no caller (exact): nothing else to update", name)
		return
	}
	a.note("%s: signature changed; exactly %d caller(s) need the new arguments (complete list, no grep needed): %s%s",
		name, len(sites), joinSites(firstSites(sites, lspAutoMaxSpots)), more(sites, lspAutoMaxSpots))
}

// signatureAction procura a ação de refatoração que muda a assinatura e a aplica. Vale para servidores que
// expõem isso como edição pronta; o gopls não expõe (a ação dele é interativa), e aí a lista exata responde.
func (a *autoEdit) signatureAction(ctx context.Context) bool {
	rng := lsp.Range{
		Start: lsp.Position{Line: a.delta.line - 1, Character: a.delta.col - 1},
		End:   lsp.Position{Line: a.delta.line - 1, Character: a.delta.col - 1 + len(a.delta.name)},
	}
	actions, err := a.client.CodeActions(ctx, a.abs, rng, []string{"refactor"})
	if err != nil {
		return false
	}
	for _, act := range actions {
		if act.Edit == nil || !strings.Contains(strings.ToLower(act.Title), "signature") {
			continue
		}
		files := act.Edit.Files()
		if len(files) == 0 {
			continue
		}
		for _, f := range files {
			captureUndo(f)
		}
		res, err := a.client.ApplyEdit(act.Edit, lsp.ApplyOptions{Root: a.root})
		if err != nil {
			continue
		}
		for _, f := range files {
			noteWrite(a.host, f)
		}
		a.skip = map[string]bool{a.delta.name: true}
		a.undo = res.PatchPath
		a.note("%s: signature change applied by the server (exact): %s; undo: exec '%s'",
			a.delta.name, act.Title, UndoCommand)
		a.changedLine(files, nil)
		return true
	}
	return false
}

// orphans é o item (6): a declaração sumiu, quem ainda a usa ficou órfão. Os usos exatos substituem a
// heurística por nome, que continua sendo o caminho quando não há servidor.
func (a *autoEdit) orphans() {
	if a.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspAutoRefBudget)
	defer cancel()
	pos := lsp.Position{Line: a.delta.line - 1, Character: a.delta.col - 1}
	locs, err := a.client.References(ctx, a.abs, pos, false)
	if err != nil {
		return
	}
	sites := withoutFile(relSites(a.root, locs), a.rel)
	a.captureDeps(a.sitesAbs(siteFiles(sites)))
	a.skip = map[string]bool{a.delta.name: true}
	if len(sites) == 0 {
		a.note("%s removed: no use left outside the file (exact)", a.delta.name)
		return
	}
	a.note("%s removed but still used %d time(s) (exact): %s%s", a.delta.name, len(sites),
		joinSites(firstSites(sites, lspAutoMaxSpots)), more(sites, lspAutoMaxSpots))
}

// Finish é a fase B, depois da gravação: erro novo, import que faltou e formatação do trecho. Devolve o rodapé
// já com o "\n" inicial, ou "" quando não há nada a dizer — e aí a resposta fica byte a byte igual à de antes.
func (a *autoEdit) Finish() string {
	if a == nil {
		return ""
	}
	text := a.Text(a.newText)
	s1 := a.layer1(text)
	sem := semNone
	if !s1.blocked { // com erro de sintaxe a semântica é ruído em cascata: nem se pergunta
		sem = a.layer2Result(text, s1)
	}
	if line := a.statusLine(s1, sem); line != "" {
		a.extra = append(a.extra, line)
	}
	if sem == semPending {
		defer a.handOff(text) // a resposta sai agora; o diagnóstico desta versão chega na próxima chamada
	}
	// O patch anunciado antes da formatação descreveria um arquivo que não existe mais no disco: reescrever
	// aqui é o que faz o `git apply -R` do aviso devolver o original.
	if a.undo != "" {
		_ = lsp.RefreshPatch(a.undo, a.root, a.abs, a.oldText) // o patch não é mais anunciado; fica como registro em disco
	}
	return a.Note()
}

// freshErrors são os erros que a edição criou. Um erro que já estava lá não entra; uma mensagem que o servidor
// já publicava também não — o número da linha pode ter andado, a mensagem não.
func (a *autoEdit) freshErrors(ctx context.Context) ([]lsp.Diagnostic, bool) {
	// Versão certa ou nada (D4): o que o servidor publicou sobre uma versão anterior do arquivo é descartado, e
	// ok=false diz ao chamador que NADA foi verificado — "sem erros" e "não sei" são respostas diferentes.
	items, ok, err := a.client.DiagnosticsFresh(ctx, a.abs, lspAutoDiagBudget)
	if err != nil || !ok {
		return nil, false
	}
	var out []lsp.Diagnostic
	for _, d := range items {
		if d.Severity != 1 { // 1 = Error
			continue
		}
		if a.beforeMsg[d.Message] {
			continue
		}
		// Sem "antes" conhecido não se pode afirmar que o erro é novo; nesse caso só vale o que caiu no
		// trecho editado, que é onde a edição podia causar algo.
		if !a.diagnosed && !a.inTract(d.Range) {
			continue
		}
		out = append(out, d)
	}
	return out, true
}

// importFix é o item (3): só import/using, e só quando o servidor classifica assim. Aplicar quickfix
// arbitrário seria editar o arquivo por conta própria.
func (a *autoEdit) importFix(ctx context.Context, fresh []lsp.Diagnostic) ([]lsp.Diagnostic, string) {
	tries := 0
	for _, d := range fresh {
		if tries == lspAutoMaxQuick || ctx.Err() != nil {
			break
		}
		tries++
		actions, err := a.client.CodeActions(ctx, a.abs, d.Range, []string{"quickfix"})
		if err != nil {
			continue
		}
		for _, act := range actions {
			if act.Edit == nil || !importish(act.Title) {
				continue
			}
			files := act.Edit.Files()
			if len(files) == 0 {
				continue
			}
			if _, err := a.client.ApplyEdit(act.Edit, lsp.ApplyOptions{Root: a.root}); err != nil {
				continue
			}
			for _, f := range files {
				noteWrite(a.host, f)
			}
			fixed := act.Diagnostics
			if len(fixed) == 0 {
				fixed = []lsp.Diagnostic{d}
			}
			return fixed, importLabel(act.Title)
		}
	}
	return nil, ""
}

// format é o item (5), na versão conservadora: formata só quando o servidor não mexeria em nada além do
// trecho editado. Um format inteiro reescreveria o arquivo inteiro na resposta de uma edição de duas linhas.
func (a *autoEdit) format() {
	ctx, cancel := context.WithTimeout(context.Background(), lspAutoFormatBudget)
	defer cancel()
	edits, err := a.client.Formatting(ctx, a.abs, lsp.FormattingOptions{TabSize: 4, InsertSpaces: true})
	if err != nil || len(edits) == 0 {
		return
	}
	for _, e := range edits {
		if !a.inTract(e.Range) {
			return
		}
	}
	we := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{lsp.PathToURI(a.abs): edits}}
	if _, err := a.client.ApplyEdit(we, lsp.ApplyOptions{Root: a.root}); err != nil {
		return
	}
	noteWrite(a.host, a.abs)
	a.note("formatted the edited range (%d edit(s))", len(edits))
}

// inTract diz se um range cai no trecho que a edição tocou (com uma linha de folga de cada lado).
func (a *autoEdit) inTract(r lsp.Range) bool {
	return r.Start.Line >= a.tract[0] && r.End.Line <= a.tract[1]
}

// --- comparação de declarações -------------------------------------------------------------------------

// declDeltaOf classifica a edição. A ordem importa: rename antes de assinatura (uma troca de nome muda a
// assinatura também, no texto), assinatura antes de remoção.
func declDeltaOf(rel, oldText, newText string) declDelta {
	oldDecls, _, ok := graph.ParseFile(rel, []byte(oldText))
	if !ok || len(oldDecls) == 0 {
		return declDelta{}
	}
	newDecls, _, ok := graph.ParseFile(rel, []byte(newText))
	if !ok || len(newDecls) == 0 {
		return declDelta{}
	}
	if d, ok := renameDelta(rel, oldText, newText, oldDecls, newDecls); ok {
		return d
	}
	if d, ok := sigDelta(rel, oldText, oldDecls, newDecls); ok {
		return d
	}
	return removedDelta(rel, oldText, oldDecls, newDecls)
}

type declKey struct {
	line, end int
	kind      string
}

// renameDelta reconhece a edição que trocou o nome de UMA declaração e nada mais: as outras continuam no mesmo
// lugar, com o mesmo tipo e a mesma assinatura, e o texto inteiro bate quando se apaga a diferença entre os
// dois nomes.
func renameDelta(rel, oldText, newText string, oldDecls, newDecls []codescan.Decl) (declDelta, bool) {
	if len(oldDecls) != len(newDecls) {
		return declDelta{}, false
	}
	byKey := make(map[declKey]int, len(newDecls))
	for i, d := range newDecls {
		byKey[declKey{d.Line, d.EndLine, d.Kind}] = i
	}
	used := make([]bool, len(newDecls))
	var oldOne, newOne codescan.Decl
	diff := 0
	for _, d := range oldDecls {
		i, ok := byKey[declKey{d.Line, d.EndLine, d.Kind}]
		if !ok || used[i] {
			return declDelta{}, false
		}
		used[i] = true
		nd := newDecls[i]
		if d.Name == nd.Name && d.Sig == nd.Sig {
			continue
		}
		diff++
		oldOne, newOne = d, nd
	}
	if diff != 1 || oldOne.Name == newOne.Name {
		return declDelta{}, false
	}
	if !maskProof(oldOne.Sig, newOne.Sig, oldOne.Name, newOne.Name) {
		return declDelta{}, false
	}
	// A prova sobre o arquivo inteiro é o que separa "renomeou a declaração" de "renomeou a declaração e mais
	// um monte de coisa" — nesse segundo caso o rename automático não é o que foi pedido.
	extra := false
	if !maskProof(oldText, newText, oldOne.Name, newOne.Name) {
		// Mais que o nome mudou. Só segue quando tudo o que sobra é UM trecho contíguo que contém a declaração
		// (nome + comentário dela na mesma edição); o resto do arquivo continua igual.
		if _, _, _, _, ok := residualHunk(oldText, newText, oldOne.Line); !ok {
			return declDelta{}, false
		}
		extra = true
	}
	col := graph.NameCol(declLineAt(oldText, oldOne.Line), oldOne.Name)
	if col == 0 {
		return declDelta{}, false
	}
	return declDelta{kind: declRenamed, name: oldOne.Name, newName: newOne.Name, sig: oldOne.Sig, newSig: newOne.Sig,
		line: oldOne.Line, endLine: oldOne.EndLine, col: col, extra: extra}, true
}

// sigDelta reconhece a assinatura trocada com o nome mantido: mesmos nomes, mesmos tipos de declaração, uma só
// com assinatura diferente.
func sigDelta(rel, oldText string, oldDecls, newDecls []codescan.Decl) (declDelta, bool) {
	if len(oldDecls) != len(newDecls) {
		return declDelta{}, false
	}
	// Homônimos no mesmo arquivo (sobrecargas, métodos de tipos diferentes) casam pela ordem de aparição: a edição
	// não mudou a quantidade de declarações, então a n-ésima de um nome é a n-ésima do mesmo nome.
	occKey := func(name string, seen map[string]int) string {
		seen[name]++
		return fmt.Sprintf("%s#%d", name, seen[name])
	}
	byName := make(map[string]codescan.Decl, len(newDecls))
	seenNew := map[string]int{}
	for _, d := range newDecls {
		byName[occKey(d.Name, seenNew)] = d
	}
	var oldOne, newOne codescan.Decl
	diff := 0
	seenOld := map[string]int{}
	for _, d := range oldDecls {
		nd, ok := byName[occKey(d.Name, seenOld)]
		if !ok || d.Kind != nd.Kind {
			return declDelta{}, false
		}
		if d.Sig == nd.Sig {
			continue
		}
		diff++
		oldOne, newOne = d, nd
	}
	if diff != 1 {
		return declDelta{}, false
	}
	col := graph.NameCol(declLineAt(oldText, oldOne.Line), oldOne.Name)
	if col == 0 {
		return declDelta{}, false
	}
	return declDelta{kind: declSigned, name: oldOne.Name, sig: oldOne.Sig, newSig: newOne.Sig,
		line: oldOne.Line, endLine: oldOne.EndLine, col: col}, true
}

// removedDelta reconhece UMA declaração que sumiu e nada mais.
func removedDelta(rel, oldText string, oldDecls, newDecls []codescan.Decl) declDelta {
	if len(newDecls) != len(oldDecls)-1 {
		return declDelta{}
	}
	newNames := make(map[string]codescan.Decl, len(newDecls))
	for _, d := range newDecls {
		newNames[d.Name] = d
	}
	var gone codescan.Decl
	missing := 0
	for _, d := range oldDecls {
		nd, ok := newNames[d.Name]
		if !ok {
			gone, missing = d, missing+1
			continue
		}
		if nd.Kind != d.Kind || nd.Sig != d.Sig {
			return declDelta{}
		}
	}
	if missing != 1 {
		return declDelta{}
	}
	col := graph.NameCol(declLineAt(oldText, gone.Line), gone.Name)
	if col == 0 {
		return declDelta{}
	}
	return declDelta{kind: declRemoved, name: gone.Name, sig: gone.Sig, line: gone.Line, endLine: gone.EndLine, col: col}
}

// residualHunk acha o único trecho (em linhas) em que oldText e newText diferem, tirando o prefixo e o sufixo
// iguais. ok só quando a linha da declaração (1-based, do texto velho) está dentro dele.
func residualHunk(oldText, newText string, declLine int) (oldLines, newLines []string, p, s int, ok bool) {
	oldLines, newLines = strings.SplitAfter(oldText, "\n"), strings.SplitAfter(newText, "\n")
	for p < len(oldLines) && p < len(newLines) && oldLines[p] == newLines[p] {
		p++
	}
	for s < len(oldLines)-p && s < len(newLines)-p && oldLines[len(oldLines)-1-s] == newLines[len(newLines)-1-s] {
		s++
	}
	if declLine < p+1 || declLine > len(oldLines)-s {
		return nil, nil, 0, 0, false
	}
	return oldLines, newLines, p, s, true
}

// mergeResidual devolve o texto final de um rename com mudança extra: o texto do servidor (o velho já renomeado,
// aqui e nos outros arquivos) com o trecho que o modelo reescreveu no lugar. O trecho velho, com o nome trocado,
// precisa aparecer UMA vez no texto do servidor; se não, não há prova e nada é aplicado.
func mergeResidual(post, oldText, newText, name, newName string, declLine int) (string, bool) {
	oldLines, newLines, p, s, ok := residualHunk(oldText, newText, declLine)
	if !ok {
		return "", false
	}
	oldHunk := strings.Join(oldLines[p:len(oldLines)-s], "")
	newHunk := strings.Join(newLines[p:len(newLines)-s], "")
	masked := renameWord(oldHunk, name, newName)
	if masked == "" || strings.Count(post, masked) != 1 {
		return "", false
	}
	return strings.Replace(post, masked, newHunk, 1), true
}

// renameWord troca as ocorrências inteiras de name por newName.
func renameWord(s, name, newName string) string {
	parts := strings.Split(maskWord(s, name), maskMarker)
	return strings.Join(parts, newName)
}

// maskProof diz se dois textos são o mesmo quando se esquece qual dos dois nomes aparece em cada lugar. É a
// prova usada para aceitar o texto do servidor: o homônimo que ele deixou em paz não conta como diferença, e
// uma mudança a mais (que não seja o rename) derruba a prova.
func maskProof(a, b, oldName, newName string) bool {
	return maskWord(maskWord(a, oldName), newName) == maskWord(maskWord(b, oldName), newName)
}

const maskMarker = "\x00"

// maskWord troca as ocorrências inteiras de name por um marcador único. "Inteiras" importa: renomear Parse
// para ParseLine não pode casar com o Parse de dentro de ParseLine.
func maskWord(s, name string) string {
	if name == "" || s == "" {
		return s
	}
	var b strings.Builder
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], name)
		if i < 0 {
			b.WriteString(s[from:])
			break
		}
		at := from + i
		if wordEdge(s, at-1) && wordEdge(s, at+len(name)) {
			b.WriteString(s[from:at])
			b.WriteString(maskMarker)
			from = at + len(name)
			continue
		}
		b.WriteString(s[from : at+1])
		from = at + 1
	}
	return b.String()
}

// wordEdge diz se a posição é fronteira de palavra. Byte fora do ASCII conta como fronteira: identificador em
// Go é ASCII, e um byte solto de UTF-8 não forma palavra.
func wordEdge(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	switch {
	case c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		return false
	}
	return true
}

// tractRange é o trecho que a edição tocou, em linhas 0-based inclusivas, com uma linha de folga de cada lado:
// o servidor costuma ancorar a correção (import acima, chave abaixo) na linha vizinha.
func tractRange(oldText, newText string) [2]int {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	first, last := -1, -1
	for i := 0; i < len(oldLines) || i < len(newLines); i++ {
		var o, n string
		if i < len(oldLines) {
			o = oldLines[i]
		}
		if i < len(newLines) {
			n = newLines[i]
		}
		if o != n {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return [2]int{0, max(0, len(newLines)-1)}
	}
	return [2]int{max(0, first-1), min(max(0, len(newLines)-1), last+1)}
}

// declLineAt devolve a linha 1-based de um texto, ou "" quando não existe.
func declLineAt(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// --- formatação da resposta ----------------------------------------------------------------------------

// diagLine é o formato do item (4).
func diagLine(rel string, ds []lsp.Diagnostic) string {
	word := "errors"
	if len(ds) == 1 {
		word = "error"
	}
	parts := make([]string, 0, lspAutoMaxDiags+1)
	for i, d := range ds {
		if i == lspAutoMaxDiags {
			parts = append(parts, fmt.Sprintf("+%d more", len(ds)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s:%d: %s", rel, d.Range.Start.Line+1, shortMsg(d.Message)))
	}
	return fmt.Sprintf("diagnostics: %d %s (%s)", len(ds), word, strings.Join(parts, "; "))
}

const (
	shortMsgMax  = 130
	shortMsgHead = 50
	shortMsgTail = 70
)

func shortMsg(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSpace(msg)
	// O que a LLM precisa para consertar costuma estar no FIM da mensagem ("... parâmetro 'throwOnInvalid' de
	// 'IsValidName(string, bool)'"): cortar o rabo escondia o nome do parâmetro. Mensagem longa mantém começo e fim.
	if r := []rune(msg); len(r) > shortMsgMax {
		msg = string(r[:shortMsgHead]) + " … " + string(r[len(r)-shortMsgTail:])
	}
	return msg
}

func shortErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	return shortMsg(err.Error())
}

func importish(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "import") || strings.Contains(t, "unused")
}

func importLabel(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.HasPrefix(t, "add import"):
		return "added import " + strings.TrimSpace(title[len("add import"):])
	case strings.HasPrefix(t, "remove unused import"):
		return "removed unused import " + strings.TrimSpace(title[len("remove unused import"):])
	case strings.Contains(t, "organize import"):
		return "organized the imports"
	}
	return "applied " + title
}

func minusDiags(all, fixed []lsp.Diagnostic) []lsp.Diagnostic {
	if len(fixed) == 0 {
		return all
	}
	drop := make(map[string]bool, len(fixed))
	for _, d := range fixed {
		drop[diagKey(d)] = true
	}
	out := make([]lsp.Diagnostic, 0, len(all))
	for _, d := range all {
		if !drop[diagKey(d)] {
			out = append(out, d)
		}
	}
	return out
}

func diagKey(d lsp.Diagnostic) string {
	return fmt.Sprintf("%d|%d|%d|%s", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Message)
}

// relSites converte locais do servidor em file:line do índice, ordenados.
func relSites(root string, locs []lsp.Location) []graph.Site {
	var sites []graph.Site
	seen := map[string]bool{}
	for _, l := range locs {
		s, ok := relSite(root, l)
		if !ok {
			continue
		}
		if k := graph.SiteKey(s); !seen[k] {
			seen[k] = true
			sites = append(sites, s)
		}
	}
	graph.SortSites(sites)
	return sites
}

func withoutFile(sites []graph.Site, rel string) []graph.Site {
	out := sites[:0:0]
	for _, s := range sites {
		if s.File != rel {
			out = append(out, s)
		}
	}
	return out
}

// withoutDecl tira a própria declaração da lista de usos: o servidor pode devolvê-la junto (includeDeclaration
// não é honrado por todo mundo), e ela não é um chamador que precise de ajuste.
func withoutDecl(sites []graph.Site, rel string, line int) []graph.Site {
	out := sites[:0:0]
	for _, s := range sites {
		if s.File == rel && s.Line == line {
			continue
		}
		out = append(out, s)
	}
	return out
}

// firstSites limita a lista de locais ao que cabe na forma curta da resposta.
func firstSites(sites []graph.Site, n int) []graph.Site {
	if len(sites) < n {
		n = len(sites)
	}
	return sites[:n]
}

func joinFirst(names []string, n int) string {
	if len(names) < n {
		n = len(names)
	}
	return strings.Join(names[:n], ", ")
}
