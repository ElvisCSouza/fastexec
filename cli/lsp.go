package cli

// lsp.go: `fastexec lsp` — doctor, install, update, remove.
//
// A regra que manda neste arquivo: nada é baixado sem o usuário ver o que é e
// dizer sim. `install` monta o plano (servidor, versão fixada, tamanho, origem,
// licença), pré-seleciona as linguagens que ele realmente usa, e pergunta uma a
// uma. Sem terminal, só roda com `--yes --lang ...` explícito. `doctor` não
// baixa nada: ele lê o que existe e mostra o estado.

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"homelab/fastexec/internal/lsp"
)

const lspUsage = `Usage: fastexec lsp <command>

  doctor                    what is installed, what is missing, and what a server would cost
  install [linguagem,...]   download and install the pinned language servers (asks first)
  update                    reinstall the servers whose pinned version changed
  remove <servidor>         delete a server that FastExec installed

install/update flags:
  --lang a,b,c   languages to install (default: the ones detected in your repositories)
  --root <dir>   also scan this folder for languages in use (repeatable)
  --yes          answer yes to every confirmation; required when there is no terminal
  --dry-run      show the plan and download nothing

Language names accept short spellings: go, cs/c#, ts/js, py, rs, cpp.`

func cmdLsp(subArgs []string) {
	if len(subArgs) == 0 {
		fmt.Println(lspUsage)
		os.Exit(1)
	}
	switch strings.ToLower(subArgs[0]) {
	case "doctor":
		lspDoctor(subArgs[1:])
	case "install":
		lspInstall(subArgs[1:])
	case "update":
		lspUpdate(subArgs[1:])
	case "remove":
		lspRemove(subArgs[1:])
	default:
		fmt.Printf("Unknown 'lsp' subcommand: '%s'\n", subArgs[0])
		fmt.Println(lspUsage)
		os.Exit(1)
	}
}

// --- argumentos ---

// lspArgs é o que a linha de comando pediu, já separado dos argumentos posicionais.
type lspArgs struct {
	Langs  []string // ids (ou apelidos) pedidos; vazio = o conjunto detectado
	Roots  []string // pastas extras a varrer
	Yes    bool     // responde sim a tudo (obrigatório sem terminal)
	DryRun bool     // só mostra o plano
	Rest   []string // posicionais restantes (o servidor de `remove`)
}

// parseLspArgs lê os argumentos de install/update/remove. Aceita `--flag valor` e
// `--flag=valor`, e trata o primeiro posicional como linguagem (install) ou
// servidor (remove).
func parseLspArgs(args []string, usage string) (lspArgs, error) {
	var a lspArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			a.Rest = append(a.Rest, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		// valor que vem no argumento seguinte
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "--yes", "-y":
			a.Yes = true
		case "--dry-run", "-n":
			a.DryRun = true
		case "--lang", "--language", "-l":
			v, err := takeValue()
			if err != nil {
				return a, err
			}
			a.Langs = append(a.Langs, v)
		case "--root", "--dir", "-C":
			v, err := takeValue()
			if err != nil {
				return a, err
			}
			a.Roots = append(a.Roots, v)
		default:
			return a, fmt.Errorf("unknown flag %q for `%s`\n\n%s", name, usage, lspUsage)
		}
	}
	return a, nil
}

// lspPlan monta o plano a partir das pastas já resolvidas. Recebe os roots
// prontos em vez de ir buscá-los, para que o teste descreva uma máquina de
// mentira sem varrer o repositório de verdade.
func lspPlan(roots []string, langs []string) ([]lsp.Item, lsp.Selection, error) {
	items := lsp.PlanAll(lsp.DetectCounts(roots...))
	sel, err := lsp.Choose(items, langs)
	return items, sel, err
}

// --- doctor ---

