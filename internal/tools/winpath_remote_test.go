package tools

import (
	"strings"
	"testing"
)

func TestIsWindowsPath(t *testing.T) {
	for _, p := range []string{`W:`, `W:\ReposHomeLab\x`, `c:/x`, `\\nas\share\x`} {
		if !isWindowsPath(p) {
			t.Errorf("%q é caminho Windows", p)
		}
	}
	for _, p := range []string{`/home/x`, `x:y`, `W:x`, `server:/tmp`, ``, `relativo/W:`} {
		if isWindowsPath(p) {
			t.Errorf("%q não é caminho Windows", p)
		}
	}
}

// Caminho Windows com host remoto que não é o padrão (o padrão + caminho Windows já ia para local): recusa antes de criar uma pasta 'W:' no Linux.
func TestResolveTargetHostWindowsPathRemoto(t *testing.T) {
	for _, c := range []struct{ host, path string }{
		{"router", `W:\ReposHomeLab\x.txt`},
		{"router", `W:/ReposHomeLab/x.txt`},
		{"", `router:W:\ReposHomeLab\x.txt`},
	} {
		_, err := ResolveTargetHost(c.host, c.path)
		if err == nil || !strings.Contains(err.Error(), "is a Windows path, but host 'router' is Linux") ||
			!strings.Contains(err.Error(), "host='local'") {
			t.Errorf("%+v: esperava recusa com a orientação, veio %v", c, err)
		}
	}
	// o que já funcionava continua: caminho Linux no remoto e caminho Windows sem host
	if tg, err := ResolveTargetHost("server", "/home/user/x"); err != nil || tg.IsLocal {
		t.Errorf("caminho Linux no remoto: %v %+v", err, tg)
	}
	if tg, err := ResolveTargetHost("", `W:\x`); err != nil || !tg.IsLocal {
		t.Errorf("caminho Windows sem host vai para local: %v %+v", err, tg)
	}
	// write_file não chega a tocar o remoto
	if _, err := HandleWriteFile(WriteFileArgs{Host: "router", Path: `W:\x\y.txt`, Content: "a"}); err == nil {
		t.Error("write_file deveria recusar caminho Windows no host remoto")
	}
}
