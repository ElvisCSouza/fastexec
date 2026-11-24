package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

var cursorHeader = regexp.MustCompile(`^\[cursor next=(\d+) size=(\d+)[^\]\n]*\]\n`)

// cursorWalk percorre o log pela resposta do cursor (formatCursorReply sobre transport.ReadLogChunkFile),
// exatamente como o modelo faria: lê o next do cabeçalho e pede de novo. Devolve o corpo reconstruído.
func cursorWalk(t *testing.T, path string) (string, int) {
	t.Helper()
	var body strings.Builder
	var cur int64
	replies := 0
	for {
		ch, err := transport.ReadLogChunkFile(path, cur)
		if err != nil {
			t.Fatal(err)
		}
		reply := formatCursorReply(ch)
		m := cursorHeader.FindStringSubmatch(reply)
		if m == nil {
			t.Fatalf("cabeçalho do cursor ausente: %q", reply[:min(len(reply), 120)])
		}
		if len(reply) > outputCapSize {
			t.Fatalf("resposta do cursor acima do teto de %d: %d", outputCapSize, len(reply))
		}
		next, _ := strconv.ParseInt(m[1], 10, 64)
		if next != ch.Next {
			t.Fatalf("cabeçalho next=%d diverge do entregue %d", next, ch.Next)
		}
		payload := reply[len(m[0]):]
		if int64(len(payload)) != next-cur {
			t.Fatalf("corpo com %d bytes mas next-cursor = %d: a resposta não bate com o avanço", len(payload), next-cur)
		}
		if next == cur {
			break
		}
		body.WriteString(payload)
		cur = next
		replies++
	}
	return body.String(), replies
}

// Requisito: escrever >64 KiB com ANSI, linhas de ruído e uma linha gigante sem '\n'; reconstruir pelo caminho do cursor.
func TestCursorReplyReconstroiLogGrande(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	var want strings.Builder
	for i := 0; want.Len() < 96<<10; i++ {
		want.WriteString(fmt.Sprintf("\x1b[32mOK\x1b[0m passo %05d\n", i))
		if i%7 == 0 {
			want.WriteString("\n\n   \t\n") // ruído: linhas vazias e de espaço
		}
	}
	want.WriteString(strings.Repeat("progresso=", 4000)) // linha gigante sem '\n' no fim
	want.WriteString("\n\x1b[31mfim\x1b[0m\n")
	if err := os.WriteFile(path, []byte(want.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, replies := cursorWalk(t, path)
	if got != want.String() {
		t.Fatalf("cursor não reconstruiu o log: got %d bytes, want %d", len(got), want.Len())
	}
	if replies < 3 {
		t.Fatalf("esperava vários pedaços, veio %d", replies)
	}
	if !strings.Contains(got, "\x1b[31mfim") || !strings.Contains(got, "\n\n   \t\n") {
		t.Fatal("ANSI ou ruído foram alterados no caminho do cursor")
	}
}

// Mesmo cursor pedido duas vezes: resposta idêntica (replay após reconexão).
func TestCursorReplyMesmoCursorIdentico(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("linha de teste\n", 9000)), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := transport.ReadLogChunkFile(path, 1000)
	b, _ := transport.ReadLogChunkFile(path, 1000)
	if formatCursorReply(a) != formatCursorReply(b) {
		t.Fatal("o mesmo cursor devolveu respostas diferentes")
	}
}

// Truncado: cursor além do tamanho atual. A resposta diz 'truncated' e entrega o log desde 0.
func TestCursorReplyTruncado(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reply := formatCursorReply(mustChunk(t, path, 999))
	if !strings.Contains(reply, "truncated") || !strings.HasPrefix(reply, "[cursor next=4 size=4 truncated") {
		t.Fatalf("truncado mal sinalizado: %q", reply)
	}
	if !strings.HasSuffix(reply, "]\nabc\n") {
		t.Fatalf("truncado deveria entregar o log desde 0: %q", reply)
	}
}

// Expirado: o log não existe mais. A resposta diz 'expired' e não entrega corpo.
func TestCursorReplyExpirado(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reply := formatCursorReply(mustChunk(t, path, 0))
	if !strings.Contains(reply, "expired") || !strings.HasSuffix(reply, "]\n") {
		t.Fatalf("expirado mal sinalizado: %q", reply)
	}
}

func mustChunk(t *testing.T, path string, cur int64) transport.LogChunk {
	t.Helper()
	ch, err := transport.ReadLogChunkFile(path, cur)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}
