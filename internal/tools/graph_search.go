package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/transport"
)

// Graph search: a plain recursive search for one identifier inside a git repository (grep -rn Name, rg Name,
// git grep Name, findstr /s Name) is answered from the code graph in the shape of the question, so the answer is
// ready to use: the declaration with its body, the call sites grouped by file with counts, or just the file list
// (-l) / the counts (-c). Also answered: a search for a FILE NAME (find -name, ls -R | grep, dir /s | findstr).
//
// The rules are deterministic:
//   - only on host 'local', only inside a git repository, only for a pure identifier (a regex, an alternation, an
//     anchor other than \b or -i means the graph cannot promise the same output: raw search) and only when the
//     graph has a declaration of it;
//   - the index is refreshed first (graph.Sync): the pass has a time budget, and when it does not fit the raw
//     search answers instead. A stale index is never answered from, and every reindex announces itself;
//   - repeating the exact same command (same request key, within graphRepeatWindow) runs the raw search;
//   - '--raw' (or the 'raw:' prefix) in the command asks for the raw search, and the footer always shows that same
//     form so the escape needs no other command.

const (
	// graphAnswerMax é o teto da resposta pronta. O rodapé nunca é cortado: é ele que traz a saída de emergência.
	graphAnswerMax = 3 * 1024
	// graphBodyMaxLines: acima disto o corpo da declaração vira assinatura + faixa de linhas (read_file é mais barato).
	graphBodyMaxLines = 40
	graphMaxListFiles = 40
	graphMaxDefs      = 8
	graphMaxOutline   = 12
)

