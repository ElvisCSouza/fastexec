package transport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// walkLog lê o arquivo do cursor 0 até o fim, pedaço a pedaço, e devolve o que foi reconstruído.
func walkLog(t *testing.T, path string) (string, int) {
	t.Helper()
	var got strings.Builder
	var cur int64
	chunks := 0
	for {
		ch, err := readLogChunk(path, cur)
		if err != nil {
			t.Fatal(err)
		}
		if ch.Truncated || ch.Expired {
			t.Fatalf("leitura contínua não deveria sinalizar truncado/expirado: %+v", ch)
		}
		if ch.Next == cur {
			break
		}
		if len(ch.Text) > maxLogChunk {
			t.Fatalf("chunk acima do limite: %d", len(ch.Text))
		}
		if int64(len(ch.Text)) != ch.Next-cur {
			t.Fatalf("Next não bate com o entregue: len=%d next-cur=%d", len(ch.Text), ch.Next-cur)
		}
		got.WriteString(ch.Text)
		cur = ch.Next
		chunks++
	}
	return got.String(), chunks
}

// Cursor sem gap: ler em pedaços a partir do cursor devolvido reconstrói o log exato, sem perder nem repetir bytes.
func TestReadLogChunkSemGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	var want strings.Builder
	for i := 0; want.Len() < 150<<10; i++ {
		want.WriteString(fmt.Sprintf("linha %06d com algum texto de preenchimento\n", i))
	}
	if err := os.WriteFile(path, []byte(want.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, chunks := walkLog(t, path)
	if got != want.String() {
		t.Fatalf("cursor perdeu ou repetiu bytes: got %d, want %d", len(got), want.Len())
	}
	if chunks < 5 {
		t.Fatalf("esperava vários pedaços para 150 KiB, veio %d", chunks)
	}
}

// Linha gigante sem '\n' (ex.: progresso com \r ou dump binário): entregue em pedaços, nada descartado.
func TestReadLogChunkLinhaGigantePlana(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	want := strings.Repeat("x", 100<<10)
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := walkLog(t, path); got != want {
		t.Fatalf("linha sem \\n perdeu bytes: got %d, want %d", len(got), len(want))
	}
}

// Multibyte sem '\n' (acento, euro, emoji) cortado na fronteira do bloco: nenhum pedaço parte uma runa.
func TestReadLogChunkUTF8NaoPartePedaco(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	unit := "é€😀a"
	want := strings.Repeat(unit, 40<<10/len(unit)+1)
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	var cur int64
	var got strings.Builder
	for {
		ch, err := readLogChunk(path, cur)
		if err != nil {
			t.Fatal(err)
		}
		if ch.Next == cur {
			break
		}
		if !utf8.ValidString(ch.Text) {
			t.Fatalf("pedaço partiu uma runa no cursor %d", cur)
		}
		got.WriteString(ch.Text)
		cur = ch.Next
	}
	if got.String() != want {
		t.Fatalf("reconstrução UTF-8 diferente: got %d, want %d", got.Len(), len(want))
	}
}

// cutPoint: função pura; fronteira no meio de runa recua até o início dela.
func TestCutPointRecuaAntesDeRunaIncompleta(t *testing.T) {
	block := append(bytes.Repeat([]byte("a"), 10), []byte("€")[:2]...) // € = E2 82 AC; só 2 bytes no bloco
	if got := cutPoint(block, false); got != 10 {
		t.Fatalf("cutPoint = %d, want 10", got)
	}
	if got := cutPoint(block, true); got != len(block) {
		t.Fatalf("no fim do log deve entregar tudo: %d", got)
	}
	nl := []byte("ab\ncd€")
	if got := cutPoint(nl, false); got != 3 {
		t.Fatalf("corte após \\n: %d, want 3", got)
	}
}

// Cursor redelivery: pedir de novo do mesmo cursor devolve exatamente o mesmo pedaço (replay após reconexão).
func TestReadLogChunkRedeliver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte("um\ndois\ntres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := readLogChunk(path, 3)
	b, _ := readLogChunk(path, 3)
	if a.Text != b.Text || a.Text != "dois\ntres\n" || a.Next != 13 {
		t.Fatalf("redelivery diferente: %+v vs %+v", a, b)
	}
}

// Sinais: cursor além do tamanho = truncado (recomeça em 0); log removido = expirado.
func TestReadLogChunkSinais(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := readLogChunk(path, 999)
	if err != nil || !ch.Truncated || ch.Next != 4 || ch.Text != "abc\n" {
		t.Fatalf("truncado não sinalizado: %+v %v", ch, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ch, err = readLogChunk(path, 0)
	if err != nil || !ch.Expired {
		t.Fatalf("expirado não sinalizado: %+v %v", ch, err)
	}
}
