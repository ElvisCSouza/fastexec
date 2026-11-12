package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile cria o arquivo de teste com o conteúdo exato (sem conversão de quebra de linha).
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write falhou: %v", err)
	}
	return p
}

func editLine(t *testing.T, name, content, target, replacement string) (string, string) {
	t.Helper()
	p := writeFile(t, name, content)
	out, err := HandleEditFile(EditFileArgs{Path: p, Target: target, Replacement: replacement})
	if err != nil {
		t.Fatalf("HandleEditFile falhou: %v", err)
	}
	b, _ := os.ReadFile(p)
	return out, string(b)
}

func TestEditFileProofCRLF(t *testing.T) {
	content := "l1\r\nl2\r\nl3\r\nl4\r\nMaxRetries = 3;\r\nl6\r\nl7\r\n"
	out, disk := editLine(t, "Config.cs", content, "MaxRetries = 3;", "MaxRetries = 5;")

	if !strings.HasPrefix(out, "[edit_file] 1 change(s) applied (15 -> 15 bytes) in ") {
		t.Errorf("primeira frase mudou: %q", out)
	}
	if !strings.Contains(out, "\nL5: MaxRetries = 5;\n") {
		t.Errorf("esperava a linha 5 citada sem \\r: %q", out)
	}
	if !strings.Contains(out, "line endings kept: CRLF") {
		t.Errorf("esperava CRLF na resposta: %q", out)
	}
	if disk != "l1\r\nl2\r\nl3\r\nl4\r\nMaxRetries = 5;\r\nl6\r\nl7\r\n" {
		t.Errorf("CRLF não preservado no disco: %q", disk)
	}
}

func TestEditFileProofLF(t *testing.T) {
	content := "l1\nl2\nl3\nl4\nl5\nl6\nl7\n"
	out, disk := editLine(t, "a.txt", content, "l5", "cinco")

	if !strings.Contains(out, "\nL5: cinco\n") {
		t.Errorf("esperava a linha 5 citada: %q", out)
	}
	if !strings.Contains(out, "line endings kept: LF") {
		t.Errorf("esperava LF na resposta: %q", out)
	}
	if disk != "l1\nl2\nl3\nl4\ncinco\nl6\nl7\n" {
		t.Errorf("conteúdo inesperado no disco: %q", disk)
	}
}

// Lote: as duas trocas são citadas, e a linha da segunda já conta a linha extra que a primeira inseriu.
func TestEditFileProofEdicaoEmLote(t *testing.T) {
	p := writeFile(t, "a.txt", "l1\nTARGET_A\nl3\nl4\nTARGET_B\nl6\n")
	out, err := HandleEditFile(EditFileArgs{Path: p, Edits: []EditOp{
		{Target: "TARGET_A", Replacement: "A1\nA2"},
		{Target: "TARGET_B", Replacement: "B"},
	}})
	if err != nil {
		t.Fatalf("HandleEditFile falhou: %v", err)
	}
	if !strings.Contains(out, "2 change(s) applied") {
		t.Errorf("contagem inesperada: %q", out)
	}
	if !strings.Contains(out, "\nL2: A1\n") {
		t.Errorf("esperava a linha 2 citada: %q", out)
	}
	if !strings.Contains(out, "\nL6: B\n") {
		t.Errorf("esperava a linha 6 (deslocada pela troca anterior): %q", out)
	}
	if b, _ := os.ReadFile(p); string(b) != "l1\nA1\nA2\nl3\nl4\nB\nl6\n" {
		t.Errorf("conteúdo inesperado: %q", b)
	}
}

func TestEditFileProofMaisDeCincoTrocas(t *testing.T) {
	// linhas longas: além de contar as trocas, prova que o orçamento da resposta é respeitado com citação cheia
	var sb strings.Builder
	var edits []EditOp
	for i := 1; i <= 7; i++ {
		tag := "T" + string(rune('0'+i))
		sb.WriteString(tag + strings.Repeat("x", 150) + "\n")
		edits = append(edits, EditOp{Target: tag, Replacement: "U" + string(rune('0'+i))})
	}
	p := writeFile(t, "a.txt", sb.String())
	out, err := HandleEditFile(EditFileArgs{Path: p, Edits: edits})
	if err != nil {
		t.Fatalf("HandleEditFile falhou: %v", err)
	}
	if !strings.Contains(out, "7 change(s) applied") {
		t.Errorf("contagem inesperada: %q", out)
	}
	for _, want := range []string{"\nL1: U1", "\nL5: U5", "\n(and 2 more)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("esperava %q em: %q", want, out)
		}
	}
	if strings.Contains(out, "\nL6: ") {
		t.Errorf("não deveria citar além da quinta linha: %q", out)
	}
	// o bloco extra (depois da primeira frase) cabe no orçamento da resposta
	proof := out[strings.Index(out, "\n"):]
	if len(proof) > editProofByteBudget {
		t.Errorf("bloco extra com %d caracteres (teto %d): %q", len(proof), editProofByteBudget, proof)
	}
}

// Linha longa é citada truncada, sem quebrar caractere multibyte.
func TestEditFileProofLinhaLonga(t *testing.T) {
	long := "MARK" + strings.Repeat("ç", 200)
	out, _ := editLine(t, "a.txt", "l1\n"+long+"\nl3\n", "MARK", "MARKED")

	line := out[strings.Index(out, "L2: "):]
	line = line[:strings.IndexByte(line, '\n')]
	if !strings.HasSuffix(line, "…") {
		t.Errorf("esperava citação truncada: %q", line)
	}
	if n := len([]rune(line)); n > editProofQuoteMax+len("L2: ")+1 {
		t.Errorf("citação com %d runas: %q", n, line)
	}
}

// BOM: o arquivo continua com BOM depois da edição e a resposta diz que a codificação foi preservada.
func TestEditFileProofBOM(t *testing.T) {
	content := utf8BOM + "l1\nl2\nl3\n"
	out, disk := editLine(t, "a.txt", content, "l2", "dois")

	if !strings.Contains(out, "\nL2: dois\n") {
		t.Errorf("esperava a linha 2 citada: %q", out)
	}
	if !strings.Contains(out, "encoding kept: UTF-8 BOM") {
		t.Errorf("esperava o aviso de BOM: %q", out)
	}
	if !strings.HasPrefix(disk, utf8BOM) || disk != utf8BOM+"l1\ndois\nl3\n" {
		t.Errorf("BOM ou conteúdo perdido: %q", disk)
	}
}

// Arquivo legado (Windows-1252) regravado na mesma codificação, com o aviso na resposta.
func TestEditFileProofEncodingLegada(t *testing.T) {
	content := "l1\nInforma\xe7\xe3o\nl3\n" // "Informação" em Windows-1252
	out, disk := editLine(t, "a.txt", content, "Informação", "Dados")

	if !strings.Contains(out, "\nL2: Dados\n") {
		t.Errorf("esperava a linha 2 citada: %q", out)
	}
	if !strings.Contains(out, "encoding kept: Windows-1252") {
		t.Errorf("esperava o aviso de codificação: %q", out)
	}
	if disk != "l1\nDados\nl3\n" {
		t.Errorf("conteúdo inesperado: %q", disk)
	}
}
