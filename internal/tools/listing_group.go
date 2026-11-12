package tools

// listing_group.go: listagem recursiva de arquivos é o outro jeito caro de a LLM descobrir onde está o código
// ('dir /s /b src\Serilog\*.cs' devolveu 8 KB numa tacada; 'ls -R', 'find . -name "*.ext"' idem nas 40 execuções
// do benchmark). Nada é omitido aqui: o texto original é todo reaproveitado, só reagrupado por diretório
// ('src\Serilog\Core\ (12): A.cs B.cs ...'), o que troca N linhas de caminho repetido por uma linha por pasta. Um
// rodapé diz o que aconteceu e como pedir as linhas originais de volta ('--raw').
//
// Só age no que é inequivocamente listagem recursiva de arquivos: 'dir' com '/s' E '/b', 'ls' com '-R', 'find' com
// '-name' (mesmo molde que o grafo já lê, para as duas leituras nunca discordarem). Antes de reagrupar, TODA linha
// da saída precisa parecer caminho de arquivo — qualquer linha estranha ('total 42', colunas do 'dir /s' sem '/b',
// cabeçalho de erro) faz o texto passar intacto. Perder a lista completa por causa de uma reformatação errada seria
// pior que devolver os 8 KB. Desligar: FASTEXEC_NO_LISTING_GROUP=1.

import (
	"fmt"
	"os"
	"strings"
)

const (
	// listingGroupMinLines: abaixo disso não há o que economizar — o texto original já cabe no contexto.
	listingGroupMinLines = 80
	// listingGroupWidth: largura da linha de nomes antes da quebra (a continuação entra indentada).
	listingGroupWidth = 120
	listingGroupNote  = "[FastExec] listing grouped by directory; --raw returns the original lines."
)

func listingGroupOff() bool { return os.Getenv("FASTEXEC_NO_LISTING_GROUP") != "" }

// listingKind: como as linhas da saída viram grupos.
type listingKind int

const (
	listingNone listingKind = iota
	// listingPathLines: cada linha é um caminho ('dir /s /b', 'find . -name ...'); o diretório sai do próprio caminho.
	listingPathLines
	// listingLsRecursive: 'ls -R' imprime um cabeçalho de pasta ('.:', './src:') e, abaixo, só os nomes.
	listingLsRecursive
)

// parseListingKind lê o comando e diz se ele é uma listagem recursiva reconhecida. Comando com pipe, redirecionamento
// ou vários estágios não entra: aí a saída não é só a listagem.
func parseListingKind(cmd string) listingKind {
	segs := shellSegments(cmd)
	if len(segs) != 1 {
		return listingNone
	}
	w := shellWords(segs[0])
	if len(w) > 1 {
		// 'cmd /c dir /s /b ...': o embrulho do cmd não muda o que o dir imprime.
		if baseProg(w[0]) == "cmd" {
			if strings.EqualFold(w[1], "/c") || strings.EqualFold(w[1], "/C") {
				w = w[2:]
			} else {
				return listingNone
			}
		}
	}
	if len(w) == 0 {
		return listingNone
	}
	switch baseProg(w[0]) {
	case "dir":
		// Sem '/b' a saída vem em colunas (data, tamanho, nome) e o reagrupamento não se aplica; sem '/s' não há
		// recursão e a lista já é curta.
		if dirHasFlag(w[1:], "s") && dirHasFlag(w[1:], "b") {
			return listingPathLines
		}
	case "ls":
		if lsHasRecursive(w[1:]) {
			return listingLsRecursive
		}
	case "find":
		if parseFindListing(w) {
			return listingPathLines
		}
	}
	return listingNone
}

// parseFindListing: 'find <caminho> -name <glob>' (ou -iname) é listagem de caminhos. O reconhecedor é mais largo que
// o do grafo de propósito: aqui não há resposta a substituir, só reformatação — mas ação que muda o disco ('-delete',
// '-exec') fica de fora, porque aí a saída não é a lista dos arquivos encontrados.
func parseFindListing(w []string) bool {
	if len(w) == 0 || baseProg(w[0]) != "find" {
		return false
	}
	named := false
	for _, t := range w[1:] {
		lt := strings.ToLower(t)
		switch lt {
		case "-name", "-iname":
			named = true
		case "-exec", "-execdir", "-ok", "-delete", "-okdir":
			return false
		}
	}
	return named
}

// dirHasFlag: '/s' e '/b' do cmd.exe, aceitos colados ('/sb') ou separados.
func dirHasFlag(args []string, flag string) bool {
	for _, a := range args {
		if len(a) < 2 || a[0] != '/' {
			continue
		}
		if strings.Contains(strings.ToLower(a[1:]), flag) {
			return true
		}
	}
	return false
}

