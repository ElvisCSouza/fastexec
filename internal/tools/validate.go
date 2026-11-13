package tools

// validate.go: o que a LLM lê sobre o código que acabou de escrever — como o VS sublinhando em vermelho.
//
// Duas camadas, na mesma resposta do edit_file/write_file:
//
//	1. sintaxe EXATA (syntaxcheck.go), sempre, em ms, sem projeto carregado;
//	2. semântica (tipos, símbolos, import) pelo servidor de linguagem, só se a camada 1 passou: com erro de
//	   sintaxe a semântica é ruído em cascata.
//
// Regras que valem para as duas: só ERROS (warning, info e hint nunca saem); só o que a edição criou (o que já
// era erro antes não aparece); e a resposta nunca é um silêncio que a LLM leia como "está certo" quando a
// semântica não foi verificada — ela diz o que não sabe. Nada aqui reverte a edição: grava, avisa e deixa o patch
// de undo que a fase A já anuncia.
//
// Formato (no máximo 3 erros por arquivo, 2 arquivos dependentes, linha de código aparada em 80 caracteres):
//
//	✗ Foo.cs:30 CS1002 ; expected │ var x = 1
//	✗ Bar.cs:44 CS7036 ... (efeito da sua edição)
//	✓ compila
//	✓ sintaxe · tipos pendentes (servidor carregando)

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/lsp"
)

const (
	validateMaxErrs = 3  // erros mostrados do arquivo editado
	validateMaxDeps = 2  // arquivos dependentes mostrados (D8)
	validateCodeMax = 80 // largura da linha de código depois do │
	// validateDepScan: quantos chamadores o servidor é consultado (o que passa disso não é verificado).
	validateDepScan = 5
	// validateDepBudget: espera por arquivo dependente. A edição já está gravada: o rodapé pode perder precisão,
	// nunca tempo.
	validateDepBudget = 400 * time.Millisecond
	// validateLateBudget: quanto tempo a camada 2 pode continuar em segundo plano depois que a resposta saiu.
	validateLateBudget = 40 * time.Second
)

// --- formatação ----------------------------------------------------------------------------------------------

// srcLine devolve a linha (1-based) aparada e limitada a validateCodeMax caracteres. Vazia se não existe.
func srcLine(text string, line int) string {
	if line < 1 {
		return ""
	}
	i := 0
	for l := 1; l < line; l++ {
		j := strings.IndexByte(text[i:], '\n')
		if j < 0 {
			return ""
		}
		i += j + 1
	}
	end := strings.IndexByte(text[i:], '\n')
	s := text[i:]
	if end >= 0 {
		s = text[i : i+end]
	}
	s = strings.TrimSpace(strings.TrimRight(s, "\r"))
	r := []rune(s)
	if len(r) > validateCodeMax {
		s = string(r[:validateCodeMax-1]) + "…"
	}
	return s
}

// errLine monta "✗ Foo.cs:30 CS1002 msg │ código".
func errLine(rel string, line int, id, msg, src, suffix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✗ %s:%d", rel, line)
	if id != "" {
		b.WriteString(" " + id)
	}
	b.WriteString(" " + shortMsg(msg))
	if suffix != "" {
		b.WriteString(" " + suffix)
	}
	if src != "" {
		b.WriteString(" │ " + src)
	}
	return b.String()
}

func moreErrs(n int) string { return fmt.Sprintf("+%d erros", n) }

// diagID devolve o código do diagnóstico do servidor ("CS0103", "undefined"...), vazio se não veio.
func diagID(d lsp.Diagnostic) string {
	switch v := d.Code.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%d", int(v))
	}
	return ""
}

// --- camada 1 ------------------------------------------------------------------------------------------------

// layer1 valida a sintaxe do texto que foi para o disco. ok = sintaxe verificada e limpa; blocked = achou erro e a
// camada 2 não deve rodar.
type layer1 struct {
	ok      bool
	blocked bool
	state   syntaxState
}

func (a *autoEdit) layer1(text string) layer1 {
	if !syntaxLanguage(a.abs) {
		return layer1{state: syntaxNone}
	}
	now, st := syntaxCheck(a.abs, text)
	if st != syntaxReady {
		return layer1{state: st}
	}
	var before []syntaxIssue
	if !a.isNew && a.oldText != "" {
		before, _ = syntaxCheck(a.abs, a.oldText)
	}
	fresh := newSyntaxIssues(now, before)
	if len(fresh) == 0 {
		if len(now) > 0 {
			// Só restam erros que já existiam: não são notícia, mas o arquivo NÃO está com a sintaxe boa. Nem
			// "✓ sintaxe" nem a camada 2 (cascata de ruído) cabem aqui.
			return layer1{blocked: true, state: st}
		}
		return layer1{ok: true, state: st}
	}
	for i, e := range fresh {
		if i == validateMaxErrs {
			a.extra = append(a.extra, moreErrs(len(fresh)-i))
			break
		}
		a.extra = append(a.extra, errLine(a.relOrBase(), e.line, e.id, e.msg, srcLine(text, e.line), ""))
	}
	markOutstanding(a.abs)
	return layer1{blocked: true, state: st}
}

