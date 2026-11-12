// exec_delete_impact.go: quando um exec apaga arquivo(s) de código, a saída diz quais símbolos daqueles arquivos
// continuam sendo usados em outros arquivos. O grafo é de ANTES do apagamento, e é justamente por isso que ele
// serve: as referências que sobraram nos outros arquivos ainda estão lá, e o modelo descobre na mesma resposta o
// que ficou quebrado. Sem isso ele roda grep atrás de cada símbolo do arquivo apagado.
//
// Só depois de um exec bem-sucedido, só em host local, só se o repositório já tinha índice, e só quando o comando
// de fato apaga (rm/del/erase/Remove-Item/git rm). Qualquer outra coisa não muda um byte da saída.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"homelab/fastexec/internal/codescan"
	"homelab/fastexec/internal/graph"
	"homelab/fastexec/internal/transport"
)

const (
	// deleteImpactBudget é o teto da linha, em caracteres (é um rodapé de uma linha só).
	deleteImpactBudget = 300
	// deleteImpactMaxSites é quantos "arquivo:linha" entram antes do "+K more".
	deleteImpactMaxSites = 5
	// deleteImpactTimeout é o mesmo orçamento do aviso de edição: passou disso, o aviso é omitido.
	deleteImpactTimeout = 150 * time.Millisecond
)

// execDeleteImpact monta o aviso de apagamento para o comando que acabou de rodar.
func execDeleteImpact(args ExecArgs) string {
	targets := deletionTargets(args)
	if len(targets) == 0 {
		return ""
	}
	// host remoto tem o repositório do outro lado; o índice daqui não diz nada sobre ele.
	if t, err := transport.ResolveHost(args.Host); err != nil || !t.IsLocal {
		return ""
	}
	start := time.Now()

	cwd := args.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var lines []string
	total := 0
	for _, p := range targets {
		if time.Since(start) > deleteImpactTimeout {
			break
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		root, ok := graph.ResolveRepoRoot(abs)
		if !ok {
			continue
		}
		du, ok := graph.DeclaredUses(root, relToRoot(root, abs))
		if !ok || len(du) == 0 {
			continue
		}
		if l := deleteLine(filepath.ToSlash(p), du); l != "" {
			if total+len(l)+1 > deleteImpactBudget && len(lines) > 0 {
				break // uma linha por arquivo, dentro do orçamento
			}
			lines = append(lines, l)
			total += len(l) + 1
		}
	}
	if time.Since(start) > deleteImpactTimeout {
		return ""
	}
	return strings.Join(lines, "\n")
}

// deletionTargets lê o comando e devolve os caminhos que ele apaga, na ordem em que aparecem. Só arquivos de
// linguagem com declarações (o resto não tem símbolo no grafo) e sem curinga (não dá para expandir sem rodar).
func deletionTargets(args ExecArgs) []string {
	var segs [][]string
	if len(args.Argv) > 0 {
		segs = append(segs, args.Argv)
	} else {
		for _, s := range shellSegments(args.Command) {
			segs = append(segs, shellWords(s))
		}
	}
	var out []string
	for _, w := range segs {
		i := 0
		for i < len(w) && isCommandPrefix(w[i]) {
			i++
		}
		if i >= len(w) {
			continue
		}
		verb := strings.ToLower(filepath.Base(w[i]))
		rest := w[i+1:]
		if verb == "git" || verb == "git.exe" {
			// 'git -C <dir> rm x.go': pula os flags (e o valor dos que o levam) até achar o subcomando
			j := 0
			for j < len(rest) && strings.HasPrefix(rest[j], "-") {
				if v := rest[j]; v == "-C" || v == "--git-dir" || v == "--work-tree" {
					j++
				}
				j++
			}
			if j >= len(rest) || strings.ToLower(rest[j]) != "rm" {
				continue
			}
			rest = rest[j+1:]
		} else if !isDeleteVerb(verb) {
			continue
		}
		for _, a := range rest {
			if isDeleteFlag(a) || strings.ContainsAny(a, "*?$") {
				continue
			}
			if codescan.LangFor(a) == nil {
				continue // json, md, imagem, diretório: sem declaração para avisar
			}
			out = append(out, a)
		}
	}
	return out
}

// isDeleteVerb cobre os nomes e apelidos usados nas duas plataformas (PowerShell aceita rm/del/erase).
func isDeleteVerb(v string) bool {
	switch v {
	case "rm", "rm.exe", "del", "del.exe", "erase", "remove-item", "rd", "rmdir":
		return true
	}
	return false
}

// isCommandPrefix pula o que vem antes do verbo sem ser o verbo: 'sudo', 'env', VAR=valor, 'command'.
func isCommandPrefix(w string) bool {
	switch w {
	case "sudo", "env", "command", "nohup", "time":
		return true
	}
	return strings.Contains(w, "=") && !strings.HasPrefix(w, "-")
}

// isDeleteFlag reconhece '-rf', '--force' e os flags de barra do cmd/PowerShell ('del /f'), distinguindo de um
// caminho absoluto Unix ('/home/x.go' tem barra no meio).
func isDeleteFlag(a string) bool {
	if strings.HasPrefix(a, "-") {
		return true
	}
	if strings.HasPrefix(a, "/") && len(a) == 2 && !strings.ContainsAny(a[1:], "./") {
		return true
	}
	return false
}

// deleteLine monta a linha de UM arquivo apagado: os símbolos que ele declarava e ainda são usados, os locais e o
// resumo. Símbolo ambíguo (declarado em mais de um lugar) ganha "(ambiguous)": o grafo casa por nome, e omitir a
// ressalva seria afirmar o que não se sabe.
func deleteLine(file string, du []graph.FileSymUses) string {
	var names []string
	files := map[string]bool{}
	var sites []graph.Site
	seen := map[string]bool{}
	for _, d := range du {
		n := d.Symbol
		if d.Uses.Ambiguous >= 2 {
			n += " (ambiguous)"
		}
		names = append(names, n)
		for _, s := range d.Uses.Sites {
			k := s.File + ":" + strconv.Itoa(s.Line)
			if seen[k] {
				continue
			}
			seen[k] = true
			sites = append(sites, s)
			files[s.File] = true
		}
	}
	if len(sites) == 0 {
		return ""
	}
	sort.SliceStable(sites, func(a, b int) bool {
		ta, tb := graph.IsTestPath(sites[a].File), graph.IsTestPath(sites[b].File)
		if ta != tb {
			return !ta
		}
		if sites[a].File != sites[b].File {
			return sites[a].File < sites[b].File
		}
		return sites[a].Line < sites[b].Line
	})
	head := fmt.Sprintf("[graph] %s deleted: %s still used in %d files:", file, strings.Join(names, ", "), len(files))
	tail := " By name, not type-checked."
	for shown := min(len(sites), deleteImpactMaxSites); shown >= 1; shown-- {
		line := head + " " + joinSites(sites[:shown]) + more(sites, shown) + "." + tail
		if len(line) <= deleteImpactBudget {
			return line
		}
	}
	line := fmt.Sprintf("%s %d site(s).", head, len(sites))
	if len(line) <= deleteImpactBudget {
		return line
	}
	return head + "."
}
