package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/mcp"
)

func main() {
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
	subArgs := args[1:]

	if run, ok := commands[cmd]; ok {
		run(subArgs)
		return
	}
	fmt.Printf("Unknown command: '%s'\n", cmd)
	fmt.Println("Commands: mcp, exec, get, put, sql, job, ping, stats, handoff, graph, config, firestick, daemon")
	os.Exit(1)
}
