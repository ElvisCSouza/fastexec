// gitdirty.go: quais dos arquivos que uma edição vai tocar o git já considerava sujos ANTES dela.
//
// Quem aplica um rename em vários arquivos precisa separar três origens para a mesma linha de status: o que o
// FastExec acabou de gravar (conta da sessão, tools), o que o rename em si gravou, e o que já estava sujo por
// fora — alteração do usuário, de outro agente, de um build. As duas primeiras são esperadas; a terceira é a
// única que o modelo precisa saber, porque ele não a causou e pode acabar sobrescrevendo-a.
//
// A consulta é limitada aos arquivos pedidos (o rename já sabe quais são): um `status` do repositório inteiro
// custaria mais do que a resposta vale, e este caminho roda no meio de uma edição.
package graph

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// GitDirty devolve, para cada arquivo pedido que o git já via como sujo, true. A chave é o caminho relativo à
// raiz, barra normal — o mesmo formato do índice. Arquivo fora da raiz, ou repositório sem git, devolve mapa
// vazio: não saber não é motivo para atrapalhar a edição.
func GitDirty(root string, files []string) map[string]bool {
	out := map[string]bool{}
	if root == "" || len(files) == 0 {
		return out
	}
	seen := map[string]bool{}
	var rel []string
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(f))
		}
		r, err := filepath.Rel(root, p)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			continue
		}
		r = filepath.ToSlash(r)
		if seen[r] {
			continue
		}
		seen[r] = true
		rel = append(rel, r)
	}
	if len(rel) == 0 {
		return out
	}

	// -z: sem aspas nem escape nos caminhos, um registro por NUL. --no-renames mantém um rename como
	// "um removido + um novo", que é como os outros consumidores do graph leem o status (sync.go).
	args := append([]string{"-C", root, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--"}, rel...)
	cmd := exec.Command("git", args...)
	cmd.SysProcAttr = hiddenWindow()
	b, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, rec := range strings.Split(string(b), "\x00") {
		// "XY <path>": o status ocupa duas colunas, a terceira é o espaço separador.
		if len(rec) < 4 {
			continue
		}
		out[strings.TrimSpace(rec[3:])] = true
	}
	return out
}