func (a *autoEdit) relOrBase() string {
	if a.rel != "" {
		return a.rel
	}
	return filepath.Base(a.abs)
}

// --- camada 2 ------------------------------------------------------------------------------------------------

// fileInProject diz se o arquivo pertence a um projeto que o servidor carrega. Arquivo solto (fora de qualquer
// .csproj / go.mod) o servidor trata como "miscellaneous": os erros de tipo dele ali seriam inventados.
func fileInProject(abs, root, langID string) bool {
	var markers []string
	switch langID {
	case "go":
		markers = []string{"go.mod", "go.work"}
	case "csharp":
		markers = []string{"*.csproj", "*.fsproj"}
	default:
		return true
	}
	for d := filepath.Dir(abs); ; d = filepath.Dir(d) {
		for _, m := range markers {
			if ms, _ := filepath.Glob(filepath.Join(d, m)); len(ms) > 0 {
				return true
			}
		}
		if d == root || filepath.Dir(d) == d {
			return false
		}
	}
}

// layer2 é a fase B sobre o servidor: erro novo, import que faltou, chamadores afetados e formatação. Devolve
// verified=false quando o servidor não respondeu a tempo sobre ESTA versão do arquivo.
func (a *autoEdit) layer2(text string, s1 layer1) (verified bool) {
	ctx, cancel := context.WithTimeout(context.Background(), lspAutoDiagBudget)
	defer cancel()
	if a.isNew {
		a.client.FileCreated(ctx, a.abs)
	}
	fresh, ok := a.freshErrors(ctx)
	if !ok {
		return false
	}
	fixed, importNote := a.importFix(ctx, fresh)
	rest := minusDiags(fresh, fixed)
	if importNote != "" {
		a.note("%s", importNote)
	}
	if len(rest) > 0 {
		cur := text
		if b, err := os.ReadFile(a.abs); err == nil {
			cur = string(b) // o import aplicado mudou as linhas
		}
		for i, d := range rest {
			if i == validateMaxErrs {
				a.extra = append(a.extra, moreErrs(len(rest)-i))
				break
			}
			ln := d.Range.Start.Line + 1
			a.extra = append(a.extra, errLine(a.relOrBase(), ln, diagID(d), d.Message, srcLine(cur, ln), ""))
		}
		markOutstanding(a.abs)
		return true
	}
	a.format()
	return true
}

// layer2Result roda a camada 2 e classifica o que a LLM pode (ou não) concluir.
func (a *autoEdit) layer2Result(text string, s1 layer1) semanticResult {
	if a.noRoot {
		return semNoProject // fora de repositório só há a camada 1, e a LLM é avisada de que os tipos não foram verificados
	}
	if !fileInProject(a.abs, a.root, a.lang) {
		return semNoProject
	}
	if a.client == nil {
		if !a.serverAvailable() {
			return semNoServer
		}
		return semPending // o servidor sobe em segundo plano (handOff) e o diagnóstico chega depois
	}
	a.phaseB = true
	n0 := len(a.extra)
	if !a.layer2(text, s1) {
		return semPending
	}
	a.depsUnverified = a.checkDependents()
	if len(a.extra) > n0 {
		return semErrors
	}
	return semOK
}

func siteFiles(sites []graph.Site) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.File)
	}
	return out
}