var (
	reIdent      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
	reDeclPrefix = regexp.MustCompile(`^(?:func|def|class|struct|interface|type|fun|void|enum|record|object)\s+`)
	reFileNeedle = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]{2,}$`)
)

// searchReq é uma busca de identificador: o padrão, os caminhos varridos e a forma da saída pedida (-l, -c).
type searchReq struct {
	pattern string
	paths   []string
	list    bool
	count   bool
}

// fileReq é uma busca por NOME DE ARQUIVO (find -name, listagem canalizada para grep/findstr).
type fileReq struct {
	needle string
	paths  []string
}

// searchFlags é o que sai da leitura dos argumentos de um programa de busca.
type searchFlags struct {
	pattern string
	paths   []string
	rec     bool
	list    bool
	count   bool
}

// cleanSearchPattern reduz "\bFoo\b", "func Foo" etc. a Foo; "" quando não é um identificador puro. Âncora de
// palavra é a mesma busca; qualquer outro metacaractere (regex, alternação, classe) fica de fora: o grafo não
// promete a mesma saída de uma regex.
func cleanSearchPattern(p string) string {
	p = strings.TrimSpace(p)
	p = reDeclPrefix.ReplaceAllString(p, "")
	for _, s := range []string{`\b`, `\<`, `\>`} {
		p = strings.ReplaceAll(p, s, "")
	}
	// Âncora de linha (^, $) NÃO sai: "grep '^Foo'" acha outra coisa que "grep Foo" e o grafo não promete isso.
	p = strings.Trim(p, " ")
	if !reIdent.MatchString(p) {
		return ""
	}
	return p
}

func baseProg(p string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".exe"))
}

// parseSearchTokens lê flags e posicionais de um programa de busca (o padrão é o primeiro posicional, salvo -e).
func parseSearchTokens(rest []string, prog string) (searchFlags, bool) {
	var f searchFlags
	for i := 0; i < len(rest); i++ {
		t := rest[i]
		lt := strings.ToLower(t)
		switch {
		case t == "--":
			f.paths = append(f.paths, rest[i+1:]...)
			i = len(rest)
		case (prog == "select-string" || prog == "sls") && strings.HasPrefix(t, "-"):
			switch {
			case lt == "-pattern":
				if i+1 < len(rest) {
					i++
					f.pattern = rest[i]
				}
			case strings.HasPrefix(lt, "-pattern:"):
				f.pattern = t[9:]
			case lt == "-path" || lt == "-literalpath":
				if i+1 < len(rest) {
					i++
					f.paths = append(f.paths, rest[i])
				}
			case strings.HasPrefix(lt, "-path:") || strings.HasPrefix(lt, "-literalpath:"):
				f.paths = append(f.paths, t[strings.Index(t, ":")+1:])
			case lt == "-casesensitive" || lt == "-simplematch":
			default:
				// ignora flags desconhecidas sem valor
			}
		case prog == "findstr" && strings.HasPrefix(t, "/"):
			switch lt {
			case "/s":
				f.rec = true
			case "/n", "/r", "/l", "/p":
			case "/i":
				return f, false // -i: a busca crua acha outra caixa; o grafo não promete isso
			default:
				if strings.HasPrefix(lt, "/c:") {
					f.pattern = t[3:]
				} else {
					return f, false // /v, /m, /x ...: saída diferente
				}
			}
		case strings.HasPrefix(t, "--"):
			name := strings.SplitN(lt, "=", 2)[0]
			switch name {
			case "--recursive", "--dereference-recursive":
				f.rec = true
			case "--files-with-matches":
				f.list = true
			case "--count", "--count-matches":
				f.count = true
			case "--line-number", "--with-filename", "--no-heading", "--color", "--colour", "--word-regexp",
				"--fixed-strings", "--extended-regexp", "--perl-regexp", "--hidden", "--no-ignore",
				"--exclude-dir", "--exclude", "--glob", "--type":
				if !strings.Contains(t, "=") && (name == "--exclude-dir" || name == "--exclude" || name == "--glob" || name == "--type") {
					i++
				}
			case "--include":
				v := ""
				if j := strings.Index(t, "="); j >= 0 {
					v = t[j+1:]
				} else if i+1 < len(rest) {
					i++
					v = rest[i]
				}
				if codescan.LangFor("x"+strings.TrimPrefix(v, "*")) == nil {
					return f, false // filtro só para arquivos que o grafo não indexa
				}
			default:
				return f, false // --ignore-case e afins: o grafo não promete a mesma saída
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			flags := t[1:]
			for k, c := range flags {
				switch c {
				case 'r', 'R':
					f.rec = true
				case 'l':
					f.list = true
				case 'c':
					f.count = true
				case 'n', 'H', 'w', 'E', 'F', 'P', 's', 'I':
				case 'e':
					if k == len(flags)-1 && i+1 < len(rest) {
						i++
						f.pattern = rest[i]
					}
				case 'g', 't', 'm':
					if k == len(flags)-1 {
						i++ // valor da flag
					}
				default:
					return f, false // -i (caixa), -v, -o, -A/-B/-C ...: o grafo não dá essa saída
				}
			}
		default:
			f.paths = append(f.paths, t)
		}
	}
	if f.pattern == "" && len(f.paths) > 0 {
		f.pattern, f.paths = f.paths[0], f.paths[1:]
	}
	return f, true
}

// parseIdentSearch lê uma busca recursiva de uma linha só. ok=false quando o comando é outra coisa ou pede uma
// saída que o grafo não dá.
func parseIdentSearch(cmd string) (searchReq, bool) {
	segs := shellSegments(cmd)
	if len(segs) != 1 {
		return searchReq{}, false
	}
	w := shellWords(segs[0])
	if len(w) < 2 {
		return searchReq{}, false
	}
	prog := baseProg(w[0])
	rec := false
	switch {
	case prog == "grep" || prog == "egrep" || prog == "fgrep":
	case prog == "rg":
		rec = true
	case prog == "git" && len(w) > 2 && w[1] == "grep":
		w, rec = append([]string{w[0] + " " + w[1]}, w[2:]...), true
	case prog == "findstr":
	case prog == "select-string" || prog == "sls":
		rec = true
	default:
		return searchReq{}, false
	}
	f, ok := parseSearchTokens(w[1:], prog)
	if !ok || (!f.rec && !rec) {
		return searchReq{}, false
	}
	p := cleanSearchPattern(f.pattern)
	if p == "" {
		return searchReq{}, false
	}
	return searchReq{pattern: p, paths: f.paths, list: f.list, count: f.count}, true
}

// parseFilterSearch: um grep/findstr de identificador usado como filtro de uma listagem (a recursividade vem da
// própria listagem, então não é exigida aqui).
func parseFilterSearch(seg string) (string, bool) {
	w := shellWords(seg)
	if len(w) < 2 {
		return "", false
	}
	prog := baseProg(w[0])
	switch prog {
	case "grep", "egrep", "fgrep", "rg", "findstr", "select-string", "sls":
	default:
		return "", false
	}
	f, ok := parseSearchTokens(w[1:], prog)
	if !ok || f.list || f.count {
		return "", false
	}
	return cleanSearchPattern(f.pattern), true
}

// parseFileishSearch reconhece a busca por nome de arquivo: 'find . -name <glob>' e listagens (ls, dir, git
// ls-files) canalizadas para um grep/findstr. O grafo responde com os arquivos do repositório.
func parseFileishSearch(cmd string) (fileReq, bool) {
	segs := shellSegments(cmd)
	switch len(segs) {
	case 1:
		return parseFindSearch(shellWords(segs[0]))
	case 2:
	default:
		return fileReq{}, false
	}
	lw := shellWords(segs[0])
	if len(lw) == 0 {
		return fileReq{}, false
	}
	prog := baseProg(lw[0])
	listing := prog == "ls" || prog == "dir" || prog == "get-childitem" || prog == "gci"
	if !listing && !(prog == "git" && len(lw) > 1 && lw[1] == "ls-files") {
		return fileReq{}, false
	}
	needle, ok := parseFilterSearch(segs[1])
	if !ok {
		return fileReq{}, false
	}
	var paths []string
	for i, t := range lw[1:] {
		if prog == "git" && i == 0 && t == "ls-files" {
			continue
		}
		if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/") {
			continue // flags: /s /b -R -la ...
		}
		paths = append(paths, t)
	}
	return fileReq{needle: needle, paths: paths}, true
}

func parseFindSearch(w []string) (fileReq, bool) {
	if len(w) == 0 || baseProg(w[0]) != "find" {
		return fileReq{}, false
	}
	var needle string
	var paths []string
	for i := 1; i < len(w); i++ {
		t := w[i]
		lt := strings.ToLower(t)
		switch {
		case lt == "-name" || lt == "-iname":
			if i+1 >= len(w) {
				return fileReq{}, false
			}
			i++
			needle = w[i]
		case lt == "-type" || lt == "-maxdepth" || lt == "-mindepth":
			i++
		case lt == "-print" || lt == "-print0":
		case strings.HasPrefix(t, "-"):
			return fileReq{}, false
		case needle == "":
			paths = append(paths, t) // o caminho vem antes do -name
		default:
			return fileReq{}, false
		}
	}
	needle = strings.Trim(needle, "*?")
	if !reFileNeedle.MatchString(needle) {
		return fileReq{}, false
	}
	return fileReq{needle: needle, paths: paths}, true
}

// parseAnySearch lê o comando nas duas formas (identificador, nome de arquivo).
func parseAnySearch(cmd string) (req searchReq, symbol bool, fr fileReq, fileish bool) {
	if r, ok := parseIdentSearch(cmd); ok {
		return r, true, fileReq{}, false
	}
	if f, ok := parseFileishSearch(cmd); ok {
		return searchReq{}, false, f, true
	}
	return searchReq{}, false, fileReq{}, false
}

// isSearchableText diz se o texto (sem o pedido de busca crua) é uma busca que o grafo saberia responder.
func isSearchableText(t string) bool {
	_, symbol, _, fileish := parseAnySearch(t)
	return symbol || fileish
}

// stripRawEscape tira do comando o pedido de busca crua ('--raw' ou o prefixo 'raw:'). O '--raw' sai por posição
// (o resto do comando não é reescrito); qualquer comando que não seja uma busca passa byte a byte.
func stripRawEscape(args *ExecArgs) (label string, stripped bool) {
	if len(args.Argv) > 0 {
		out := make([]string, 0, len(args.Argv))
		hit := false
		for i, a := range args.Argv {
			if i > 0 && (strings.EqualFold(a, "--raw") || strings.EqualFold(a, "raw:")) {
				hit = true
				continue
			}
			out = append(out, a)
		}
		label = strings.Join(args.Argv, " ")
		if !hit || !isSearchableText(strings.Join(out, " ")) {
			return label, false
		}
		args.Argv = out
		return strings.Join(out, " "), true
	}
	out, hit := stripRawText(args.Command)
	if !hit || !isSearchableText(out) {
		return args.Command, false
	}
	args.Command = out
	return out, true
}

func stripRawText(cmd string) (string, bool) {
	s, hit := cmd, false
	lead := len(s) - len(strings.TrimLeft(s, " \t"))
	if rest, ok := cutPrefixFold(s[lead:], "raw:"); ok {
		s, hit = s[:lead]+rest, true
	}
	spans := shellTokenSpans(s)
	for i := len(spans) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.Trim(s[spans[i].Start:spans[i].End], `"'`), "--raw") {
			continue
		}
		start, end := spans[i].Start, spans[i].End
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
		for start > 0 && end == len(s) && (s[start-1] == ' ' || s[start-1] == '\t') {
			start--
		}
		s, hit = s[:start]+s[end:], true
	}
	if !hit {
		// embrulhado (pwsh -Command "... --raw"): o pedido está dentro do script, tira lá dentro
		if script, _ := unwrapShell(ExecArgs{Command: cmd}, true); script != "" && script != cmd {
			if inner, ok := stripRawText(script); ok && strings.Contains(cmd, script) {
				s, hit = strings.Replace(cmd, script, inner, 1), true
			}
		}
	}
	return strings.TrimSpace(s), hit
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return strings.TrimLeft(s[len(prefix):], " \t"), true
	}
	return s, false
}

