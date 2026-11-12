// Package textenc detects and converts the text encodings that reach FastExec
// but are not plain UTF-8: the Brazilian legacy codepage (Windows-1252) and the
// UTF-16 that Windows PowerShell 5 writes by default.
//
// A file read as UTF-8 when it is really Windows-1252 shows up as "Informa��o",
// and writing UTF-8 back over a UTF-16 file destroys it. Both the reading side
// (`internal/tools`) and the edit applier (`internal/lsp`) use this package so
// there is exactly one definition of what these encodings mean.
package textenc

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Encoding names returned by Decode and accepted by Encode. The empty string
// means "no conversion": the bytes are already UTF-8 (possibly with a UTF-8 BOM,
// which is preserved as a leading U+FEFF in the text).
const (
	Windows1252 = "Windows-1252"
	UTF16LE     = "UTF-16LE"
	UTF16BE     = "UTF-16BE"
)

// cp1252High maps 0x80..0x9F; 0xA0..0xFF match Latin-1 (U+00A0..U+00FF).
// Undefined bytes (0x81, 0x8D, 0x8F, 0x90, 0x9D) become the C1 control of the
// same value, as WHATWG decoders do.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// Decode returns the text in UTF-8 plus the name of the source encoding.
// With no conversion needed (valid UTF-8, empty, or binary) it returns the
// input unchanged and "". Binary (a NUL outside UTF-16 with a BOM) is never
// "decoded": it is not text.
func Decode(data string) (string, string) {
	n := len(data)
	if n >= 2 {
		switch {
		case data[0] == 0xFF && data[1] == 0xFE:
			return decodeUTF16(data[2:], false), UTF16LE
		case data[0] == 0xFE && data[1] == 0xFF:
			return decodeUTF16(data[2:], true), UTF16BE
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
	return string(out), Windows1252
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

// Encode converts UTF-8 text back to the encoding Decode reported. ok=false
// when a character does not fit the encoding: the caller must refuse the edit
// rather than write "?" in its place.
//
// UTF-16 output carries the BOM again; a UTF-8 BOM survives on its own because
// it is part of the text as U+FEFF.
func Encode(text, enc string) (string, bool) {
	switch enc {
	case UTF16LE, UTF16BE:
		u := utf16.Encode([]rune(text))
		b := make([]byte, 0, 2+len(u)*2)
		if enc == UTF16LE {
			b = append(b, 0xFF, 0xFE)
		} else {
			b = append(b, 0xFE, 0xFF)
		}
		for _, v := range u {
			if enc == UTF16LE {
				b = append(b, byte(v), byte(v>>8))
			} else {
				b = append(b, byte(v>>8), byte(v))
			}
		}
		return string(b), true
	case Windows1252:
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

// LineEnding returns the dominant line ending of a text: "\r\n", "\n" or "".
// Used to keep a file's existing convention when new text is inserted.
func LineEnding(text string) string {
	crlf := 0
	lf := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		if i > 0 && text[i-1] == '\r' {
			crlf++
		} else {
			lf++
		}
	}
	switch {
	case crlf > 0 && crlf >= lf:
		return "\r\n"
	case lf > 0:
		return "\n"
	case crlf > 0:
		return "\r\n"
	default:
		return ""
	}
}
