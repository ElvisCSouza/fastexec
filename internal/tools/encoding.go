package tools

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Codificações de arquivo de texto que o modelo não consegue ler como UTF-8. Legados brasileiros (Windows-1252 / Latin-1)
// e o UTF-16 que o Windows PowerShell 5 grava por padrão chegavam ao modelo como "Informa��o", e ele recorria a
// scripts (python que o host nem tem) só para decodificar: turnos e erros gastos por nada.
const (
	encWindows1252 = "Windows-1252"
	encUTF16LE     = "UTF-16LE"
	encUTF16BE     = "UTF-16BE"
)

// cp1252High mapeia 0x80..0x9F; 0xA0..0xFF coincidem com Latin-1 (U+00A0..U+00FF). Bytes indefinidos (0x81, 0x8D, 0x8F,
// 0x90, 0x9D) viram o controle C1 de mesmo valor, como fazem os decodificadores do WHATWG.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// decodeLegacyText devolve o texto em UTF-8 e o nome da codificação de origem. Sem conversão (UTF-8 válido, binário ou
// vazio) devolve o próprio dado e "". Binário (NUL fora de UTF-16 com BOM) nunca é "decodificado".
func decodeLegacyText(data string) (string, string) {
	n := len(data)
	if n >= 2 {
		switch {
		case data[0] == 0xFF && data[1] == 0xFE:
			return decodeUTF16(data[2:], false), encUTF16LE
		case data[0] == 0xFE && data[1] == 0xFF:
			return decodeUTF16(data[2:], true), encUTF16BE
		}
	}
	if n == 0 || utf8.ValidString(data) {
		return data, ""
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return data, ""
		}
	}
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		c := data[i]
		switch {
		case c < 0x80, c >= 0xA0:
			out = append(out, rune(c))
		default:
			out = append(out, cp1252High[c-0x80])
		}
	}
	return string(out), encWindows1252
}

func decodeUTF16(b string, bigEndian bool) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(u))
}

// encodeLike converte texto UTF-8 de volta para a codificação detectada na leitura. ok=false se algum caractere não
// cabe na codificação (a edição não deve gravar "?" no lugar).
func encodeLike(text, enc string) (string, bool) {
	switch enc {
	case encUTF16LE, encUTF16BE:
		u := utf16.Encode([]rune(text))
		b := make([]byte, 0, 2+len(u)*2)
		if enc == encUTF16LE {
			b = append(b, 0xFF, 0xFE)
		} else {
			b = append(b, 0xFE, 0xFF)
		}
		for _, v := range u {
			if enc == encUTF16LE {
				b = append(b, byte(v), byte(v>>8))
			} else {
				b = append(b, byte(v>>8), byte(v))
			}
		}
		return string(b), true
	case encWindows1252:
		b := make([]byte, 0, len(text))
	next:
		for _, r := range text {
			switch {
			case r < 0x80, r >= 0xA0 && r <= 0xFF:
				b = append(b, byte(r))
			default:
				for i, h := range cp1252High {
					if h == r {
						b = append(b, byte(0x80+i))
						continue next
					}
				}
				return "", false
			}
		}
		return string(b), true
	}
	return text, true
}
