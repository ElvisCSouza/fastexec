package transport

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"unicode/utf8"
)

// maxLogChunk limita cada resposta incremental do log (32 KiB). Fica abaixo do teto de resposta da ferramenta
// (64 KiB) com folga para o cabeçalho do cursor, então o pedaço nunca é cortado depois de calculado o Next.
const maxLogChunk = 32 << 10

// LogChunkLimit é o tamanho máximo de um pedaço do log (bytes). Exposto para o caminho remoto (Linux) pedir no máximo
// esse bloco ao host e aplicar o mesmo corte.
const LogChunkLimit = maxLogChunk

// LogChunk é uma leitura incremental do log de um job. O cursor é um offset em bytes do log:
// o cliente guarda Next e pede de novo a partir dele, sem perder nem repetir bytes.
type LogChunk struct {
	Text      string // bytes do log de [cursor, Next): exatamente o que foi entregue, sem filtro
	Next      int64  // cursor para a próxima chamada (= cursor + len(Text))
	Size      int64  // tamanho atual do log
	Truncated bool   // cursor além do tamanho atual: o log foi encolhido/recriado; a leitura recomeça em 0
	Expired   bool   // o log não existe mais (job expirado e limpo): não há o que entregar
}

// ReadLogChunkFile lê um pedaço do log a partir de cursor. Não guarda estado: o chamador guarda o cursor.
// É a mesma função usada pelo Windows e pelo Linux (log materializado em arquivo), então o contrato é um só.
func ReadLogChunkFile(path string, cursor int64) (LogChunk, error) {
	return readLogChunk(path, cursor)
}

func readLogChunk(path string, cursor int64) (LogChunk, error) {
	if cursor < 0 {
		cursor = 0
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LogChunk{Expired: true}, nil
		}
		return LogChunk{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return LogChunk{}, err
	}
	out := LogChunk{Size: st.Size()}
	if cursor > out.Size {
		out.Truncated = true
		cursor = 0
	}
	if cursor == out.Size {
		out.Next = cursor
		return out, nil
	}
	buf := make([]byte, min(int64(maxLogChunk), out.Size-cursor))
	n, err := io.ReadFull(io.NewSectionReader(f, cursor, int64(len(buf))), buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return LogChunk{}, err
	}
	ch := ChunkFromBlock(buf[:n], cursor, out.Size)
	ch.Size = out.Size
	ch.Truncated = out.Truncated
	return ch, nil
}

// ChunkFromBlock monta o pedaço a partir dos bytes [cursor, cursor+len(block)) de um log de tamanho size. É o único
// ponto de corte: o caminho local (arquivo) e o remoto (base64 vindo do host Linux) passam por aqui, então o contrato
// de bytes é o mesmo nos dois.
func ChunkFromBlock(block []byte, cursor, size int64) LogChunk {
	end := cutPoint(block, cursor+int64(len(block)) >= size)
	return LogChunk{Text: string(block[:end]), Next: cursor + int64(end), Size: size}
}

// cutPoint decide até onde o bloco é entregue. Função pura (testável em qualquer SO):
//   - no fim do log, entrega tudo;
//   - senão, corta após o último '\n' (não parte linha);
//   - sem '\n' no bloco, corta antes de um caractere UTF-8 incompleto no fim (não parte runa).
//
// Nunca retorna 0 para um bloco cheio: um bloco não-final tem 32 KiB, e 4 bytes bastam para uma runa.
func cutPoint(block []byte, atEOF bool) int {
	if atEOF {
		return len(block)
	}
	if i := bytes.LastIndexByte(block, '\n'); i >= 0 {
		return i + 1
	}
	end := len(block)
	for k := 1; k <= utf8.UTFMax && k <= end; k++ {
		if utf8.RuneStart(block[end-k]) {
			if !utf8.FullRune(block[end-k:]) {
				return end - k
			}
			return end
		}
	}
	return end
}