// lspDoctor runs the diagnostic. It exits 0 even when servers are missing:
// a missing optional server is not a broken installation, and failing here
// would make `lsp doctor` unusable in a script that only wants the listing.
func lspDoctor(_ []string) {
	if lsp.Disabled() {
		fmt.Println("LSP is disabled by FASTEXEC_LSP: every call falls back to the graph by name.")
		fmt.Println("The listing below is what is installed, not what is being used.")
		fmt.Println()
	}
	ctx := context.Background()
	entries := lsp.Doctor(ctx)

	found := 0
	fmt.Println("Language servers")
	for _, e := range entries {
		name := fmt.Sprintf("%-24s", e.Language.Name)
		switch {
		case !e.Found:
			fmt.Printf("  %s not found\n", name)
			// The pinned download first, then every server's own install
			// command and note: a language with a secondary server has more
			// than one way in, and the second one is a real answer too.
			printPinned(e.Language)
			printServerHints(e.Language)
		case e.NoVersion:
			fmt.Printf("  %s %s  %s   (no version flag)%s\n", name, e.Server.Name, e.Path, fallbackNote(e))
			found++
		case e.Version == "":
			fmt.Printf("  %s %s found %s (version unreadable: %s)%s\n", name, e.Server.Name, e.Path, e.Problem, fallbackNote(e))
			printDetails(e)
			found++
		default:
			fmt.Printf("  %s %s %s  %s%s\n", name, e.Server.Name, e.Version, e.Path, fallbackNote(e))
			printDetails(e)
			found++
		}
	}
	fmt.Printf("\n%d of %d language server(s) available; the rest fall back to the graph by name.\n",
		found, len(entries))
	fmt.Println("Optional languages (Markdown, JSON, YAML, Shell, PowerShell) answer by name already: a missing server there is not a degradation.")

	printInstalled()
	fmt.Println("Nothing is downloaded without asking: `fastexec lsp install` shows the pinned version,")
	fmt.Println("the size, the origin and the licence of each server before you confirm it.")
}

// printPinned shows, for a language whose server is missing, what `lsp install`
// would download: the fixed version, the size, where it comes from, the licence
// and what the machine still needs. A third-party component the user is about to
// fetch must be named with its licence before the question, not after.
func printPinned(lang lsp.Language) {
	pin, ok := lsp.PinFor(lang)
	if !ok {
		fmt.Printf("  %-24s   install: fastexec lsp install %s (no pinned download; see manual)\n", "", lang.ID)
		return
	}
	line := fmt.Sprintf("%s %s, %s, %s, %s", pin.Server, pin.Version, pin.SizeFor(runtime.GOOS, runtime.GOARCH), pin.Origin, pin.License)
	fmt.Printf("  %-24s   install: fastexec lsp install %s  (%s)\n", "", lang.ID, line)
	for _, pr := range lsp.Prereqs(pin) {
		if pr.Found {
			continue
		}
		fmt.Printf("  %-24s   needs %s (%s): %s\n", "", pr.Tool, pr.Why, pr.Install)
	}
}

// printInstalled lists what FastExec itself put under <data_dir>/lsp, with the
// licence of each third-party component and whether the catalogue moved on.
func printInstalled() {
	list := lsp.InstalledList()
	if len(list) == 0 {
		return
	}
	fmt.Printf("\nInstalled by FastExec in %s:\n", lsp.InstallDir())
	for _, st := range list {
		fmt.Printf("  %-28s %s  %s\n", st.Server, st.Version, st.License)
		if pin, ok := lsp.PinByServer(st.Server); ok {
			switch {
			case pin.Version == st.Version:
				fmt.Printf("  %-28s up to date\n", "")
			default:
				fmt.Printf("  %-28s update available: the catalogue pins %s (`fastexec lsp update`)\n", "", pin.Version)
			}
		}
		fmt.Printf("  %-28s %s\n", "", st.Bin)
	}
}

// --- install ---

func lspInstall(args []string) {
	a, err := parseLspArgs(args, "install")
	if err != nil {
		fail(err)
	}
	if err := runLSPInstall(a, lsp.DetectRoots(a.Roots...)); err != nil {
		fail(err)
	}
}

// setupLSP é o passo opcional "servidores de linguagem" do `fastexec setup`.
// Não baixa nada por conta própria: mostra o plano (versão fixada, tamanho,
// origem, licença) e pergunta linguagem por linguagem. Sem terminal e sem
// `--yes --lang`, não instala e diz qual comando faz isso: um "sim" que ninguém
// digitou não vale, e o setup não pode parar por causa de um passo opcional.
func setupLSP(dry, yes bool, langs []string) error {
	if len(langs) == 0 && !isTerminal() {
		fmt.Println("\nLanguage servers: not offered here (no terminal to ask).")
		fmt.Println("Run `fastexec lsp install`: it shows the pinned version, the size, the origin")
		fmt.Println("and the licence of each server, and downloads nothing before you confirm it.")
		return nil
	}
	fmt.Println()
	return runLSPInstall(lspArgs{Langs: langs, Yes: yes, DryRun: dry}, lsp.DetectRoots())
}

