package transport

import (
	"bytes"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/charmap"
)

// Saída de processos no Windows chega em encodings misturados: runtimes modernos (node, git, python, rg) escrevem UTF-8,
// utilitários clássicos (cmd.exe, findstr, erros do sistema) escrevem na code page OEM do console (CP850 no pt-BR).
// Sem tratamento, "não" vira "n\ufffdo" e qualquer comparação textual com a mensagem do sistema falha.
//
// Estratégia: por linha. Linha UTF-8 válida fica intacta; linha inválida é lida como OEM. Texto acentuado em CP850/CP437
// quase nunca forma uma sequência UTF-8 válida, então o falso positivo é desprezível. Não usa chcp (que injetaria
// "Active code page" na saída e não afeta programas DOS redirecionados para pipe).

var oemMap = map[uint32]*charmap.Charmap{
	437:  charmap.CodePage437,
	850:  charmap.CodePage850,
	852:  charmap.CodePage852,
	860:  charmap.CodePage860,
	863:  charmap.CodePage863,
	865:  charmap.CodePage865,
	866:  charmap.CodePage866,
	1252: charmap.Windows1252,
}

func oemCharmap() *charmap.Charmap {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP")
	if cp, _, _ := proc.Call(); cp != 0 {
		if m, ok := oemMap[uint32(cp)]; ok {
			return m
		}
	}
	return charmap.CodePage850
}

// decodeMixed devolve b como UTF-8, preservando exatamente os terminadores de linha originais.
func decodeMixed(b []byte) string {
	if utf8.Valid(b) { // caminho rápido e mais comum
		return string(b)
	}
	dec := oemCharmap().NewDecoder()
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/4)
	for len(b) > 0 {
		end := bytes.IndexByte(b, '\n') + 1
		if end == 0 {
			end = len(b)
		}
		line := b[:end]
		b = b[end:]
		if utf8.Valid(line) {
			out.Write(line)
			continue
		}
		if conv, err := dec.Bytes(line); err == nil {
			out.Write(conv)
		} else {
			out.Write(line)
		}
	}
	return out.String()
}
