package transport

import (
	"strings"
	"testing"
)

func TestLegacyLineWindowsUsaPowerShell(t *testing.T) {
	got := buildLegacyLine("hostname", `C:\inetpub`, "")
	if strings.Contains(got, "export ") || strings.Contains(got, "&&") {
		t.Fatalf("linha Windows com sintaxe POSIX: %q", got)
	}
	if !strings.Contains(got, `Set-Location -LiteralPath 'C:\inetpub'; hostname`) {
		t.Fatalf("linha Windows sem Set-Location: %q", got)
	}
}

func TestLegacyLineWindowsAspaSimplesNoCwd(t *testing.T) {
	got := buildLegacyLine("dir", `C:\o'neil`, "")
	if !strings.Contains(got, `'C:\o''neil'`) {
		t.Fatalf("aspa simples não escapada: %q", got)
	}
}

func TestLegacyLinePOSIXContinuaIgual(t *testing.T) {
	got := buildLegacyLine("ls", "/tmp", "")
	want := "export CI=1 DEBIAN_FRONTEND=noninteractive PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb NO_COLOR=1; cd \"/tmp\" && ls"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
