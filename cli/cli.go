// Package cli é a entrada do FastExec: servidor MCP (sem argumentos ou 'mcp') e subcomandos de terminal.
//
// É o ponto de extensão público: um build próprio (outro módulo Go) importa este pacote, acrescenta
// subcomandos com Register e chama Main. O binário público é só `func main() { cli.Main() }`.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/mcp"
)

// builtinOrder é a ordem dos comandos na mensagem de uso (aliases e internos como daemon-run ficam de fora).
var builtinOrder = []string{"mcp", "exec", "get", "put", "sql", "job", "ping", "stats", "handoff", "graph", "config", "daemon", "version"}

// Version é gravada no build (-ldflags "-X homelab/fastexec/cli.Version=..."); 'dev' em build local sem ela.
var Version = "dev"

// registered: subcomandos acrescentados por Register, na ordem de registro.
var registered []string

// Register acrescenta um subcomando ao CLI. Chamar antes de Main; nome repetido (inclusive de um embutido) é erro
// de programação e para o processo.
func Register(name string, run func(args []string)) {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, dup := commands[name]; dup || name == "" || name == "mcp" {
		panic(fmt.Sprintf("fastexec: command %q already exists", name))
	}
	commands[name] = run
	registered = append(registered, name)
}

// cmdVersion atende: version. Versão gravada, commit do build (módulo principal) e extensões registradas.
func cmdVersion(_ []string) {
	line := "fastexec " + Version
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				line += " (" + bi.Main.Path + "@" + s.Value[:7] + ")"
			}
		}
	}
	if len(registered) > 0 {
		line += " + " + strings.Join(registered, ", ")
	}
	fmt.Println(line)
}

// Main roda o FastExec com os argumentos do processo.
func Main() {
	logger.Init()

	args := os.Args[1:]
	if len(args) == 0 || args[0] == "mcp" {
		server := mcp.NewServer()
		server.Forwarder = func(name string, rawArgs json.RawMessage) (mcp.ToolCallResult, error) {
			res, err := daemon.CallTool(name, rawArgs)
			if err != nil {
				return mcp.ToolCallResult{}, err
			}
			return *res, nil
		}
		if err := server.Run(); err != nil {
			logger.Error("MCP server stopped with error: %v", err)
			os.Exit(1)
		}
		return
	}

	// Modo CLI direto para uso no terminal ou shims
	cmd := strings.ToLower(args[0])
	if run, ok := commands[cmd]; ok {
		run(args[1:])
		return
	}
	fmt.Printf("Unknown command: '%s'\n", cmd)
	fmt.Println("Commands: " + strings.Join(append(append([]string{}, builtinOrder...), registered...), ", "))
	os.Exit(1)
}
