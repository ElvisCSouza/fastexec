package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cp1252 de "Informação básica: coração não é peça — €" (travessão 0x97, euro 0x80).
func cp1252Bytes(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80, r >= 0xA0 && r <= 0xFF:
			b = append(b, byte(r))
		case r == '—':
			b = append(b, 0x97)
		case r == '€':
			b = append(b, 0x80)
		default:
			panic("rune fora do teste: " + string(r))
		}
	}
	return b
}

func TestDecodeLegacyText(t *testing.T) {
	const want = "Informação básica: coração não é peça — €"
	got, enc := decodeLegacyText(string(cp1252Bytes(want)))
	if got != want || enc != encWindows1252 {
		t.Fatalf("cp1252: got %q (%s)", got, enc)
	}
	if g, e := decodeLegacyText("já é UTF-8: ação"); e != "" || g != "já é UTF-8: ação" {
		t.Fatalf("UTF-8 válido deve passar intacto: %q %q", g, e)
	}
	if g, e := decodeLegacyText("bin\x00\xff\xfe"); e != "" || g != "bin\x00\xff\xfe" {
		t.Fatalf("binário (NUL) nunca é decodificado: %q %q", g, e)
	}
	if g, e := decodeLegacyText(""); e != "" || g != "" {
		t.Fatalf("vazio")
	}
	// UTF-16LE com BOM, como o Windows PowerShell 5 grava
	u16 := "\xff\xfe" + "A\x00\xe7\x00\xe3\x00o\x00\r\x00\n\x00"
	if g, e := decodeLegacyText(u16); e != encUTF16LE || g != "Ação\r\n" {
		t.Fatalf("utf16le: %q %q", g, e)
	}
}

func TestEncodeLikeRoundTrip(t *testing.T) {
	for _, raw := range []string{string(cp1252Bytes("Informação — €")), "\x81\x8d\x8f\x90\x9d\xe7"} {
		text, enc := decodeLegacyText(raw)
		if enc != encWindows1252 {
			t.Fatalf("esperava Windows-1252 para %q, veio %q", raw, enc)
		}
		back, ok := encodeLike(text, enc)
		if !ok || back != raw {
			t.Fatalf("ida e volta: ok=%v back=%q raw=%q", ok, back, raw)
		}
	}
	if _, ok := encodeLike("中", encWindows1252); ok {
		t.Fatal("caractere fora do Windows-1252 deve ser recusado")
	}
}

func TestReadFileLegacyEncoding(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dados.txt")
	body := "Registro 1 sem acentuação\r\nInformação básica: coração não é peça\r\nAção concluída\r\n"
	if err := os.WriteFile(p, cp1252Bytes(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleReadFile(ReadFileArgs{Path: p, Pattern: "^Informa", Context: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Informação básica: coração não é peça") || !strings.Contains(out, "Windows-1252") {
		t.Fatalf("read_file deve decodificar e avisar: %q", out)
	}
	if strings.ContainsRune(out, '�') {
		t.Fatalf("não pode sobrar caractere de substituição: %q", out)
	}
	// busca com acento casa no texto decodificado
	out, err = HandleReadFile(ReadFileArgs{Path: p, Pattern: "coração", Context: 0})
	if err != nil || !strings.Contains(out, "coração") {
		t.Fatalf("pattern com acento: %v %q", err, out)
	}
}

func TestEditFileKeepsLegacyEncoding(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "legado.txt")
	body := "linha um\r\nInformação: coração\r\nAção concluída\r\n"
	if err := os.WriteFile(p, cp1252Bytes(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// alvo ASCII: o resto do arquivo tem que ficar byte a byte igual
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "linha um", Replacement: "linha 1"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(cp1252Bytes(strings.Replace(body, "linha um", "linha 1", 1))) {
		t.Fatalf("edição ASCII alterou outros bytes: %q", got)
	}
	// alvo com acento (o modelo escreve em UTF-8) tem que achar e regravar em Windows-1252
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "coração", Replacement: "coração — ok"}); err != nil {
		t.Fatalf("alvo acentuado: %v", err)
	}
	got, _ = os.ReadFile(p)
	if string(got) != string(cp1252Bytes(strings.Replace(strings.Replace(body, "linha um", "linha 1", 1), "coração", "coração — ok", 1))) {
		t.Fatalf("regravação fora do Windows-1252: %q", got)
	}
	// caractere que não existe no Windows-1252: recusa e não grava
	before, _ := os.ReadFile(p)
	if _, err := HandleEditFile(EditFileArgs{Path: p, Target: "linha 1", Replacement: "中文"}); err == nil {
		t.Fatal("deveria recusar caractere fora do Windows-1252")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("recusa não pode alterar o arquivo")
	}
}