// runLSPInstall é o `lsp install` inteiro sem os.Exit: o `setup` chama o mesmo
// caminho para oferecer o passo opcional sem derrubar a instalação do FastExec
// quando um servidor de linguagem falha.
func runLSPInstall(a lspArgs, roots []string) error {
	if len(a.Rest) > 0 {
		a.Langs = append(a.Langs, a.Rest...)
	}
	items, sel, err := lspPlan(roots, a.Langs)
	if err != nil {
		return err
	}

	implicit := len(a.Langs) == 0
	fmt.Println("Language servers that can be installed")
	printPlan(items, implicit)
	printBlocked(sel.Blocked)

	if len(sel.Items) == 0 {
		if implicit {
			fmt.Println("\nNothing to install: no installable language was detected in your repositories.")
			fmt.Println("Ask for one explicitly, e.g. `fastexec lsp install --lang go,cs,ts`.")
		} else {
			fmt.Println("\nNothing to install: see the lines above.")
		}
		return nil
	}
	if a.DryRun {
		fmt.Printf("\n--dry-run: %d language(s) would be installed; nothing was downloaded.\n", len(sel.Items))
		return nil
	}

	chosen, err := confirm(sel.Items, a.Yes, "Install")
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		fmt.Println("\nNothing was installed.")
		return nil
	}
	return installAll(chosen, "install")
}

// --- update ---

func lspUpdate(args []string) {
	a, err := parseLspArgs(args, "update")
	if err != nil {
		fail(err)
	}
	// --lang narrows the update to some languages; the ids are validated before
	// anything is printed, so a typo fails while the machine is still untouched.
	wanted := map[string]bool{}
	if len(a.Langs) > 0 {
		items, sel, err := lspPlan(nil, a.Langs)
		_ = sel
		if err != nil {
			fail(err)
		}
		for _, it := range items {
			wanted[it.Lang.ID] = true
		}
	}

	var stale, gone []lsp.Item
	for _, it := range lsp.PlanAll(nil) {
		if it.Installed == nil || (len(wanted) > 0 && !wanted[it.Lang.ID]) {
			continue
		}
		switch {
		case it.Pinned && !it.UpToDate:
			stale = append(stale, it)
		case !it.Pinned:
			gone = append(gone, it)
		}
	}

	if len(gone) > 0 {
		fmt.Println("Installed but no longer in the pinned catalogue (left alone):")
		for _, it := range gone {
			fmt.Printf("  %-24s %s %s\n", it.Lang.Name, it.Installed.Server, it.Installed.Version)
		}
		fmt.Println()
	}
	if len(stale) == 0 {
		fmt.Println("Every server FastExec installed is already at the pinned version.")
		fmt.Println("`fastexec lsp doctor` shows what is installed.")
		return
	}

	fmt.Println("Servers whose pinned version changed")
	for _, it := range stale {
		fmt.Printf("  %-24s %s  %s -> %s  (%s, %s, %s)\n", it.Lang.Name, it.Pin.Server,
			it.Installed.Version, it.Pin.Version, it.Size, it.Pin.Origin, it.Pin.License)
	}
	if a.DryRun {
		fmt.Printf("\n--dry-run: %d server(s) would be updated; nothing was downloaded.\n", len(stale))
		return
	}
	chosen, err := confirm(stale, a.Yes, "Update")
	if err != nil {
		fail(err)
	}
	if len(chosen) == 0 {
		fmt.Println("\nNothing was updated.")
		return
	}
	if err := installAll(chosen, "update"); err != nil {
		fail(err)
	}
}

// --- remove ---