// lsHasRecursive: '-R', '--recursive' e o '-R' no meio de um agrupamento ('-lR', '-laR').
func lsHasRecursive(args []string) bool {
	for _, a := range args {
		if strings.EqualFold(a, "--recursive") {
			return true
		}
		if len(a) > 1 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "R") {
			return true
		}
	}
	return false
}

// listingGroup é um diretório e os nomes que saíram sob ele, na ordem em que apareceram.
type listingGroup struct {
	dir   string
	names []string
}

// regroupListing reagrupa a saída quando ela é uma listagem recursiva reconhecida e grande. ok=false: devolva a saída
// como veio. 'note' é o rodapé que ensina o caminho de volta ('--raw').
func regroupListing(label, stdout string) (text, note string, ok bool) {
	if listingGroupOff() {
		return "", "", false
	}
	kind := parseListingKind(label)
	if kind == listingNone {
		return "", "", false
	}
	body := strings.TrimRight(stdout, "\r\n")
	if body == "" {
		return "", "", false
	}
	groups, order, total, ok := collectListing(kind, strings.Split(body, "\n"))
	if !ok || total < listingGroupMinLines {
		return "", "", false
	}
	out := renderListing(groups, order)
	if out == "" {
		return "", "", false
	}
	return out, listingGroupNote, true
}

// collectListing monta os grupos e devolve também o total de nomes vistos (o critério de tamanho é o número de
// arquivos, não o de linhas: no 'ls -R' os cabeçalhos também ocupam linha).
func collectListing(kind listingKind, lines []string) (map[string][]string, []string, int, bool) {
	groups := map[string][]string{}
	var order []string
	total := 0
	current := ""
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Linha com espaços duplicados, tabulação ou espaço nas pontas é saída em colunas (ou aviso do shell): não é
		// listagem de caminho puro, e reagrupar isso destruiria informação.
		if line != strings.TrimSpace(line) || strings.Contains(line, "\t") || strings.Contains(line, "  ") {
			return nil, nil, 0, false
		}
		switch kind {
		case listingPathLines:
			dir, name := splitDirName(line)
			if dir == "" {
				if len(order) > 0 {
					// caminho solto sem pasta depois de outros com pasta (mistura de formatos): não mexe
					return nil, nil, 0, false
				}
				dir = "."
			}
			if _, seen := groups[dir]; !seen {
				order = append(order, dir)
			}
			groups[dir] = append(groups[dir], name)
			total++
		case listingLsRecursive:
			if dir, isHeader := lsHeaderDir(line); isHeader {
				current = dir
				continue
			}
			if current == "" {
				return nil, nil, 0, false
			}
			if strings.ContainsAny(line, `/\`) {
				// '-R' junto de uma flag que imprime caminho: formato diferente do esperado, não mexe
				return nil, nil, 0, false
			}
			if _, seen := groups[current]; !seen {
				order = append(order, current)
			}
			groups[current] = append(groups[current], line)
			total++
		}
	}
	if total == 0 {
		return nil, nil, 0, false
	}
	return groups, order, total, true
}

// splitDirName separa um caminho impresso na pasta e no nome, preservando o separador como ele saiu ('\' no Windows,
// '/' no Unix): o rótulo do grupo tem de parecer o caminho que a LLM mandou listar.
func splitDirName(p string) (dir, name string) {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "", p
	}
	return p[:i+1], p[i+1:]
}

// lsHeaderDir: '.:' ou './src:' abrem um bloco do 'ls -R'. 'C:' (unidade do Windows) não é cabeçalho.
func lsHeaderDir(line string) (string, bool) {
	if !strings.HasSuffix(line, ":") {
		return "", false
	}
	dir := strings.TrimSuffix(line, ":")
	if dir == "" || (len(dir) == 2 && dir[1] == ':') {
		return "", false
	}
	return dir, true
}

// renderListing escreve uma linha por diretório — 'src\Serilog\Core\ (12): A.cs B.cs ...' — quebrando os nomes em
// listingGroupWidth com continuação indentada. Todo nome aparece: nenhum é cortado nem resumido.
func renderListing(groups map[string][]string, order []string) string {
	var b strings.Builder
	for _, dir := range order {
		names := groups[dir]
		head := fmt.Sprintf("%s (%d):", dirLabel(dir), len(names))
		line := head
		for _, n := range names {
			if len(line)+1+len(n) > listingGroupWidth && line != head {
				b.WriteString(line + "\n")
				line = "  " + n
				continue
			}
			line += " " + n
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// dirLabel: o rótulo do grupo com o separador no fim, para o diretório não virar um nome qualquer.
func dirLabel(dir string) string {
	if strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, `\`) {
		return dir
	}
	if strings.Contains(dir, `\`) && !strings.Contains(dir, "/") {
		return dir + `\`
	}
	return dir + "/"
}