// withRawEscape devolve o mesmo comando pedindo a busca crua: '--raw' antes do '--' final ou no fim. É a forma que
// o rodapé mostra, e ela funciona sozinha (stripRawEscape tira o token e o comando roda como o original).
func withRawEscape(cmd string) string {
	spans := shellTokenSpans(cmd)
	for i := len(spans) - 1; i >= 0; i-- {
		if cmd[spans[i].Start:spans[i].End] == "--" {
			return cmd[:spans[i].Start] + "--raw " + cmd[spans[i].Start:]
		}
	}
	return strings.TrimRight(cmd, " \t") + " --raw"
}

// tryGraphSearch responde a busca pelo grafo. handled=false: rode o comando; note (quando não vazio) explica o que
// aconteceu (índice refeito, teto estourado, repetição, busca crua pedida).
func tryGraphSearch(args ExecArgs, label string, raw bool) (out string, handled bool, note string) {
	if raw || searchGuardOff() || os.Getenv("FASTEXEC_NO_GRAPH_SEARCH") != "" {
		return "", false, ""
	}
	if t, err := transport.ResolveHost(args.Host); err != nil || !t.IsLocal {
		return "", false, "" // o grafo indexa só repositórios locais
	}
	req, symbol, fr, fileish := parseAnySearch(label)
	if !symbol && !fileish {
		script, _ := unwrapShell(args, true)
		if script == "" || script == label {
			return "", false, ""
		}
		req, symbol, fr, fileish = parseAnySearch(script)
		if !symbol && !fileish {
			return "", false, ""
		}
	}
	paths := req.paths
	if fileish {
		paths = fr.paths
	}
	base := args.Cwd
	if base == "" {
		if wd, err := os.Getwd(); err == nil {
			base = wd
		}
	}
	target := base
	if len(paths) > 1 {
		return "", false, ""
	}
	if len(paths) == 1 && !strings.ContainsAny(paths[0], "*?") {
		target = paths[0]
		if !filepath.IsAbs(target) {
			if base == "" {
				return "", false, ""
			}
			target = filepath.Join(base, target)
		}
	}
	if target == "" {
		return "", false, ""
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return "", false, ""
	}
	root, ok := graph.ResolveRepoRoot(target)
	if !ok {
		return "", false, ""
	}
	sub, _ := filepath.Rel(root, target)
	if sub = filepath.ToSlash(sub); sub == "." {
		sub = ""
	}

	had := graph.Indexed(root) // antes do Sync: separa "índice atrasado" de "índice ainda não existe" (a nota diz qual dos dois)
	st, err := graph.Sync(root, graph.SyncBudget)
	if err != nil {
		return "", false, GraphMarkIndex + ": the code graph could not refresh its index (" + firstLine(err.Error()) + "); the raw search follows."
	}
	announce := indexNote(st)
	if st.OverBudget {
		return "", false, notes(announce, overBudgetNote(had))
	}
	where := filepath.Base(root)
	if sub != "" {
		where += "/" + sub
	}

	if fileish {
		return answerFileSearch(root, sub, where, fr.needle, withRawEscape(label), announce)
	}
	defs, err := graph.Definitions(root, req.pattern)
	if err != nil {
		return "", false, announce
	}
	if defs = defsUnderSub(defs, sub); len(defs) == 0 {
		return "", false, announce // não é símbolo do código (texto, chave de config, outro idioma): busca normal
	}
	key := fmt.Sprintf("search|%s|%s", strings.ToLower(args.Cwd), strings.ToLower(label)) // sem identidade: ver graph_first.go
	if graphRepeat(key) {
		return "", false, notes(announce, GraphMarkRepeat+": raw search output because this exact command was repeated.")
	}
	counts, _ := graph.CallerFileCounts(root, req.pattern)
	counts = countsUnderSub(counts, sub)
	return buildSymbolAnswer(root, where, req, defs, counts, withRawEscape(label), announce), true, ""
}

