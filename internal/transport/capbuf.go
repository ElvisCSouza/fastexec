package transport

import "fmt"

// Limites do buffer de captura por stream. O pipeline de outclean.go reduz a saída para 256 KB; aqui só impedimos que um
// processo que cospe saída sem parar (loop infinito) aloque gigabytes antes de o timeout encerrá-lo.
const (
	capHeadMax = 3 << 20 // primeiros 3 MiB
	capTailMax = 1 << 20 // últimos 1 MiB (o fim costuma ter o erro que importa)
)

// capBuffer é um io.Writer que nunca falha e nunca passa de ~4 MiB: guarda o começo e o fim da saída e descarta o meio.
// Nunca devolve erro de Write, para o processo filho não morrer por erro de escrita enquanto o limite é aplicado.
type capBuffer struct {
	head    []byte
	tail    []byte // anel de capTailMax bytes; só é usado depois de o head encher
	tailPos int    // próxima posição de escrita no anel
	tailLen int    // bytes válidos no anel
	dropped int64  // bytes do meio que foram descartados
}

func (b *capBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := capHeadMax - len(b.head); room > 0 {
		take := min(room, len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}
	if b.tail == nil {
		b.tail = make([]byte, capTailMax)
	}
	if len(p) > capTailMax { // só o último capTailMax de p interessa
		b.dropped += int64(len(p)-capTailMax) + int64(b.tailLen)
		p = p[len(p)-capTailMax:]
		b.tailPos, b.tailLen = 0, 0
	}
	for len(p) > 0 {
		c := copy(b.tail[b.tailPos:], p)
		p = p[c:]
		if over := b.tailLen + c - capTailMax; over > 0 {
			b.dropped += int64(over)
		}
		b.tailPos = (b.tailPos + c) % capTailMax
		b.tailLen = min(b.tailLen+c, capTailMax)
	}
	return n, nil
}

// Bytes devolve a captura; se houve descarte, marca o corte entre o começo e o fim.
func (b *capBuffer) Bytes() []byte {
	if b.tailLen == 0 {
		return b.head
	}
	tail := b.tail[:b.tailLen]
	if b.tailLen == capTailMax && b.tailPos != 0 { // anel já deu a volta: reordenar do mais antigo ao mais novo
		tail = append(append(make([]byte, 0, capTailMax), b.tail[b.tailPos:]...), b.tail[:b.tailPos]...)
	}
	out := make([]byte, 0, len(b.head)+len(tail)+80)
	out = append(out, b.head...)
	if b.dropped > 0 {
		out = append(out, fmt.Sprintf("\n[... %d bytes descartados pelo limite de captura ...]\n", b.dropped)...)
	}
	return append(out, tail...)
}
