// member_plan.go: o grep por "Tipo.Membro" é quase sempre o começo de um rename ou de um ajuste de todos os usos. O
// hint genérico ("edit_file na declaração com symbol=") era ignorado em metade das execuções: o modelo precisa do
// arquivo da declaração para fazer a chamada, e achá-lo custa turnos (ls, outline, leitura). Aqui o servidor já o
// resolve — pelo qualificador, entre os homônimos — e entrega a chamada pronta.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/graph"
)

var reQualMember = regexp.MustCompile(`^([A-Z]\w*)\.([A-Z]\w*)\(?$`)

// firstQualifiedMember acha "Tipo.Membro" nas palavras da linha de busca (com /c:, aspas e barra de escape do ponto).
func firstQualifiedMember(label string) (qual, member string, ok bool) {
	for _, tk := range shellWords(label) {
		tk = strings.Trim(tk, `"'`)
		tk = strings.TrimPrefix(strings.TrimPrefix(tk, "/c:"), "/C:")
		tk = strings.Trim(strings.ReplaceAll(tk, `\.`, "."), `"'`)
		if strings.ContainsAny(tk, `/\`) {
			continue // caminho, não nome
		}
		if m := reQualMember.FindStringSubmatch(tk); m != nil {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// searchDirFor é a pasta onde a busca rodou: o cwd, o "cd X &&" do começo ou um caminho absoluto citado na linha.
func searchDirFor(cwd, label string) string {
	cand := []string{}
	if _, dir, ok := splitLeadingCd(label); ok {
		d := windowsPathForNativeRead(dir)
		if !filepath.IsAbs(d) && cwd != "" {
			d = filepath.Join(cwd, d)
		}
		cand = append(cand, d)
	}
	if cwd != "" {
		cand = append(cand, cwd)
	}
	for _, tk := range shellWords(label) {
		tk = windowsPathForNativeRead(strings.Trim(tk, `"'`))
		if filepath.IsAbs(tk) {
			cand = append(cand, tk)
		}
	}
	for _, d := range cand {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// memberRenamePlan devolve o hint com a chamada pronta, ou "" quando a declaração não se resolve sem ambiguidade.
func memberRenamePlan(cwd, label string) string {
	qual, member, ok := firstQualifiedMember(label)
	if !ok {
		return ""
	}
	dir := searchDirFor(cwd, label)
	if dir == "" {
		return ""
	}
	root, ok := graph.ResolveRepoRoot(dir)
	if !ok {
		return ""
	}
	if _, err := graph.Sync(root, graph.SyncBudget); err != nil {
		return ""
	}
	defs, err := graph.Definitions(root, member)
	if err != nil || len(defs) == 0 {
		return ""
	}
	// O tipo dono é achado pelo nome do arquivo (convenção de C#, Java e Go: Tipo.ext) — o único jeito barato de
	// separar o SelfLog.WriteLine do NullTextWriter.WriteLine sem perguntar ao compilador.
	var hit []graph.Def
	for _, d := range defs {
		base := strings.TrimSuffix(filepath.Base(d.File), filepath.Ext(d.File))
		if strings.EqualFold(base, qual) {
			hit = append(hit, d)
		}
	}
	if len(hit) != 1 {
		return ""
	}
	abs := filepath.Join(root, filepath.FromSlash(hit[0].File))
	return fmt.Sprintf("[FastExec FastHint] %s.%s is declared in %s:%d. To rename it and EVERY use in ONE call (compiler-exact, also updates text files that hold its signature, no sed, no per-file edits): edit_file path='%s' symbol='%s' replacement='<NewName>'.",
		qual, member, hit[0].File, hit[0].Start, filepath.ToSlash(abs), member)
}