func lspRemove(args []string) {
	a, err := parseLspArgs(args, "remove")
	if err != nil {
		fail(err)
	}
	if len(a.Rest) != 1 {
		fail(fmt.Errorf("remove needs exactly one server: `fastexec lsp remove <servidor>`\n\n%s", lspUsage))
	}
	server := resolveServer(a.Rest[0])
	in := lsp.NewInstaller()
	st, ok := in.State(server)
	if !ok {
		fail(fmt.Errorf("%s was not installed by FastExec in %s; nothing to remove", server, lsp.InstallDir()))
	}
	fmt.Printf("%s %s (%s) is at %s\n", st.Server, st.Version, st.License, st.Bin)
	if !confirmYesNo(fmt.Sprintf("Remove it? FastExec will stop using it and the folder is deleted [y/N] "), a.Yes) {
		fmt.Println("Nothing was removed.")
		return
	}
	if err := in.Remove(server); err != nil {
		fail(err)
	}
	fmt.Printf("Removed %s. `fastexec lsp install %s` puts it back.\n", server, st.Lang)
}

// resolveServer aceita tanto o nome do executável (gopls) quanto o id da
// linguagem (go): o usuário pensa em "go", o doctor imprime "gopls".
func resolveServer(arg string) string {
	if lang, ok := lsp.LanguageByID(lsp.NormalizeLangID(arg)); ok {
		if pin, ok := lsp.PinFor(lang); ok {
			return pin.Server
		}
		return lang.Primary().Name
	}
	return strings.TrimSpace(arg)
}

// --- confirmação e execução ---

// confirm pergunta uma a uma. Sem --yes e sem ninguém para perguntar, não
// instala nada: um "sim" que ninguém digitou não vale.
func confirm(items []lsp.Item, yes bool, verb string) ([]lsp.Item, error) {
	var ask lsp.Confirm
	if !yes && isTerminal() {
		ask = lsp.Ask(os.Stdin, os.Stdout, func(it lsp.Item) string {
			return fmt.Sprintf("%s %s %s (%s, %s, %s)? [y/N] ",
				verb, it.Pin.Server, it.Pin.Version, it.Size, it.Pin.Origin, it.Pin.License)
		})
	}
	return decideConfirmation(items, yes, ask, verb)
}

// decideConfirmation é a decisão inteira, sem terminal e sem rede: é o que os
// testes chamam para provar que sem --yes e sem alguém para responder nada é
// baixado. ask é nil quando não há a quem perguntar.
func decideConfirmation(items []lsp.Item, yes bool, ask lsp.Confirm, verb string) ([]lsp.Item, error) {
	if yes {
		return items, nil
	}
	if ask == nil {
		return nil, fmt.Errorf("no terminal to ask for confirmation, so nothing was installed.\n"+
			"If you really mean it, name the languages and add --yes, e.g.\n"+
			"  fastexec lsp %s --yes --lang %s",
			strings.ToLower(verb), strings.Join(idsOf(items), ","))
	}
	var out []lsp.Item
	for _, it := range items {
		if ask(it) {
			out = append(out, it)
			continue
		}
		fmt.Printf("  skipped %s\n", it.Lang.ID)
	}
	return out, nil
}

func confirmYesNo(question string, yes bool) bool {
	if yes {
		return true
	}
	if !isTerminal() {
		return false
	}
	fmt.Print(question)
	var line string
	if _, err := fmt.Scanln(&line); err != nil {
		return false
	}
	switch strings.ToLower(line) {
	case "y", "yes", "s", "sim":
		return true
	}
	return false
}

// installAll roda o instalador item a item: uma linguagem que falha não impede
// as outras, e devolve erro se alguma falhou (o chamador decide se isso derruba
// o comando — no `setup` não derruba).
func installAll(items []lsp.Item, verb string) error {
	in := lsp.NewInstaller()
	in.Log = func(format string, a ...any) { fmt.Printf("     %s\n", fmt.Sprintf(format, a...)) }
	ok := true
	for _, it := range items {
		fmt.Printf("\n%s %s %s\n", capitalize(verb), it.Pin.Server, it.Pin.Version)
		st, err := in.Install(context.Background(), it.Pin)
		if err != nil {
			ok = false
			fmt.Fprintf(os.Stderr, "  FAILED: %v\n", err)
			continue
		}
		fmt.Printf("  installed %s %s in %s\n", st.Server, st.Version, st.Bin)
		fmt.Printf("  licence: %s — %s\n", st.License, pinHome(st.Server))
	}
	if !ok {
		return fmt.Errorf("some servers failed; their previous version (if any) was left untouched")
	}
	fmt.Println("\nDone. `fastexec lsp doctor` shows the result.")
	return nil
}

