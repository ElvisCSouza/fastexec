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

func distinctText(size int) string {
	var b strings.Builder
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "linha %08d: resultado ok\n", i)
	}
	return b.String()[:size]
}

func TestCleanOutputRemovesANSI(t *testing.T) {
	in := "\x1b[31mERRO\x1b[0m ok\n\x1b]0;título\x07texto\x1b[2K\n"
	want := "ERRO ok\ntexto\n"
	if got := cleanOutput(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCleanOutputRemovesTerminalNoise(t *testing.T) {
	in := "commit 1234abc\nhint: Using 'master' as the name for the initial branch\nhint: This default branch name is subject to change.\nAuthor: Elvis\nnpm notice New major version of npm available! 10.1.0 -> 10.8.2\ncode running\n"
	want := "commit 1234abc\nAuthor: Elvis\ncode running\n"
	if got := cleanOutput(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	pipNoise := "instalado com sucesso\n[notice] A new release of pip is available: 23.0 -> 24.0\n[notice] To update, run: python -m pip install --upgrade pip\n"
	wantPip := "instalado com sucesso\n"
	if got := cleanOutput(pipNoise); got != wantPip {
		t.Fatalf("pip noise: got %q, want %q", got, wantPip)
	}

	psNoise := "A new PowerShell prerelease is available: https://aka.ms/PSWindows\nresultado\n"
	wantPs := "resultado\n"
	if got := cleanOutput(psNoise); got != wantPs {
		t.Fatalf("powershell noise: got %q, want %q", got, wantPs)
	}
}

func TestCleanOutputCollapsesRepeatedLines(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"lf":    {"a\na\na\nb\n", "a ×3\nb\n"},
		"crlf":  {"a\r\na\r\n", "a ×2\r\n"},
		"unica": {"a\nb\n", "a\nb\n"},
		"nv":    {"a\nb\na\n", "a\nb\na\n"},
	}
	for name, c := range cases {
		if got := cleanOutput(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestCleanOutputCapsLongLines(t *testing.T) {
	in := strings.Repeat("x", 450) + "\n"
	want := strings.Repeat("x", 400) + "…(+50 chars)\n"
	if got := cleanOutput(in); got != want {
		t.Fatalf("linha ASCII: got %d bytes, want %d", len(got), len(want))
	}

	in = strings.Repeat("á", 401)
	want = strings.Repeat("á", 400) + "…(+1 chars)"
	if got := cleanOutput(in); got != want {
		t.Fatalf("linha UTF-8: got %q, want %q", got, want)
	}

	in = strings.Repeat("y", 400)
	if got := cleanOutput(in); got != in {
		t.Fatalf("linha de exatamente 400 não deve ser cortada")
	}
}

func TestFormatExecResultNoResultOnExit1Empty(t *testing.T) {
	out, err := formatExecResult("findstr x", transport.ExecResult{ExitCode: 1})
	if out != NoResultText || err != nil {
		t.Fatalf("got (%q, %v), want (%q, nil)", out, err, NoResultText)
	}
	if !IsNoResult(out) {
		t.Fatalf("IsNoResult deveria reconhecer o texto de sem resultado")
	}
}

func TestFormatExecResultKeepsOtherFailures(t *testing.T) {
	out, err := formatExecResult("cmd", transport.ExecResult{ExitCode: 1, Stdout: "falhou\n"})
	if out != "[exit 1] falhou\n" || err == nil {
		t.Errorf("exit 1 com saída: got (%q, %v)", out, err)
	}

	out, err = formatExecResult("cmd", transport.ExecResult{ExitCode: 2})
	if out != "[exit 2] comando falhou com exit code 2 (sem saída)" || err == nil {
		t.Errorf("exit 2 vazio: got (%q, %v)", out, err)
	}

	out, err = formatExecResult("cmd", transport.ExecResult{ExitCode: 2, Stderr: "acesso negado"})
	if out != "[exit 2] acesso negado" || err == nil {
		t.Errorf("exit 2 com stderr: got (%q, %v)", out, err)
	}
}

func TestFormatExecResultCleanOutputIsByteIdentical(t *testing.T) {
	cases := []string{
		"saida simples\n",
		"linha1\r\nlinha2\r\n",
		"sem quebra final",
		"ação ñandú — ok\n",
		"x\n" + strings.Repeat("y", 400) + "\n",
		distinctText(1000),
		distinctText(63 * 1024),
	}
	for _, stdout := range cases {
		out, err := formatExecResult("cmd", transport.ExecResult{Stdout: stdout})
		if err != nil || out != stdout {
			t.Errorf("stdout de %d bytes alterado (err=%v)", len(stdout), err)
		}
	}

	out, err := formatExecResult("cmd", transport.ExecResult{Stderr: "erro\n"})
	if err != nil || out != "erro\n" {
		t.Errorf("stderr-only: got (%q, %v)", out, err)
	}

	out, err = formatExecResult("cmd", transport.ExecResult{})
	if err != nil || out != "" {
		t.Errorf("exit 0 vazio: got (%q, %v)", out, err)
	}
}

func TestFormatExecResultCapAt63KBIsUntouched(t *testing.T) {
	in := distinctText(63 * 1024)
	out, err := formatExecResult("cmd", transport.ExecResult{Stdout: in})
	if err != nil || out != in {
		t.Fatalf("saída de 63 KB não deve ser cortada (len=%d)", len(out))
	}
}

func TestFormatExecResultCapAt65KB(t *testing.T) {
	in := distinctText(65 * 1024)
	out, err := formatExecResult("cmd", transport.ExecResult{Stdout: in})
	if err != nil {
		t.Fatalf("err inesperado: %v", err)
	}
	marker := fmt.Sprintf("\n[truncado: %d bytes; só os primeiros 16 KB foram devolvidos]", len(in))
	if !strings.HasSuffix(out, marker) {
		t.Fatalf("marcador de truncamento ausente; fim=%q", out[len(out)-120:])
	}
	if head := out[:len(out)-len(marker)]; head != in[:16*1024] {
		t.Fatalf("cabeça devolvida deveria ter 16 KB exatos")
	}
	if !IsTruncated(out) {
		t.Fatalf("IsTruncated deveria reconhecer a saída truncada")
	}
}

func TestCapOutputDoesNotSplitUTF8(t *testing.T) {
	in := strings.Repeat("a", outputHeadLen-1) + strings.Repeat("á", 300*1024)
	out := capOutput(in)
	head := out[:strings.Index(out, "\n[truncado: ")]
	if !utf8.ValidString(head) {
		t.Fatalf("corte no meio de caractere UTF-8")
	}
}

func TestIsTruncatedIgnoresPlainOutput(t *testing.T) {
	if IsTruncated("saida normal\n") || IsNoResult("saida normal") {
		t.Fatalf("saída comum não deve ser marcada")
	}
}

func TestFileToolsReplyShortOK(t *testing.T) {
	dir := t.TempDir()
	content := "conteúdo de teste\n"

	out, err := HandleWriteFile(WriteFileArgs{Host: "local", Path: filepath.Join(dir, "a.txt"), Content: content})
	if err != nil || out != fmt.Sprintf("ok %d bytes", len(content)) {
		t.Fatalf("write_file: got (%q, %v)", out, err)
	}

	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = HandlePutFile(PutFileArgs{Host: "local", LocalPath: src, RemotePath: filepath.Join(dir, "dst.bin")})
	if err != nil || out != "ok 10 bytes" {
		t.Fatalf("put_file: got (%q, %v)", out, err)
	}

	if got := okReply(2048); got != "ok 2048 bytes" {
		t.Fatalf("get_file usa okReply: got %q", got)
	}
}
