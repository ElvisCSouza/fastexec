package tools

// lsp_warm.go: D6, o servidor de linguagem pré-aquecido. A primeira operação do FastExec num repositório com
// linguagem conhecida (read_file, code_outline, code_graph, edit_file, write_file) sobe o servidor em segundo plano.
// Quando a LLM faz a primeira edição, a semântica já sai na hora em vez de "tipos pendentes". Custo: RAM e CPU de
// um servidor ocioso (o gerente o recolhe sozinho) — troca que o dono do FastExec escolheu: token vale mais.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"homelab/fastexec/internal/lsp"
)

const (
	warmEvery      = 20 * time.Minute // não reabre o mesmo (repositório, linguagem) antes disso
	warmWalkDepth  = 4
	warmWalkBudget = 3000 // entradas olhadas à procura de um arquivo da linguagem
	warmTimeout    = 120 * time.Second
)

var warmTools = map[string]bool{
	"read_file": true, "write_file": true, "edit_file": true, "code_outline": true, "code_graph": true,
}

var (
	warmMu   sync.Mutex
	warmSeen = map[string]time.Time{}
)

// warmLanguageServers sobe, uma vez por (raiz, linguagem), o servidor das linguagens que o caminho da chamada
// mostra. hint é o arquivo ou a pasta que a tool recebeu. Nunca bloqueia e nunca falha para quem chamou.
func warmLanguageServers(tool, root, hint string) {
	if !warmTools[tool] || lsp.Disabled() || hint == "" {
		return
	}
	files := map[string]string{} // id da linguagem → um arquivo dela dentro do projeto
	if st, err := os.Stat(hint); err == nil && !st.IsDir() {
		if l, ok := lsp.LanguageForPath(hint); ok && !l.Optional {
			files[l.ID] = hint
		}
	} else {
		findLanguageFiles(root, files)
	}
	for id, file := range files {
		key := root + "|" + id
		warmMu.Lock()
		last, seen := warmSeen[key]
		if seen && time.Since(last) < warmEvery {
			warmMu.Unlock()
			continue
		}
		warmSeen[key] = time.Now()
		warmMu.Unlock()
		if id == "csharp" {
			go lsp.RoslynPrepare() // o helper de sintaxe do C# é compilado uma vez, offline
		}
		go func(file string) {
			ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
			defer cancel()
			c, release, err := lsp.Manager().AcquireFile(ctx, file)
			if err != nil {
				return
			}
			_ = c.WaitSettled(ctx, file, warmTimeout/2)
			release()
		}(file)
	}
}

// findLanguageFiles procura, por baixo de root, um arquivo de cada linguagem com servidor (não opcional).
func findLanguageFiles(root string, out map[string]string) {
	skip := map[string]bool{".git": true, "node_modules": true, "bin": true, "obj": true, "vendor": true,
		"target": true, ".venv": true, "dist": true, "build": true}
	seen := 0
	base := strings.Count(filepath.ToSlash(root), "/")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		seen++
		if seen > warmWalkBudget {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if skip[d.Name()] || strings.Count(filepath.ToSlash(p), "/")-base > warmWalkDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if l, ok := lsp.LanguageForPath(p); ok && !l.Optional {
			if _, have := out[l.ID]; !have {
				out[l.ID] = p
			}
		}
		return nil
	})
}
