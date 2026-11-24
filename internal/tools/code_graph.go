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
	// Repositório git primeiro (comportamento de sempre). Fora dele, a pasta pedida é indexada como avulsa quando
	// couber nos limites: antes a chamada morria aqui e o modelo perdia o turno (M2/M3 do ciclo 4).
	root, ok := graph.ResolveRepoRoot(expandHome(args.RepoPath))
	standalone := false
	if !ok {
		if root, ok = graph.ResolveStandaloneRoot(expandHome(args.RepoPath)); !ok {
			return "", graph.ErrNotInRepo(args.RepoPath)
		}
		standalone = true
	}
	out, err := runCodeGraph(root, args)
	if err != nil {
		if graph.IsNotInRepo(err) { // pasta avulsa grande demais: mesma recusa, com o caminho que o agente pediu
			return "", graph.ErrNotInRepo(args.RepoPath)
		}
		return "", err
	}
	if standalone {
		out = fmt.Sprintf("[graph] not a git repository: indexed the folder as standalone (%d files); "+
			"results reflect the files on disk now.\n%s", graph.TrackedFiles(root), out)
	}
	return out, nil
}

func runCodeGraph(root string, args CodeGraphArgs) (string, error) {
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
		g := newGraphCall(root, "callers", args.Symbol, args.Depth)
		out, err := courseWithGraph(root, args.Symbol,
			func() (string, error) { return lspCallers(root, args.Symbol, args.Max, args.Depth) },
			func() (string, error) { return graph.Callers(root, args.Symbol, args.Max, args.Depth) })
		return withBasis(withImplementersLine(groupCallerSites(out)), g), err
	case "def", "definition":
		if err := need(args.Symbol, "symbol"); err != nil {
			return "", err
		}
		g := newGraphCall(root, "def", args.Symbol, 0)
		out, err := courseWithGraph(root, args.Symbol,
			func() (string, error) { return lspDefinition(root, args.Symbol) },
			func() (string, error) { return graph.Definition(root, args.Symbol) })
		return withBasis(out, g), err
	case "impact":
		t := args.Target
		if t == "" {
			t = args.Symbol
		}
		if err := need(t, "target"); err != nil {
			return "", err
		}
		g := newGraphCall(root, "impact", t, 0)
		out, err := courseWithGraph(root, t,
			func() (string, error) { return lspImpact(root, t, args.Max) },
			func() (string, error) { return graph.Impact(root, t, args.Max) })
		return withBasis(out, g), err
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
