package tools

import (
	"fmt"
	"strconv"

	"homelab/fastexec/internal/transport"
)

// cursorChunk entrega o log a partir do cursor (offset em bytes da resposta anterior). Sem filtro: o que está
// entre cursor e next é exatamente o que vai na resposta (ver formatCursorReply).
func cursorChunk(jobID, cursor string) (string, error) {
	cur, perr := strconv.ParseInt(cursor, 10, 64)
	if perr != nil || cur < 0 {
		return "", fmt.Errorf("cursor must be the decimal byte offset returned by a previous tail/wait")
	}
	ch, cerr := transport.LocalJobLogChunk(jobID, cur)
	if cerr != nil {
		return "", cerr
	}
	return formatCursorReply(ch), nil
}

// formatCursorReply monta a resposta do cursor: cabeçalho "[cursor next=N size=S ...]" e, depois, os bytes exatos
// de [cursor, next). Não há cleanOutput nem capOutput aqui: se algum filtro alterasse o texto depois de calculado
// o next, os bytes sumiriam sem aviso. O tamanho já é limitado na origem (transport.maxLogChunk, abaixo do teto
// de resposta), e o cliente continua com o next do cabeçalho.
func formatCursorReply(ch transport.LogChunk) string {
	head := fmt.Sprintf("[cursor next=%d size=%d", ch.Next, ch.Size)
	if ch.Truncated {
		head += " truncated: log shorter than cursor, restarted from 0"
	}
	if ch.Expired {
		head += " expired: log no longer exists"
	}
	return head + "]\n" + ch.Text
}
