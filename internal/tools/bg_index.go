package tools

// bg_index.go: o gancho que tira a primeira indexação do caminho da consulta. Toda chamada de tool que carrega um
// caminho local diz que alguém está trabalhando naquele repositório: é o sinal mais barato que existe para começar a
// indexar antes de o modelo perguntar qualquer coisa ao grafo. O disparo é de fundo, uma vez por repositório, e não
// devolve nada — quem espera (e avisa quando não dá tempo) é a consulta, em internal/graph.
//
// Aqui só se decide *se* e *qual* raiz: host remoto e caminho fora de repositório git não fazem nada, e um argumento
// estranho não vira erro (a tool de verdade vai reclamar dele com o modelo, no lugar certo).

import (
	"encoding/json"
	"os"

	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/transport"
)

// bgIndexTools são as tools cujo argumento aponta um diretório de trabalho. Ficam de fora as que não têm caminho
// local (sql, job, ping, handoff) e o write_file/edit_file sem path — nesses o unmarshal já devolve vazio.
var bgIndexTools = map[string]bool{
	"exec":         true,
	"read_file":    true,
	"write_file":   true,
	"edit_file":    true,
	"code_outline": true,
	"code_graph":   true,
}

// bgIndexArgs é o mínimo que interessa de qualquer uma dessas tools: o host, o caminho e o diretório de trabalho.
type bgIndexArgs struct {
	Host     string `json:"host"`
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	RepoPath string `json:"repo_path"`
}

// KickBackgroundIndex dispara a indexação de fundo do repositório que a chamada está usando (singleflight por raiz,
// quem decide é internal/graph). Silencioso de propósito: qualquer coisa que não seja caminho local dentro de um
// repositório git simplesmente não faz nada. É chamado a cada tool, então não pode bloquear nem custar quase nada.
func KickBackgroundIndex(tool string, rawArgs json.RawMessage) {
	if !bgIndexTools[tool] || graph.BgDisabled() || len(rawArgs) == 0 {
		return
	}
	var a bgIndexArgs
	if err := json.Unmarshal(rawArgs, &a); err != nil {
		return
	}
	if a.Host != "" && !transport.IsDefaultName(a.Host) {
		return // host remoto: o índice descreve o disco local, não o de lá
	}
	// cwd > path > repo_path: nenhuma tool dessas tem mais de um deles, e o exec não tem path.
	p := a.Cwd
	if p == "" {
		p = a.Path
	}
	if p == "" {
		p = a.RepoPath
	}
	if p == "" {
		if tool != "exec" {
			return
		}
		// exec sem cwd roda no diretório do processo: é ele o diretório de trabalho da chamada.
		if wd, err := os.Getwd(); err == nil {
			p = wd
		}
	}
	if p == "" {
		return
	}
	// Caminho com prefixo de host ("noteserver:/srv/x") vale só se o host for o local.
	if h, rest := SplitHostPath(p); h != "" {
		if !transport.IsDefaultName(h) {
			return
		}
		p = rest
	}
	if root, ok := graph.ResolveRepoRoot(expandHome(p)); ok {
		graph.KickBackground(root)
	}
}

// StopBackgroundIndex encerra as passadas de fundo (fechamento do daemon): o processo está indo embora e não vale
// começar trabalho novo.
func StopBackgroundIndex() { graph.StopBackground() }
