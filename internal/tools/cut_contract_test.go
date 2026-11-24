package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"homelab/fastexec/internal/transport"
)

const cutCanary = "ghp_CANARYaaaaaaaaaaaaaaaaaaaaaa"

func TestCapOutputInfoMultibyteExactLimit(t *testing.T) {
	in := strings.Repeat("é", 40*1024) // 80 KB of 2-byte runes, one line
	out, info := capOutputInfo(in)
	head := out[:strings.Index(out, "\n"+truncMarker)]
	if len(head) != outputHeadLen {
		t.Fatalf("cabeça deveria ter %d bytes exatos, tem %d", outputHeadLen, len(head))
	}
	if !utf8.ValidString(head) {
		t.Fatal("corte no meio de caractere UTF-8")
	}
	if info.origBytes != len(in) || info.retBytes != len(head) {
		t.Fatalf("contagens de bytes erradas: %+v", info)
	}
	if info.filter != "head only" || info.reason != "teto" {
		t.Fatalf("motivo/filtro errados: %+v", info)
	}
	want := fmt.Sprintf("Original 1 lines / %d bytes; returned 1 lines / %d bytes; omitted 0 lines / %d bytes.",
		len(in), len(head), len(in)-len(head))
	if !strings.Contains(out, want) {
		t.Fatalf("rodapé sem contagens exatas:\n%s", out[len(head):])
	}
}

func TestCapOutputInfoAtLimitNotCut(t *testing.T) {
	in := strings.Repeat("ñ", outputCapSize/2) // exactly 64 KB
	out, info := capOutputInfo(in)
	if out != in || info.cut() {
		t.Fatal("saída no limite exato não deve ser cortada")
	}
}

func TestShapeBigOneEnormousMultibyteLine(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	in := strings.Repeat("é", 20000) // one line, 40000 bytes
	out, info, ok := shapeBig(in, execSoftCapDefault)
	if !ok {
		t.Fatal("shapeBig deveria cortar a linha única")
	}
	if info.filter != "byte cut (one line)" || info.origLines != 1 || info.retLines != 1 {
		t.Fatalf("corte por bytes errado: %+v", info)
	}
	head := out[:strings.Index(out, "\n"+truncMarker)]
	if !utf8.ValidString(head) || len(head) != info.retBytes || len(head) > execSoftCapDefault {
		t.Fatalf("cabeça inválida: len=%d retBytes=%d", len(head), info.retBytes)
	}
}

func TestShapeBigCursorCountsAndSpill(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	var lines []string
	for i := 1; i <= 3000; i++ {
		lines = append(lines, fmt.Sprintf("line %05d ok", i))
	}
	in := strings.Join(lines, "\n")
	out, info, ok := shapeBig(in, execSoftCapDefault)
	if !ok || info.filter != "head+tail" {
		t.Fatalf("esperava head+tail: ok=%v %+v", ok, info)
	}
	if info.origLines != 3000 || info.origBytes != len(in) {
		t.Fatalf("contagens originais erradas: %+v", info)
	}
	if info.retLines <= 0 || info.retLines >= 3000 {
		t.Fatalf("linhas retornadas fora do esperado: %+v", info)
	}
	if !strings.HasPrefix(info.cursor, "read_file(path, ranges='") {
		t.Fatalf("cursor inesperado: %q", info.cursor)
	}
	if !strings.Contains(out, info.cursor) && !strings.Contains(out, "Next: read_file(path, ranges=") {
		t.Fatalf("cursor ausente do rodapé: %q", out)
	}
	var a, b int
	if _, err := fmt.Sscanf(info.cursor, "read_file(path, ranges='%d-%d')", &a, &b); err != nil {
		t.Fatalf("cursor não parseável: %v", err)
	}
	if a < 2 || b < a || b-a > 80 {
		t.Fatalf("faixa do cursor fora do limite: %d-%d", a, b)
	}
	got, err := os.ReadFile(info.spill)
	if err != nil || string(got) != in {
		t.Fatalf("spill deveria ter o texto integral: %v", err)
	}
	if filepath.Dir(info.spill) != spillDir() {
		t.Fatalf("spill fora do diretório: %s", info.spill)
	}
}

func TestSpillIsRedactedWithCanaryMasked(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	var b strings.Builder
	for i := 1; i <= 1500; i++ {
		fmt.Fprintf(&b, "step %d done\n", i)
		if i == 700 {
			fmt.Fprintf(&b, "token=%s\n", cutCanary)
		}
	}
	in := b.String()
	out, info, ok := shapeBig(in, execSoftCapDefault)
	if !ok {
		t.Fatal("shapeBig falhou")
	}
	got, err := os.ReadFile(info.spill)
	if err != nil {
		t.Fatalf("spill ilegível: %v", err)
	}
	if strings.Contains(string(got), cutCanary) {
		t.Fatal("segredo do canário apareceu no spill")
	}
	if strings.Count(string(got), "\n") != strings.Count(in, "\n") {
		t.Fatal("redação alterou a contagem de linhas do spill")
	}
	if strings.Contains(out, cutCanary) {
		t.Fatal("segredo apareceu na resposta")
	}
	if !strings.Contains(out, "Spill is redacted") {
		t.Fatalf("resposta não avisa que o spill é redigido: %q", out[:200])
	}
}

func TestRawAndEnvKeepTextByteForByte(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	in := strings.Repeat("line of output\n", 2000) // ~30 KB, above the soft cap, under 64 KB
	for _, env := range []string{"FASTEXEC_NO_OUTPUT_FILTER", "FASTEXEC_NO_SHAPE", "FASTEXEC_NO_BUILD_COMPACT"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "1")
			out, info := shapeOrCapInner("", in)
			if out != in || info.cut() {
				t.Fatalf("%s deve devolver o texto íntegro", env)
			}
		})
	}
	if entries, _ := os.ReadDir(spillDir()); len(entries) != 0 {
		t.Fatalf("modo sem filtro não deve gravar spill: %d arquivos", len(entries))
	}
}

func TestFilterFailureReturnsIntegralText(t *testing.T) {
	// spill directory unusable: the shaper must fall back to the integral text under the cap
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FASTEXEC_SPILL_DIR", filepath.Join(blocker, "spill"))
	in := distinctText(44 * 1024) // distinct lines: nothing to compact, so the shaper must try to spill
	out, err := formatExecResult("cmd", transport.ExecResult{Stdout: in})
	if err != nil {
		t.Fatalf("err inesperado: %v", err)
	}
	if out != in {
		t.Fatalf("falha no filtro deve devolver o texto íntegro (len %d -> %d)", len(in), len(out))
	}
}
