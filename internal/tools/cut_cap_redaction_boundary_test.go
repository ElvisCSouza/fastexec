package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Um Bearer começa antes do corte, mas só 3 bytes do token entram no prefixo mostrado. O prefixo
// isolado não atinge o mínimo da regra e não é redigido; o texto integral é. O cursor não pode
// usar o corte original cegamente: precisa recuar para o prefixo comum e continuar sem saltar o resto.
func TestCapCursorRedactionBoundaryNeverSkips(t *testing.T) {
	t.Setenv("FASTEXEC_SPILL_DIR", t.TempDir())
	marker := "SUFFIX-MARKER-AFTER-SECRET"
	in := strings.Repeat("a", outputHeadLen-11) + " Bearer " + strings.Repeat("X", 40) + " " + marker + strings.Repeat("b\n", outputCapSize)
	_, info := capOutputInfo(in)
	if info.spill == "" {
		t.Fatal("spill ausente")
	}
	spilled, err := os.ReadFile(info.spill)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`offset=([0-9]+)`).FindStringSubmatch(info.cursor)
	if m == nil {
		t.Fatalf("cursor sem offset: %q", info.cursor)
	}
	off, _ := strconv.Atoi(m[1])
	if off < 0 || off >= outputHeadLen {
		t.Fatalf("offset deveria recuar diante da redação que cruza o corte: off=%d cut=%d cursor=%q", off, outputHeadLen, info.cursor)
	}
	if off > len(spilled) || !utf8.Valid(spilled[off:]) {
		t.Fatalf("offset inválido no spill: off=%d size=%d", off, len(spilled))
	}
	rest := string(spilled[off:])
	if !strings.Contains(rest, "[REDACTED]") {
		t.Fatalf("continuação não começa no segredo redigido: off=%d trecho=%q", off, rest[:min(len(rest), 120)])
	}
	if !strings.Contains(rest, marker) {
		t.Fatalf("continuação saltou o sufixo: off=%d trecho=%q", off, rest[:min(len(rest), 120)])
	}
	if strings.Contains(rest, strings.Repeat("X", 40)) {
		t.Fatal("segredo-canário apareceu no spill")
	}
	if !strings.Contains(info.cursor, fmt.Sprintf("offset=%d", off)) {
		t.Fatalf("cursor não usa o offset seguro: %q", info.cursor)
	}
}
