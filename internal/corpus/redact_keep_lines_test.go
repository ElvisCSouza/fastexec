package corpus

import (
	"strings"
	"testing"
)

const canaryToken = "ghp_CANARYaaaaaaaaaaaaaaaaaaaaaa"

func TestRedactKeepLinesMasksCanaryAndKeepsLineCount(t *testing.T) {
	in := "line one\n" +
		"-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\nKUpRKfFLfRYC9AIKjbJTWit+Cqvjy\n-----END RSA PRIVATE KEY-----\n" +
		"token " + canaryToken + "\n" +
		"password=hunter2hunter2\n" +
		"last line"
	out := RedactKeepLines(in, defaultRedactors)

	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Fatalf("line count changed: %d -> %d\n%s", strings.Count(in, "\n"), strings.Count(out, "\n"), out)
	}
	for _, secret := range []string{canaryToken, "hunter2hunter2", "MIIBOgIBAAJBAKj34"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q survived redaction:\n%s", secret, out)
		}
	}
	if !strings.HasPrefix(out, "line one\n") || !strings.HasSuffix(out, "last line") {
		t.Fatalf("surrounding text altered:\n%s", out)
	}
}

func TestRedactKeepLinesLeavesCleanTextAlone(t *testing.T) {
	in := "ok 1\nok 2\né multibyte ✓\n"
	if got := RedactKeepLines(in, defaultRedactors); got != in {
		t.Fatalf("clean text changed: %q", got)
	}
}