// sitesAbs converte os locais exatos do servidor em caminhos absolutos de arquivo.
func (a *autoEdit) sitesAbs(files []string) []string {
	out := make([]string, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(a.root, filepath.FromSlash(f))
		}
		if p != a.abs && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// checkDependents é o D8: os erros que a edição causou em OUTROS arquivos (o chamador de uma assinatura que mudou, o
// uso de um símbolo removido). Devolve quantos dos arquivos consultados não puderam ser verificados.
func (a *autoEdit) checkDependents() (unverified int) {
	if a.client == nil || len(a.deps) == 0 {
		return 0
	}
	files := make([]string, 0, len(a.deps))
	for f := range a.deps {
		files = append(files, f)
	}
	sort.Strings(files)
	shown, hidden := 0, 0
	for _, f := range files {
		st := a.deps[f]
		ctx, cancel := context.WithTimeout(context.Background(), validateDepBudget*2)
		items, ok, _ := a.client.DiagnosticsSince(ctx, f, st.seq, validateDepBudget)
		cancel()
		if !ok {
			unverified++
			continue
		}
		var errs []lsp.Diagnostic
		for _, d := range items {
			if d.Severity != 1 || st.base[d.Message] {
				continue
			}
			// Sem "antes" conhecido não se pode afirmar que o erro é da edição: só vale o que fala do símbolo.
			if !st.known && !a.mentionsDeclared(d.Message) {
				continue
			}
			errs = append(errs, d)
		}
		if len(errs) == 0 {
			continue
		}
		if shown == validateMaxDeps {
			hidden += len(errs)
			continue
		}
		shown++
		d := errs[0]
		ln := d.Range.Start.Line + 1
		var src string
		if b, err := os.ReadFile(f); err == nil {
			src = srcLine(string(b), ln)
		}
		rel := relToRoot(a.root, f)
		if rel == "" {
			rel = filepath.Base(f)
		}
		a.extra = append(a.extra, errLine(rel, ln, diagID(d), d.Message, src, "(efeito da sua edição)"))
		markOutstanding(f)
		if len(errs) > 1 {
			hidden += len(errs) - 1
		}
	}
	if hidden > 0 {
		a.extra = append(a.extra, moreErrs(hidden))
	}
	return unverified
}

func (a *autoEdit) mentionsDeclared(msg string) bool {
	for _, n := range []string{a.delta.name, a.delta.newName} {
		if n != "" && strings.Contains(msg, n) {
			return true
		}
	}
	return false
}

// depState é o "antes" de um arquivo dependente, tirado antes da edição.
type depState struct {
	seq   uint64
	base  map[string]bool
	known bool
}

// captureDeps guarda o "antes" dos arquivos que a edição pode afetar. Roda ANTES de a edição chegar ao servidor.
func (a *autoEdit) captureDeps(files []string) {
	if a.client == nil {
		return
	}
	if a.deps == nil {
		a.deps = map[string]*depState{}
	}
	for _, f := range files {
		if len(a.deps) >= validateDepScan {
			return
		}
		if f == a.abs {
			continue
		}
		st := &depState{seq: a.client.DiagSeq(f), base: map[string]bool{}}
		if a.client.Diagnosed(f) {
			st.known = true
			for _, d := range a.client.Published(f) {
				st.base[d.Message] = true
			}
		}
		a.deps[f] = st
	}
}

// --- estado e fecho ------------------------------------------------------------------------------------------

var (
	statusOnceMu sync.Mutex
	statusOnce   = map[string]bool{}
)

// once devolve true na primeira vez que a chave aparece neste processo: aviso permanente ("não há servidor de C#
// instalado") não precisa se repetir em toda edição.
func once(key string) bool {
	statusOnceMu.Lock()
	defer statusOnceMu.Unlock()
	if statusOnce[key] {
		return false
	}
	statusOnce[key] = true
	return true
}

// statusLine decide a linha final. worthy diz se vale dizer "✓ compila" (arquivo novo, edição estrutural, ou
// qualquer coisa que antes tivesse erro): numa edição comum e limpa o silêncio é a resposta e custa zero token.
func (a *autoEdit) statusLine(s1 layer1, semantic semanticResult) string {
	switch semantic {
	case semOK:
		if !a.worthyOK() {
			return ""
		}
		// Assinatura mudada (não o rename, que o servidor já aplicou nos usos): os chamadores estavam na lista do aviso, e \"nenhum erro neles\" logo
		// depois da edição pode ser só o servidor ainda não ter reanalisado. Dizer \"compila\" sem ressalva faria a LLM
		// acreditar que os chamadores estão certos (o CS3 v6 viu exatamente isso).
		if a.depsUnverified > 0 || (a.delta.kind == declSigned && len(a.deps) > 0) {
			return "✓ compila (este arquivo) · chamadores não verificados: confira os listados acima"
		}
		return "✓ compila"
	case semPending:
		if s1.ok {
			return "✓ sintaxe · tipos pendentes (servidor carregando)"
		}
		return "· tipos pendentes (servidor carregando)"
	case semNoProject:
		if !s1.ok {
			return ""
		}
		if !a.isNew && !once("noproject|"+filepath.Dir(a.abs)) {
			return "" // edição de arquivo existente: avisa uma vez por pasta, não a cada edição
		}
		return "✓ sintaxe · tipos não verificados (arquivo fora do projeto)"
	case semNoServer:
		if !once("noserver|" + a.lang) {
			return ""
		}
		if s1.ok {
			return "✓ sintaxe · tipos não verificados (sem servidor de " + a.lang + ")"
		}
		return "· tipos não verificados (sem servidor de " + a.lang + ")"
	}
	return ""
}

func (a *autoEdit) worthyOK() bool {
	return a.isNew || a.delta.kind != declNone || len(a.deps) > 0 || clearOutstanding(a.abs)
}

// outstanding guarda os arquivos para os quais o FastExec JA mostrou um erro. A edição que o conserta não pode ficar
// em silêncio: sem "✓ compila" a LLM não sabe se o erro sumiu e gasta um build para descobrir.
var outstanding = struct {
	sync.Mutex
	files map[string]bool
}{files: map[string]bool{}}

func markOutstanding(abs string) {
	if abs == "" {
		return
	}
	outstanding.Lock()
	outstanding.files[filepath.Clean(abs)] = true
	outstanding.Unlock()
}

// clearOutstanding diz se havia erro mostrado para abs e já o tira da lista.
func clearOutstanding(abs string) bool {
	outstanding.Lock()
	defer outstanding.Unlock()
	abs = filepath.Clean(abs)
	if !outstanding.files[abs] {
		return false
	}
	delete(outstanding.files, abs)
	return true
}

type semanticResult int

const (
	semNone      semanticResult = iota // nada a dizer (camada 2 não se aplica)
	semOK                              // verificada e limpa
	semErrors                          // verificada, com erros (já nas linhas)
	semPending                         // servidor ainda não respondeu sobre esta versão
	semNoProject                       // arquivo fora de projeto: só sintaxe
	semNoServer                        // não há servidor desta linguagem nesta máquina
)

// serverAvailable: o servidor da linguagem existe nesta máquina (instalado e com o que ele precisa para subir).
func (a *autoEdit) serverAvailable() bool {
	l, ok := lsp.LanguageByID(a.lang)
	if !ok {
		return false
	}
	return lsp.Installed(l)
}

// --- entrega tardia (D5) -------------------------------------------------------------------------------------

var lateQ struct {
	mu    sync.Mutex
	items []string
}

func queueLate(s string) {
	lateQ.mu.Lock()
	lateQ.items = append(lateQ.items, s)
	if len(lateQ.items) > 8 {
		lateQ.items = lateQ.items[len(lateQ.items)-8:]
	}
	lateQ.mu.Unlock()
}

// TakeLate devolve (e esvazia) os diagnósticos que ficaram prontos depois de a resposta da edição ter saído. O
// despachante de tools anexa isto UMA vez à próxima resposta de qualquer ferramenta.
func TakeLate() string {
	lateQ.mu.Lock()
	defer lateQ.mu.Unlock()
	if len(lateQ.items) == 0 {
		return ""
	}
	out := "\n" + strings.Join(lateQ.items, "\n")
	lateQ.items = nil
	return out
}

// handOff entrega o servidor a uma goroutine que continua esperando o diagnóstico desta versão do arquivo. Se o
// arquivo mudou até lá, o resultado é descartado (hash). Chamado no fim da resposta: o cliente deixa de ser da edição.
func (a *autoEdit) handOff(text string) {
	if a == nil || a.noRoot || lsp.Disabled() {
		return
	}
	sum := sha256.Sum256([]byte(text))
	c, release := a.client, a.release
	a.client, a.release = nil, nil
	abs, rel, isNew := a.abs, a.relOrBase(), a.isNew
	before, diagnosed, tract := a.beforeMsg, a.diagnosed, a.tract
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), validateLateBudget)
		defer cancel()
		if c == nil {
			var err error
			c, release, err = autoSpawn(ctx, abs)
			if err != nil {
				return
			}
			_ = c.WaitSettled(ctx, abs, validateLateBudget/2)
		}
		defer release()
		if isNew {
			c.FileCreated(ctx, abs)
		}
		items, ok, err := c.DiagnosticsFresh(ctx, abs, validateLateBudget)
		if err != nil || !ok {
			return
		}
		b, err := os.ReadFile(abs)
		if err != nil || sha256.Sum256(b) != sum {
			return // o arquivo mudou: o resultado é de uma versão que já não existe
		}
		var errs []lsp.Diagnostic
		for _, d := range items {
			if d.Severity != 1 || before[d.Message] {
				continue
			}
			if !isNew && !diagnosed && (d.Range.Start.Line < tract[0] || d.Range.End.Line > tract[1]) {
				continue
			}
			errs = append(errs, d)
		}
		if len(errs) == 0 {
			queueLate(fmt.Sprintf("⏱ diagnóstico tardio %s: ✓ compila (tipos verificados)", rel))
			return
		}
		for i, d := range errs {
			if i == validateMaxErrs {
				queueLate(fmt.Sprintf("⏱ diagnóstico tardio %s: %s", rel, moreErrs(len(errs)-i)))
				break
			}
			ln := d.Range.Start.Line + 1
			queueLate("⏱ diagnóstico tardio " + errLine(rel, ln, diagID(d), d.Message, srcLine(string(b), ln), "")[len("✗ "):])
		}
	}()
}
