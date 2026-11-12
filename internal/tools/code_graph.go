// code_graph.go: tool code_graph. Encaminha callers/def/impact/index para internal/graph no repositório local de
// repo_path (raiz achada pelo .git; o índice é criado no primeiro uso e atualizado de forma incremental a cada consulta).
package tools

import (
	"fmt"
	"strings"

	"homelab/fastexec/internal/graph"
)

type CodeGraphArgs struct {
	Action   string `json:"action"`
	Symbol   string `json:"symbol"`
	Target   string `json:"target"`
	RepoPath string `json:"repo_path"`
	Max      int    `json:"max"`
	Depth    int    `json:"depth"`
}

func HandleCodeGraph(args CodeGraphArgs) (string, error) {
	if args.RepoPath == "" {
		return "", fmt.Errorf("parameter 'repo_path' is required (repository root or any path inside it, on host 'local')")
	}
	root, ok := graph.ResolveRepoRoot(expandHome(args.RepoPath))
	if !ok {
		return "", graph.ErrNotInRepo(args.RepoPath)
	}
	need := func(v, name string) error {
		if v == "" {
			return fmt.Errorf("action '%s' needs '%s'", args.Action, name)
		}
		return nil
	}
	switch strings.ToLower(args.Action) {
	case "callers":
		if err := need(args.Symbol, "symbol"); err != nil {
			return "", err
		}
		// Exato quando o servidor de linguagem responde (distingue dois métodos de mesmo nome em tipos
		// diferentes); por nome quando não há servidor, com uma linha dizendo por quê.
		return courseWithGraph(root, args.Symbol,
			func() (string, error) { return lspCallers(root, args.Symbol, args.Max, args.Depth) },
			func() (string, error) { return graph.Callers(root, args.Symbol, args.Max, args.Depth) })
	case "def", "definition":
		if err := need(args.Symbol, "symbol"); err != nil {
			return "", err
		}
		return courseWithGraph(root, args.Symbol,
			func() (string, error) { return lspDefinition(root, args.Symbol) },
			func() (string, error) { return graph.Definition(root, args.Symbol) })
	case "impact":
		t := args.Target
		if t == "" {
			t = args.Symbol
		}
		if err := need(t, "target"); err != nil {
			return "", err
		}
		return courseWithGraph(root, t,
			func() (string, error) { return lspImpact(root, t, args.Max) },
			func() (string, error) { return graph.Impact(root, t, args.Max) })
	case "index":
		st, err := graph.Reindex(root)
		if err != nil {
			return "", err
		}
		docs := ""
		if st.Docs > 0 {
			docs = fmt.Sprintf(" (%d docs)", st.Docs)
		}
		return fmt.Sprintf("indexed %s: %d files%s in %d ms", root, st.Changed, docs, st.Took.Milliseconds()), nil
	}
	return "", fmt.Errorf("unknown action %q (use callers, def, impact or index)", args.Action)
}