func pinHome(server string) string {
	if pin, ok := lsp.PinByServer(server); ok {
		return pin.Home
	}
	return ""
}

// --- impressão do plano ---

// printPlan mostra linha a linha o que existe, o que falta e o que está
// marcado. Em `install` sem --lang, o marcado é exatamente o detectado.
func printPlan(items []lsp.Item, implicit bool) {
	for _, it := range items {
		mark := "  "
		switch {
		case it.Ok && (it.Detected || !implicit):
			mark = "[x]"
		default:
			mark = "[ ]"
		}
		state := ""
		switch {
		case it.UpToDate:
			state = "already at the pinned version"
		case it.Installed != nil:
			state = "installed " + it.Installed.Version
		case it.Detected:
			state = fmt.Sprintf("in use here (%d file(s))", it.Files)
		}
		if state != "" {
			state = "  — " + state
		}
		fmt.Printf("  %s %-24s %s%s\n", mark, it.Lang.Name, summaryOf(it), state)
		if len(it.Missing) > 0 {
			for _, pr := range it.Requires {
				if !pr.Found {
					fmt.Printf("      needs %s (%s): %s\n", pr.Tool, pr.Why, pr.Install)
				}
			}
		}
		if !it.Pinned && it.Manual != "" {
			fmt.Printf("      no pinned download; manual: %s\n", it.Manual)
		}
	}
}

// summaryOf é o que a confirmação promete: nome, versão fixada, tamanho,
// origem e licença — nunca "latest".
func summaryOf(it lsp.Item) string {
	if !it.Pinned {
		return "not installable by FastExec"
	}
	return fmt.Sprintf("%s %s, %s, %s, %s", it.Pin.Server, it.Version, it.Size, it.Origin, it.License)
}

// printBlocked explica, uma a uma, as linguagens que o usuário pediu e que não
// podem ser instaladas agora: o resto da seleção continua.
func printBlocked(blocked []lsp.Item) {
	if len(blocked) == 0 {
		return
	}
	fmt.Println("\nNot installable on this machine")
	for _, it := range blocked {
		switch {
		case len(it.Missing) > 0:
			fmt.Printf("  %-24s missing prerequisite: %s\n", it.Lang.Name, strings.Join(it.Missing, ", "))
			for _, pr := range it.Requires {
				if !pr.Found {
					fmt.Printf("  %-24s   %s\n", "", pr.Install)
				}
			}
			fmt.Printf("  %-24s   FastExec does not install SDKs; the language is skipped.\n", "")
		default:
			fmt.Printf("  %-24s no pinned download\n", it.Lang.Name)
			if it.Manual != "" {
				fmt.Printf("  %-24s   manual: %s\n", "", it.Manual)
			}
		}
	}
}

// --- utilidades ---

func idsOf(items []lsp.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Lang.ID)
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// isTerminal diz se dá para perguntar alguma coisa. Sem terminal, `install` só
// segue com --yes explícito.
func isTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// printDetails prints the extra lines a found server has to say about itself —
// for the TypeScript server, which tsserver it will drive.
func printDetails(e lsp.DoctorEntry) {
	for _, d := range e.Details {
		fmt.Printf("  %-24s   %s\n", "", d)
	}
}

// printServerHints lists what would install each server of a language, best
// first, plus the note that explains what a missing one needs. It prints text
// only; FastExec never runs any of it.
func printServerHints(lang lsp.Language) {
	for i, srv := range lang.Servers {
		verb := "install:"
		if i > 0 {
			verb = "or:"
		}
		if srv.Install != "" {
			fmt.Printf("  %-24s   %-8s %s\n", "", verb, srv.Install)
		}
		if len(srv.Requires) > 0 {
			fmt.Printf("  %-24s   %-8s %s\n", "", "needs:", strings.Join(srv.Requires, ", "))
		}
		if srv.Note != "" {
			fmt.Printf("  %-24s   %-8s %s\n", "", "note:", srv.Note)
		}
	}
}

// fallbackNote marks a language being served by something other than its
// preferred server: the hints are then not about the server that is running.
func fallbackNote(e lsp.DoctorEntry) string {
	if e.Server.Name == e.Language.Primary().Name {
		return ""
	}
	return "   (fallback; " + e.Language.Primary().Name + " is preferred)"
}
