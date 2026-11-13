// test_hint_users.go: segundo salto do rodapé de testes. Quando nenhum teste tem o nome do arquivo editado, o teste que
// o cobre costuma ter o nome de quem o USA (LevelOutputFormat.cs é exercitado por MessageTemplateTextFormatterTests.cs,
// porque MessageTemplateTextFormatter é quem o chama). Primeiro o índice do grafo (sem criar nem atualizar nada); sem
// ele, uma varredura limitada do texto dos arquivos da mesma linguagem. Só aponta arquivos, como o salto direto.
package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/graph"
)

const (
	usersMaxFiles    = 20
	usersScanFiles   = 3000   // arquivos lidos no máximo na varredura de texto
	usersScanMaxSize = 256000 // bytes: arquivo maior que isso (gerado, minificado) não entra
	usersMaxNames    = 4
)

func nameHasAny(lowerBase string, wants []string) bool {
	for _, w := range wants {
		if strings.Contains(lowerBase, w) {
			return true
		}
	}
	return false
}

// testsOfUsers devolve os testes de quem usa o símbolo `base` (o nome do arquivo editado, que em C#/Java/Kotlin/TS é o
// nome do tipo). Teste que cita o símbolo diretamente vale mais que teste de nome parecido com o de um usuário.
func testsOfUsers(root, rel, base, ext string) []string {
	var direct, names []string
	seen := map[string]bool{}
	for _, f := range usersOf(root, rel, base, ext) {
		if isTestPath(f) {
			if len(direct) < relatedTestsMax {
				direct = append(direct, f)
			}
			continue
		}
		n := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if len(n) >= 4 && !seen[n] && len(names) < usersMaxNames {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(direct) > 0 {
		return direct
	}
	return findRelatedTests(root, rel, names)
}

// usersOf lista os arquivos (relativos, com barras) que citam o símbolo, sem o próprio arquivo editado.
func usersOf(root, rel, base, ext string) []string {
	if us, ok := graph.Uses(root, []string{base}, rel); ok {
		var out []string
		seen := map[string]bool{}
		for _, s := range us[base].Sites {
			if !seen[s.File] && len(out) < usersMaxFiles {
				seen[s.File] = true
				out = append(out, s.File)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return scanUsers(root, rel, base, ext)
}

func scanUsers(root, rel, base, ext string) []string {
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(base) + `\b`)
	if err != nil {
		return nil
	}
	var out []string
	read := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && relatedTestsSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= usersMaxFiles || read >= usersScanFiles {
			return fs.SkipAll
		}
		if !strings.EqualFold(filepath.Ext(p), ext) {
			return nil
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		r = filepath.ToSlash(r)
		if r == rel {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > usersScanMaxSize {
			return nil
		}
		read++
		if b, err := os.ReadFile(p); err == nil && re.Match(b) {
			out = append(out, r)
		}
		return nil
	})
	return out
}