// overBudgetNote diz o que aconteceu quando a passada de frescor não coube no teto. Distinguir "ainda não existe" de
// "estava atrasado" importa: só no segundo caso havia um índice que ficou pela metade.
func overBudgetNote(existed bool) string {
	what := "the index was behind and refreshing it did not fit"
	if !existed {
		what = "this repository has no code-graph index yet and building one did not fit"
	}
	return fmt.Sprintf("%s: %s in %d ms, so the raw search answers instead. The pass kept what it indexed; the next search is shorter.", GraphMarkIndex, what, graph.SyncBudget.Milliseconds())
}

// indexNote anuncia a reindexação automática (toda mudança automática se anuncia) e some quando nada mudou.
func indexNote(st graph.Stats) string {
	if st.Changed+st.Removed == 0 {
		return ""
	}
	return fmt.Sprintf("%s: reindexed %d file(s) just now (%d files indexed, %d ms)", GraphMarkIndex, st.Changed+st.Removed, st.Files, st.Took.Milliseconds())
}

func notes(list ...string) string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func defPhrase(defs []graph.Def) string {
	if len(defs) == 1 {
		return fmt.Sprintf("1 definition (%s:%d-%d)", defs[0].File, defs[0].Start, defs[0].End)
	}
	return fmt.Sprintf("ambiguous: %d definitions", len(defs))
}

