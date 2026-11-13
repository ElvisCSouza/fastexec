// test_hint.go: depois de editar um arquivo de código, o teste que o cobre é o que a LLM vai buscar para provar a
// correção. Achá-lo custa turnos (ls, grep, leitura); aqui o servidor já o entrega, com o filtro que roda só ele —
// a alternativa da LLM costuma ser a suíte inteira, com falhas antigas que ela gasta mais turnos investigando (CS6).
// Só aponta arquivos (convenção do nome); não afirma que o teste cobre o comportamento nem que passa.
package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"homelab/fastexec/internal/graph"
)

const (
	relatedTestsMax   = 3
	relatedTestsVisit = 30000 // arquivos visitados no máximo: pasta enorme não atrasa a resposta da edição
)

// relatedTestsLine devolve a linha "tests for this file: ..." na primeira edição de um arquivo de código que não é
// teste e tem arquivo de teste de nome parecido; "" no resto.
func (a *autoEdit) relatedTestsLine() string {
	if a == nil || a.isNew || a.root == "" || a.lang == "" {
		return ""
	}
	rel, err := filepath.Rel(a.root, a.abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if isTestPath(rel) {
		return ""
	}
	ext := filepath.Ext(rel)
	base := strings.TrimSuffix(filepath.Base(rel), ext)
	if len(base) < 4 || !once("tests|"+a.abs) {
		return ""
	}
	tests := findRelatedTests(a.root, rel, []string{base})
	if len(tests) == 0 {
		tests = testsOfUsers(a.root, rel, base, ext)
	}
	if len(tests) == 0 {
		return ""
	}
	line := "tests for this file: " + strings.Join(tests, ", ")
	if strings.EqualFold(ext, ".cs") {
		cls := strings.TrimSuffix(filepath.Base(tests[0]), ".cs")
		return line + fmt.Sprintf(` — run only them: dotnet test --filter "FullyQualifiedName~%s" (not the whole suite)`, cls)
	}
	return line + " — run only these, not the whole suite"
}

// Convenções de teste que o graph.TestFile não cobre: C#/Java/Kotlin (FluentValidation.Tests/, LengthValidatorTests.cs)
// e pastas "x.test"/"x_spec". Maiúscula marcada de propósito: "latest/" e "Contest.cs" não são teste.
var (
	reTestDirLoose  = regexp.MustCompile(`(^|/)([^/]*[._-])?(?i:tests?|specs?)/|(^|/)[^/]*(Tests?|Specs?)/`)
	reTestFileLoose = regexp.MustCompile(`[^/]*(Tests?|Specs?)\.(cs|java|kt|scala|swift|php|rb|ts|tsx|js|jsx)$|[^/]*_(tests?|spec)\.(rb|js|ts|php)$`)
)

func isTestPath(rel string) bool {
	return graph.TestFile(rel) || reTestDirLoose.MatchString(rel) || reTestFileLoose.MatchString(rel)
}

// genericNames: nomes de arquivo que se repetem em todo projeto. Teste de nome parecido só conta na mesma pasta.
var genericNames = map[string]bool{
	"main": true, "index": true, "util": true, "utils": true, "common": true, "helpers": true, "types": true,
	"init": true, "app": true, "mod": true, "lib": true, "config": true, "program": true, "startup": true, "base": true,
}

// genericOnly: o teste só casou por nomes genéricos (nenhum nome específico do arquivo aparece no nome do teste).
func genericOnly(lowerBase string, wants []string) bool {
	for _, w := range wants {
		if strings.Contains(lowerBase, w) && !genericNames[w] {
			return false
		}
	}
	return true
}

// sameLanguage: o teste está na linguagem do arquivo editado (um .zig não é coberto por main_test.go). TS e JS contam como uma.
func sameLanguage(rel, testRel string) bool {
	fam := func(e string) string {
		e = strings.ToLower(e)
		switch e {
		case ".ts", ".tsx", ".js", ".jsx", ".mjs":
			return ".js"
		case ".kt", ".java", ".scala":
			return ".jvm"
		}
		return e
	}
	return fam(filepath.Ext(rel)) == fam(filepath.Ext(testRel))
}

var relatedTestsSkipDirs = map[string]bool{
	".git": true, "bin": true, "obj": true, "node_modules": true, ".vs": true, "packages": true, "target": true,
}

// findRelatedTests anda pela pasta e junta os arquivos de teste cujo nome contém algum dos nomes pedidos.
func findRelatedTests(root, rel string, bases []string) []string {
	wants := make([]string, 0, len(bases))
	for _, b := range bases {
		if b = strings.ToLower(b); len(b) >= 4 {
			wants = append(wants, b)
		}
	}
	if len(wants) == 0 {
		return nil
	}
	var out []string
	visited := 0
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
		if visited++; visited > relatedTestsVisit || len(out) >= relatedTestsMax {
			return fs.SkipAll
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		r = filepath.ToSlash(r)
		if r == rel || !isTestPath(r) || !sameLanguage(rel, r) {
			return nil
		}
		lower := strings.ToLower(filepath.Base(r))
		if !nameHasAny(lower, wants) {
			return nil
		}
		// "main", "index", "util"...: o nome não diz de quem é o teste; só vale na mesma pasta (main_test.go do pacote ao lado).
		if genericOnly(lower, wants) && path.Dir(r) != path.Dir(rel) {
			return nil
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, r)
		}
		return nil
	})
	return out
}
