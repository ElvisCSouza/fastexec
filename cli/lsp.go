package cli

// lsp.go: `fastexec lsp doctor` lista, por linguagem, se o servidor de linguagem
// foi encontrado neste PC e qual é a versão. Não instala nada: quando o servidor
// falta, imprime o comando exato que o instalaria, e para por aí. O único
// processo que ele chega a iniciar é o próprio servidor com a sua flag de versão.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"homelab/fastexec/internal/lsp"
)

func cmdLsp(subArgs []string) {
	if len(subArgs) == 0 {
		fmt.Println("usage: fastexec lsp doctor")
		os.Exit(1)
	}
	switch strings.ToLower(subArgs[0]) {
	case "doctor":
		lspDoctor(subArgs[1:])
	default:
		fmt.Printf("Unknown 'lsp' subcommand: '%s'\n", subArgs[0])
		fmt.Println("Subcommands: doctor")
		os.Exit(1)
	}
}

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
			if e.Language.Install != "" {
				fmt.Printf("  %-24s   install: %s\n", "", e.Language.Install)
			}
			if e.Language.Note != "" {
				fmt.Printf("  %-24s   note: %s\n", "", e.Language.Note)
			}
		case e.Version == "":
			fmt.Printf("  %s found %s (version unreadable: %s)\n", name, e.Path, e.Problem)
			found++
		default:
			fmt.Printf("  %s %s  %s\n", name, e.Version, e.Path)
			found++
		}
	}
	fmt.Printf("\n%d of %d language server(s) available; the rest fall back to the graph by name.\n",
		found, len(entries))
	fmt.Println("Nothing is downloaded automatically: install a server and re-run `fastexec lsp doctor`.")
}