// shortSig deixa a assinatura numa linha curta (a resposta é pronta, não é o arquivo).
func shortSig(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 76 {
		s = s[:76] + "..."
	}
	return s
}

func kindOf(d graph.Def) string {
	if d.Kind == "" {
		return "decl"
	}
	return d.Kind
}

// buildSymbolAnswer monta a resposta no formato da pergunta: definição (com corpo, quando é única e curta), ou a
// lista de arquivos (-l), ou as contagens (-c); sempre com o rodapé que diz o que o índice cobre e como pedir a
// busca crua.
func buildSymbolAnswer(root, where string, req searchReq, defs []graph.Def, counts map[string]int, escape, announce string) string {
	refs, tests := 0, 0
	for f, n := range counts {
		refs += n
		if graph.TestFile(f) {
			tests++
		}
	}
	var b strings.Builder
	switch {
	case req.list:
		fmt.Fprintf(&b, "%s: files matching '%s' in %s — %s; %d file(s) reference it\n", GraphMarkSearch, req.pattern, where, defPhrase(defs), len(counts))
	case req.count:
		fmt.Fprintf(&b, "%s: counts for '%s' in %s — %s (graph counts: references per file, one per line, not grep -c matches)\n", GraphMarkSearch, req.pattern, where, defPhrase(defs))
	default:
		fmt.Fprintf(&b, "%s: '%s' in %s — %s; %d reference(s) in %d file(s)\n", GraphMarkSearch, req.pattern, where, defPhrase(defs), refs, len(counts))
	}
	if announce != "" {
		b.WriteString(announce + "\n")
	}

	switch {
	case req.list:
		files := unionFiles(defs, counts)
		if len(files) > graphMaxListFiles {
			files = files[:graphMaxListFiles]
		}
		for _, f := range files {
			b.WriteString("  " + f + "\n")
		}
	case req.count:
		// Arquivos que só declaram (nenhuma referência) também entram: o grep -c os listaria com o casamento da
		// própria declaração, e sumir com eles faria a resposta parecer incompleta.
		for _, f := range countOrder(defs, counts) {
			n := counts[f]
			label := ""
			if n == 0 {
				label = "  (declares it)"
			}
			fmt.Fprintf(&b, "  %s: %d%s\n", f, n, label)
		}
	default:
		if len(defs) == 1 {
			d := defs[0]
			fmt.Fprintf(&b, "def: %s:%d-%d (%s)\n", d.File, d.Start, d.End, kindOf(d))
			if body := graph.SymbolBody(root, d, graphBodyMaxLines); body != "" {
				b.WriteString(body + "\n")
			} else {
				fmt.Fprintf(&b, "  %s\n  %d lines: read_file('%s', %d, %d)\n", shortSig(d.Sig), d.End-d.Start+1, d.File, d.Start, d.End)
			}
		} else {
			fmt.Fprintf(&b, "def: ambiguous (%d definitions) — the name is declared in more than one place\n", len(defs))
			for i, d := range defs {
				if i == graphMaxDefs {
					fmt.Fprintf(&b, "  ... +%d more definitions (code_graph action 'definition')\n", len(defs)-i)
					break
				}
				fmt.Fprintf(&b, "  %s:%d-%d (%s) %s\n", d.File, d.Start, d.End, kindOf(d), shortSig(d.Sig))
			}
		}
		if len(counts) == 0 {
			b.WriteString("callers: no indexed reference\n")
		} else {
			fmt.Fprintf(&b, "callers: %d reference(s) in %d file(s) — non-test: %d file(s) | tests: %d file(s)\n", refs, len(counts), len(counts)-tests, tests)
			for _, f := range orderedFiles(counts) {
				fmt.Fprintf(&b, "  %s (%d)\n", f, counts[f])
			}
		}
	}

	footer := "index covers code identifiers only (comments, strings and docs are not listed). For the raw search: " + escape
	return capAnswer(strings.TrimRight(b.String(), "\n"), footer)
}

