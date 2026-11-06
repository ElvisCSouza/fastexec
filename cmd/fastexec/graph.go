package main

// graph.go: subcomando "fastexec graph" (callers, def, impact, outline, index) sobre internal/graph e code_outline.

import (
	"fmt"
	"os"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/tools"
)

const graphUsage = `Usage:
  fastexec graph callers <symbol> [repo_path]
  fastexec graph def <symbol> [repo_path]
  fastexec graph impact <file_or_symbol> [repo_path]
  fastexec graph outline <file>
  fastexec graph index [repo_path]   (full rebuild)`

// runGraph devolve o código de saída.
func runGraph(args []string) int {
	if len(args) == 0 {
		fmt.Println(graphUsage)
		return 1
	}
	action, rest := args[0], args[1:]
	arg := func(i int, def string) string {
		if i < len(rest) {
			return rest[i]
		}
		return def
	}

	if action == "outline" {
		if len(rest) == 0 {
			fmt.Println(graphUsage)
			return 1
		}
		return printResult(tools.HandleCodeOutline(tools.CodeOutlineArgs{Host: "local", Path: rest[0]}))
	}

	repoArg := "."
	switch action {
	case "callers", "def", "impact":
		if len(rest) == 0 {
			fmt.Println(graphUsage)
			return 1
		}
		repoArg = arg(1, ".")
	case "index":
		repoArg = arg(0, ".")
	default:
		fmt.Println(graphUsage)
		return 1
	}
	root, ok := graph.ResolveRepoRoot(repoArg)
	if !ok {
		fmt.Fprintln(os.Stderr, graph.ErrNotInRepo(repoArg))
		return 1
	}
	switch action {
	case "callers":
		return printResult(graph.Callers(root, rest[0], 0))
	case "def":
		return printResult(graph.Definition(root, rest[0]))
	case "impact":
		return printResult(graph.Impact(root, rest[0], 0))
	}
	st, err := graph.Reindex(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("indexed %s: %d files in %d ms\n", root, st.Changed, st.Took.Milliseconds())
	return 0
}

func printResult(out string, err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(out)
	return 0
}
