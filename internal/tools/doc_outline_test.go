package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodeOutlineMarkdown cobre code_outline de um Markdown: as seções (os nós do grafo de documentação) saem como
// "L<início>-L<fim>: <cabeçalho>", e o filtro por 'symbol' casa com o caminho de cabeçalhos.
func TestCodeOutlineMarkdown(t *testing.T) {
	src := "# Demo\n\nIntro.\n\n## Instalação\n\nPasso a passo.\n"
	got := outlineOf(t, "README.md", src, "")
	want := "L1-L3: # Demo\nL5-L7: ## Instalação"
	if got != want {
		t.Errorf("outline MD:\n%s\nquer:\n%s", got, want)
	}
	if got := outlineOf(t, "README.md", src, "Instalação"); got != "L5-L7: ## Instalação" {
		t.Errorf("outline MD por símbolo:\n%s", got)
	}
	if got := outlineOf(t, "README.md", src, "NadaDisso"); !strings.HasPrefix(got, "(no declaration matching") {
		t.Errorf("outline MD sem casar símbolo:\n%s", got)
	}
}

// TestCodeOutlineMarkdownGrande cobre o teto de 'max' (mesmo rodapé de guarda do código).
func TestCodeOutlineMarkdownGrande(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("## Seção ")
		b.WriteByte(byte('A' + i))
		b.WriteString("\n\ntexto.\n\n")
	}
	got := outlineOf(t, "README.md", b.String(), "")
	if !strings.Contains(got, "L1-L3: ## Seção A") {
		t.Errorf("primeira seção:\n%s", got)
	}
}

// TestMarkdownNaoENoiseNaListagem garante a decisão de escopo: .md não é uma linguagem do codescan, então o outline
// de uma PASTA continua listando só código (o Markdown entra pela porta do grafo, não pela varredura de código).
func TestMarkdownNaoENoiseNaListagem(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Demo\n\ntexto.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := outlineTree(dir, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "README.md") {
		t.Errorf("outline de pasta listou Markdown:\n%s", got)
	}
}

// TestCodeOutlineMdMensagemDeTipo cobre a mensagem de tipo não suportado, que agora anuncia .md/.markdown.
func TestCodeOutlineMdMensagemDeTipo(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notas.txt")
	if err := os.WriteFile(p, []byte("texto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := HandleCodeOutline(CodeOutlineArgs{Host: "local", Path: p})
	if err == nil {
		t.Fatal("esperava erro para .txt")
	}
	if !strings.Contains(err.Error(), ".md/.markdown") {
		t.Errorf("mensagem sem as extensões de documentação: %v", err)
	}
}
