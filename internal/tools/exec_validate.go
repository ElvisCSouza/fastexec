package tools

// exec_validate.go: D9, o código gravado pelo exec (heredoc, `>`, Set-Content, tee, sed -i...) também é validado.
// O write_file/edit_file têm o caminho de edição; o exec não, e é por ele que um modelo escreve um arquivo inteiro com
// `cat > Foo.cs <<'EOF'`. Sem isto o erro só aparecia no build, um turno depois.
//
// O desenho evita varrer o disco: a linha de comando diz quais arquivos .go/.cs ela menciona, e a prova de que o
// exec os gravou é o mtime/tamanho de antes e depois (alguns stats, nada de `git status` nem de caminhada na árvore).
// Um arquivo que a linha só lê (cat, grep, dotnet build) não muda de mtime e não é tocado. Código gravado por um
// programa que a linha apenas executa (um gerador, `go generate`) não é mencionado e fica de fora — dito na doc.
//
// O que um arquivo gravado por exec recebe é o que um arquivo NOVO recebe: não há texto "antes" para separar o que a
// LLM acabou de escrever do que já estava lá, então todo erro conta como dela.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"homelab/fastexec/internal/lsp"
)

const (
	execWatchMaxFiles = 8
	execWatchMaxBytes = 512 << 10
)

// codeMention acha caminhos .go/.cs soltos na linha, com ou sem aspas.
var codeMention = regexp.MustCompile(`(?i)[A-Za-z0-9_@~$:./\\-]+\.(?:cs|go)\b`)

type fileStamp struct {
	exists bool
	size   int64
	mtime  int64
}

type execWatch struct {
	host   string
	stamps map[string]fileStamp
}

func stampOf(p string) fileStamp {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: st.Size(), mtime: st.ModTime().UnixNano()}
}

// execWatchBegin devolve nil quando não há o que vigiar: assistente desligado, host remoto ou nenhuma menção a
// arquivo de código. É chamado a cada exec, então o caminho sem menção custa uma regexp.
func execWatchBegin(host, label, cwd string) *execWatch {
	if lsp.Disabled() || label == "" {
		return nil
	}
	found := codeMention.FindAllString(label, -1)
	if len(found) == 0 {
		return nil
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	w := &execWatch{host: host, stamps: map[string]fileStamp{}}
	for _, m := range found {
		m = strings.Trim(m, `"'`)
		if strings.HasPrefix(m, "~") {
			m = expandHome(m)
		}
		p := m
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, filepath.FromSlash(p))
		}
		p = filepath.Clean(p)
		if _, dup := w.stamps[p]; dup || !impactLocal(host, p) {
			continue
		}
		if len(w.stamps) >= execWatchMaxFiles {
			break
		}
		w.stamps[p] = stampOf(p)
	}
	if len(w.stamps) == 0 {
		return nil
	}
	return w
}

// Finish valida cada arquivo que o exec gravou e devolve as linhas para o fim da resposta (vazio se nada mudou).
func (w *execWatch) Finish() string {
	if w == nil {
		return ""
	}
	paths := make([]string, 0, len(w.stamps))
	for p := range w.stamps {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		after := stampOf(p)
		if !after.exists || after == w.stamps[p] || after.size > execWatchMaxBytes {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		ae := autoEditBegin(w.host, p, "", string(b))
		if ae == nil {
			continue
		}
		note := strings.TrimPrefix(ae.Finish(), "\n")
		ae.Close()
		if note == "" {
			continue
		}
		name := ae.relOrBase()
		for _, line := range strings.Split(note, "\n") {
			// "✗" já leva arquivo:linha; os estados ("✓ compila", "· tipos pendentes") não.
			if strings.HasPrefix(line, "✓ ") || strings.HasPrefix(line, "· ") {
				line = name + ": " + line
			}
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}