// answerFileSearch responde a busca por nome de arquivo com os arquivos do repositório (e um resumo das
// declarações quando dá para apontar um arquivo só).
func answerFileSearch(root, sub, where, needle, escape, announce string) (out string, handled bool, note string) {
	files := filesUnderSub(graph.FindFiles(root, needle, graphMaxListFiles+1), sub)
	if len(files) == 0 {
		return "", false, announce
	}
	total := len(files)
	if len(files) > graphMaxListFiles {
		files = files[:graphMaxListFiles]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d file(s) in %s with '%s' in the path\n", GraphMarkSearch, total, where, needle)
	if announce != "" {
		b.WriteString(announce + "\n")
	}
	for _, f := range files {
		b.WriteString("  " + f + "\n")
	}
	if total > len(files) {
		fmt.Fprintf(&b, "  ... +%d more\n", total-len(files))
	}
	if total <= 3 {
		if o := shortOutline(root, files[0], graphMaxOutline); o != "" {
			fmt.Fprintf(&b, "--- outline: %s ---\n%s\n", files[0], o)
		}
	}
	footer := "from the index (files the scanner understands). For the raw listing: " + escape
	return capAnswer(strings.TrimRight(b.String(), "\n"), footer), true, ""
}

// shortOutline lista as declarações de um arquivo indexado (o mesmo formato do read_file com grafo primeiro).
func shortOutline(root, rel string, max int) string {
	lang := codescan.LangFor(rel)
	if lang == nil {
		return ""
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	decls, _ := codescan.Scan(src, lang)
	if len(decls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range decls {
		if i == max {
			fmt.Fprintf(&b, "  ... +%d more declaration(s)\n", len(decls)-max)
			break
		}
		if d.EndLine > d.Line {
			fmt.Fprintf(&b, "L%d-L%d: %s\n", d.Line, d.EndLine, d.Sig)
		} else {
			fmt.Fprintf(&b, "L%d: %s\n", d.Line, d.Sig)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// capAnswer corta o corpo no teto de bytes preservando o rodapé: a saída de emergência nunca some.
func capAnswer(body, footer string) string {
	if len(body)+len(footer)+1 <= graphAnswerMax {
		return body + "\n" + footer
	}
	budget := graphAnswerMax - len(footer) - 1 - 48
	lines := strings.Split(body, "\n")
	var keep []string
	n := 0
	for _, l := range lines {
		if n+len(l)+1 > budget {
			break
		}
		keep = append(keep, l)
		n += len(l) + 1
	}
	keep = append(keep, fmt.Sprintf("... +%d more line(s); narrow the search or use code_graph", len(lines)-len(keep)))
	return strings.Join(keep, "\n") + "\n" + footer
}

func defsUnderSub(defs []graph.Def, sub string) []graph.Def {
	if sub == "" {
		return defs
	}
	var out []graph.Def
	for _, d := range defs {
		if pathUnderSub(d.File, sub) {
			out = append(out, d)
		}
	}
	return out
}

func countsUnderSub(counts map[string]int, sub string) map[string]int {
	if sub == "" {
		return counts
	}
	out := map[string]int{}
	for f, n := range counts {
		if pathUnderSub(f, sub) {
			out[f] = n
		}
	}
	return out
}

func filesUnderSub(files []string, sub string) []string {
	if sub == "" {
		return files
	}
	var out []string
	for _, f := range files {
		if pathUnderSub(f, sub) {
			out = append(out, f)
		}
	}
	return out
}

func pathUnderSub(file, sub string) bool {
	return strings.HasPrefix(file, sub+"/")
}

// orderedFiles põe os arquivos que não são de teste na frente e, dentro de cada grupo, os mais citados primeiro.
func orderedFiles(counts map[string]int) []string {
	files := make([]string, 0, len(counts))
	for f := range counts {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		ti, tj := graph.TestFile(files[i]), graph.TestFile(files[j])
		if ti != tj {
			return !ti
		}
		if counts[files[i]] != counts[files[j]] {
			return counts[files[i]] > counts[files[j]]
		}
		return files[i] < files[j]
	})
	return files
}

// countOrder é a ordem de -c: os arquivos que referenciam (mais citados primeiro, não-testes antes) e depois os
// que só declaram, com 0. Teto de linhas igual ao de -l (o corte por bytes não é a primeira defesa).
func countOrder(defs []graph.Def, counts map[string]int) []string {
	files := orderedFiles(counts)
	seen := map[string]bool{}
	for _, f := range files {
		seen[f] = true
	}
	var declOnly []string
	for _, d := range defs {
		if !seen[d.File] {
			seen[d.File] = true
			declOnly = append(declOnly, d.File)
		}
	}
	sortNonTest(declOnly)
	files = append(files, declOnly...)
	if len(files) > graphMaxListFiles {
		files = files[:graphMaxListFiles]
	}
	return files
}

// sortNonTest põe os arquivos que não são de teste na frente, mantendo a ordem alfabética dentro do grupo.
func sortNonTest(files []string) {
	sort.SliceStable(files, func(i, j int) bool {
		ti, tj := graph.TestFile(files[i]), graph.TestFile(files[j])
		if ti != tj {
			return !ti
		}
		return files[i] < files[j]
	})
}

// unionFiles junta os arquivos que declaram o símbolo com os que o referenciam (-l responde pelos dois).
func unionFiles(defs []graph.Def, counts map[string]int) []string {
	seen := map[string]bool{}
	var files []string
	for _, d := range defs {
		if !seen[d.File] {
			seen[d.File] = true
			files = append(files, d.File)
		}
	}
	for f := range counts {
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		ti, tj := graph.TestFile(files[i]), graph.TestFile(files[j])
		if ti != tj {
			return !ti
		}
		return files[i] < files[j]
	})
	return files
}
