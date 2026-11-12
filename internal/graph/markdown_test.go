package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// md troca '§' por crase: string crua de Go não aceita crase e os fixtures de Markdown são cheios delas.
func md(s string) string { return strings.ReplaceAll(s, "§", "`") }

// docFixture monta um repositório com código Go indexável e um corpus de Markdown cobrindo seções aninhadas,
// front matter, link relativo com âncora, wikilink, crases (símbolo existente, símbolo inexistente, caminho de
// arquivo) e bloco de código cercado.
func docFixture(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.com/demo\n",
		"store/store.go": `package store

type Entry struct{ Name string }

func NewEntry(n string) Entry {
	return Entry{Name: n}
}
`,
		"docs/README.md": md(`---
title: Demo
tags: [doc]
---

# Demo

Introdução do projeto.

## Instalação

Use §store.NewEntry()§ para criar um registro. Veja a [API](api.md#api) e as [[Notas]].

§§§go
// exemplo: store.NewEntry("x") é código, não referência
cat docs/api.md
§§§

### Detalhe

O arquivo §store/store.go§ tem a implementação. §NaoExisteEmLugarNenhum§ não existe.

## Referências

[sumiu](naoexiste.md)
[âncora errada](api.md#nao-existe)
`),
		"docs/api.md": `# API

Referência da API.

## NewEntry

Cria um registro.
`,
		"_docs/notas.md": `# Notas

Rascunhos.
`,
		// ruído e arquivo grande: nenhum dos dois entra no grafo de documentação
		"node_modules/pkg/README.md": "# Dependência\n",
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestParseMarkdownSecoes cobre seções aninhadas, front matter e o limite em que um MD sem cabeçalho vira um nó só.
func TestParseMarkdownSecoes(t *testing.T) {
	src := md(`---
title: Demo
---

# Demo

Intro.

## Instalação

### Detalhe

Fim.

## Referências

Fim.
`)
	secs, _ := ParseMarkdown("docs/README.md", []byte(src))
	var got []string
	for _, s := range secs {
		got = append(got, s.Name)
	}
	want := []string{
		"docs/README.md > Demo",
		"docs/README.md > Demo > Instalação",
		"docs/README.md > Demo > Instalação > Detalhe",
		"docs/README.md > Demo > Referências",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("seções:\n%s\nquer:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// front matter YAML não vira símbolo nem seção
	for _, s := range secs {
		if strings.Contains(s.Name, "title") {
			t.Errorf("front matter virou seção: %q", s.Name)
		}
	}
	// linha do cabeçalho e fim real (a última linha com conteúdo antes do próximo cabeçalho)
	if secs[2].Line != 11 || secs[2].EndLine != 13 {
		t.Errorf("Detalhe: linha=%d fim=%d, quer 11/13", secs[2].Line, secs[2].EndLine)
	}

	// MD sem cabeçalho: um nó só, com o caminho como nome
	secs, _ = ParseMarkdown("docs/plain.md", []byte("Só texto.\n"))
	if len(secs) != 1 || secs[0].Name != "docs/plain.md" {
		t.Errorf("MD sem cabeçalho: %+v", secs)
	}
}

// TestParseMarkdownRefs cobre link relativo, wikilink e crases dentro e fora de bloco cercado. O parser devolve os
// candidatos crus (quem decide se a crase casa com algo é a resolução, com o índice na mão).
func TestParseMarkdownRefs(t *testing.T) {
	src := md(`# T

` + "`store/store.go`" + ` ` + "`NaoExiste`" + `

` + "```go" + `
` + "`NewEntry`" + ` fica de fora: aqui identificador é exemplo
cat docs/api.md
` + "```" + `

[a](api.md#api) [b](https://x.test/y.md) [[Notas]]
`)
	_, refs := ParseMarkdown("docs/x.md", []byte(src))
	var links, wiki, mentions []string
	for _, r := range refs {
		switch r.Kind {
		case RelDocLink:
			links = append(links, r.Target)
		case RelDocWiki:
			wiki = append(wiki, r.Target)
		case RelDocMention:
			mentions = append(mentions, r.Target)
		}
	}
	if strings.Join(links, ",") != "api.md#api,https://x.test/y.md" {
		t.Errorf("links: %v", links)
	}
	if strings.Join(wiki, ",") != "Notas" {
		t.Errorf("wikilinks: %v", wiki)
	}
	// fora do bloco cercado a crase vira candidato; dentro dele só caminho (`NewEntry` ali é exemplo de código)
	want := "store/store.go,NaoExiste,docs/api.md"
	if strings.Join(mentions, ",") != want {
		t.Errorf("menções: %v\nquer: %s", mentions, want)
	}
}

// TestGrafoDocumentacao cobre indexação (Docs), def/callers/impact com a linha "docs:" e a resolução exata das
// menções (símbolo existente e arquivo existente viram aresta; os demais não).
func TestGrafoDocumentacao(t *testing.T) {
	root := docFixture(t)
	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Docs != 3 {
		t.Errorf("Stats.Docs = %d, quer 3 (README, api, notas; node_modules fica de fora)", st.Docs)
	}
	if _, ok := indexedFiles(t, root)["node_modules/pkg/README.md"]; ok {
		t.Error("Markdown de node_modules entrou no índice")
	}

	// def: onde o símbolo é declarado + quais MDs o citam (âncora = seção que contém a menção)
	got, err := Definition(root, "store.NewEntry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "docs: docs/README.md#instalacao") {
		t.Errorf("def sem a linha docs:\n%s", got)
	}

	// callers: a menção em crase NÃO conta como chamada; a linha docs: vem antes do rodapé [index]
	got, err = Callers(root, "NewEntry", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "(no callers of 'NewEntry'") {
		t.Errorf("menção de doc virou chamada:\n%s", got)
	}
	if !strings.Contains(got, "docs: docs/README.md#instalacao") {
		t.Errorf("callers sem a linha docs:\n%s", got)
	}

	// impact de arquivo: quem cita o arquivo (link e menção de caminho, deduplicados por seção)
	got, err = Impact(root, "store/store.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "docs: docs/README.md#detalhe") {
		t.Errorf("impact de arquivo sem a linha docs:\n%s", got)
	}
	// impact de um Markdown: quem o cita
	got, err = Impact(root, "docs/api.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "docs: docs/README.md#instalacao") {
		t.Errorf("impact de MD sem a linha docs:\n%s", got)
	}
}

// TestOutlineDocReferenciasQuebradas cobre a detecção de quebra no outline: arquivo que não existe e âncora que não
// corresponde a nenhuma seção do alvo. Menção em crase nunca é quebra (o símbolo pode ter sido removido depois).
func TestOutlineDocReferenciasQuebradas(t *testing.T) {
	root := docFixture(t)
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "docs", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	decls, broken := DocOutline(root, "docs/README.md", src)
	if len(decls) != 4 {
		t.Errorf("seções do outline: %d, quer 4", len(decls))
	}
	want := "docs: 2 broken refs: docs/README.md:25 -> naoexiste.md (not found); docs/README.md:26 -> api.md#nao-existe (not found)"
	if broken != want {
		t.Errorf("quebras:\n%s\nquer:\n%s", broken, want)
	}

	// o MD que só tem referência boa não ganha linha nenhuma
	api, err := os.ReadFile(filepath.Join(root, "docs", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, b := DocOutline(root, "docs/api.md", api); b != "" {
		t.Errorf("api.md não tem quebra, veio: %s", b)
	}

	// fora de repositório (root vazio) não há índice para conferir: só as seções
	if _, b := DocOutline("", "docs/README.md", src); b != "" {
		t.Errorf("sem índice não deveria acusar quebra: %s", b)
	}
}

// TestDocsAusentesNaoMudamSaida garante o contrato: sem Markdown no repositório, def/callers/impact saem como
// sempre saíram (nenhuma linha "docs:").
func TestDocsAusentesNaoMudamSaida(t *testing.T) {
	root := fixture(t) // fixture de código, sem nenhum .md
	got, err := Definition(root, "store.NewEntry")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(got), "docs:") {
		t.Errorf("def ganhou linha docs: num repositório sem documentação:\n%s", got)
	}
	got, err = Callers(root, "helper", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(got), "docs:") {
		t.Errorf("callers ganhou linha docs: num repositório sem documentação:\n%s", got)
	}
	got, err = Impact(root, "store/store.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(got), "docs:") {
		t.Errorf("impact ganhou linha docs: num repositório sem documentação:\n%s", got)
	}
}

// TestDocGrandeForaDoIndice cobre o teto de tamanho por arquivo.
func TestDocGrandeForaDoIndice(t *testing.T) {
	root := docFixture(t)
	big := filepath.Join(root, "docs", "gerado.md")
	blob := "# Gerado\n\n" + strings.Repeat("linha de documentação gerada\n", 25000) // > 512 KB
	if err := os.WriteFile(big, []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Docs != 3 {
		t.Errorf("MD acima de 512 KB entrou na contagem: Docs = %d, quer 3", st.Docs)
	}
	if _, ok := indexedFiles(t, root)["docs/gerado.md"]; ok {
		t.Error("MD acima de 512 KB entrou no índice")
	}
}

// TestDocIncremental cobre a reindexação: apagar a referência apaga a aresta, então a linha "docs:" some.
func TestDocIncremental(t *testing.T) {
	root := docFixture(t)
	if _, err := Refresh(root); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(root, "docs", "README.md")
	src, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	// some com a menção ao símbolo e com a quebra de âncora, deixando só uma seção
	if err := os.WriteFile(readme, []byte("# Demo\n\nTexto sem referência nenhuma.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReindexFile(root, readme); err != nil {
		t.Fatal(err)
	}
	got, err := Definition(root, "store.NewEntry")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "docs:") {
		t.Errorf("aresta de doc sobreviveu à reindexação:\n%s", got)
	}
	if docTool := DocDecls("docs/README.md", src); len(docTool) != 4 {
		t.Errorf("DocDecls: %d seções, quer 4", len(docTool))
	}
}

// indexedFiles devolve os arquivos rastreados pelo grafo (o que o índice realmente guardou).
func indexedFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	d, err := store()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.Query(`SELECT file_path FROM code_files WHERE repo_root = ?`, repoKey(root))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			t.Fatal(err)
		}
		out[f] = true
	}
	return out
}
