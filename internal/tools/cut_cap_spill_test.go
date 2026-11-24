package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/corpus"
)

// rawAboveCap builds an output over outputCapSize with the canary in the head (inside the 16 KB shown
// in the reply) so the test can tell the reply (raw, as asked by --raw/env) from the redacted spill.
func rawAboveCap() string {
	var b strings.Builder
	for i := 0; b.Len() < 8*1024; i++ {
		fmt.Fprintf(&b, "row %06d ok\n", i)
	}
	fmt.Fprintf(&b, "token=%s\n", cutCanary)
	for i := 0; b.Len() < outputCapSize+20*1024; i++ {
		fmt.Fprintf(&b, "row %06d tail\n", i)
	}
	return b.String()
}

func TestRawAboveCapKeepsCapSpillsRedactedAndCursorReadsBack(t *testing.T) {
	spillDirT := t.TempDir()
	t.Setenv("FASTEXEC_SPILL_DIR", spillDirT)
	in := rawAboveCap()
	if len(in) <= outputCapSize {
		t.Fatalf("entrada deve passar do teto: %d bytes", len(in))
	}
	for _, env := range []string{"FASTEXEC_NO_OUTPUT_FILTER", "FASTEXEC_NO_SHAPE", "FASTEXEC_NO_BUILD_COMPACT"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "1")
			out, info := shapeOrCapInner("", in)

			// the reply is still capped: the first 16 KB, then the footer
			if !strings.HasPrefix(out, in[:outputHeadLen]) || out[outputHeadLen] != '\n' {
				t.Fatalf("resposta deve manter o cabeçalho de 16 KB e o rodapé")
			}
			if len(out) >= len(in) {
				t.Fatalf("resposta não pode ser ilimitada: %d bytes de %d", len(out), len(in))
			}
			if info.spill == "" || !strings.HasPrefix(info.spill, spillDirT) {
				t.Fatalf("spill ausente ou fora do diretório: %+v", info)
			}
			spilled, err := os.ReadFile(info.spill)
			if err != nil {
				t.Fatalf("spill ilegível: %v", err)
			}
			if strings.Contains(string(spilled), cutCanary) {
				t.Fatal("segredo apareceu no spill")
			}
			redacted := corpus.RedactKeepLines(in, corpus.DefaultRedactors())
			if string(spilled) != redacted {
				t.Fatal("spill deve ser o texto integral redigido")
			}
			if !strings.Contains(out, "Spill is redacted") || !strings.Contains(out, filepath.Base(info.spill)) {
				t.Fatalf("rodapé deve avisar o spill redigido e o caminho: %q", out[outputHeadLen:])
			}

			// the cursor is an exact byte offset into the spill, in redacted coordinates
			off := len(corpus.RedactKeepLines(in[:outputHeadLen], corpus.DefaultRedactors()))
			wantCursor := spillBytesCursor(info.spill, off)
			if info.cursor != wantCursor || !strings.Contains(out, wantCursor) {
				t.Fatalf("cursor errado: %q, quero %q", info.cursor, wantCursor)
			}
			page, serr := selectFromText(info.spill, string(spilled), ReadFileArgs{Offset: int64(off), Limit: outputCapSize})
			if serr != nil {
				t.Fatalf("leitura pelo cursor falhou: %v", serr)
			}
			if page != string(spilled)[off:off+outputCapSize] {
				t.Fatal("página do cursor não coincide com o spill naquele offset")
			}
			if !strings.Contains(page, "row 000") && !strings.Contains(page, "tail") {
				t.Fatal("página do cursor vazia de conteúdo esperado")
			}
		})
	}
}

func TestAtOrBelowCapRawNeverSpills(t *testing.T) {
	spillDirT := t.TempDir()
	t.Setenv("FASTEXEC_SPILL_DIR", spillDirT)
	for _, in := range []string{
		strings.Repeat("line of output\n", 2000),  // ~30 KB
		distinctText(outputCapSize),               // exactly 64 KB
	} {
		for _, env := range []string{"FASTEXEC_NO_OUTPUT_FILTER", "FASTEXEC_NO_SHAPE"} {
			t.Setenv(env, "1")
			out, info := shapeOrCapInner("", in)
			if out != in || info.cut() || info.spill != "" {
				t.Fatalf("%s até o teto deve devolver o texto íntegro sem spill (len %d)", env, len(in))
			}
		}
	}
	if entries, _ := os.ReadDir(spillDirT); len(entries) != 0 {
		t.Fatalf("até o teto não deve gravar spill: %d arquivos", len(entries))
	}
}

func TestNoteSpillReadHostRules(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FASTEXEC_SPILL_DIR", dir)
	spill := filepath.Join(dir, "20261124-101010-aaaaaaaa.txt")
	other := filepath.Join(t.TempDir(), "20261124-101010-aaaaaaaa.txt")

	var marked []string
	prev := markCutRecovered
	markCutRecovered = func(p string) { marked = append(marked, p) }
	defer func() { markCutRecovered = prev }()

	cases := []struct {
		name, host, path string
		want             bool
	}{
		{"host vazio marca", "", spill, true},
		{"local marca", "local", spill, true},
		{"LOCAL marca (case-insensitive)", "LOCAL", spill, true},
		{"remoto não marca spill local", "server", spill, false},
		{"caminho que não é spill não marca", "", other, false},
		{"vazio sem caminho não marca", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			marked = nil
			got := noteSpillRead(c.host, c.path)
			if got != c.want {
				t.Fatalf("noteSpillRead(%q, %q) = %v, quero %v", c.host, c.path, got, c.want)
			}
			if c.want && (len(marked) != 1 || marked[0] != c.path) {
				t.Fatalf("marcação errada: %v", marked)
			}
			if !c.want && len(marked) != 0 {
				t.Fatalf("não deveria marcar: %v", marked)
			}
		})
	}
}
