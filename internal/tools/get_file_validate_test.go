package tools

import (
	"strings"
	"testing"

	"homelab/fastexec/internal/config"
)

// Os erros de get_file saem antes de qualquer dial/ssh/daemon. Como o host de teste (192.0.2.x) não responde, um
// erro que chegasse ao transporte seria de conexão, não o texto de validação: a checagem por texto prova a ordem.
func TestGetFileValidatesBeforeTransport(t *testing.T) {
	cases := []struct {
		name string
		args GetFileArgs
		want string
	}{
		{"remote_path missing", GetFileArgs{Host: "router"}, "get_file needs remote_path: the file path on the remote host, e.g. remote_path='/home/user/x.txt'"},
		{"remote_path blank", GetFileArgs{Host: "router", RemotePath: "   "}, "get_file needs remote_path:"},
		{"host prefix without path", GetFileArgs{RemotePath: "router:"}, "get_file needs remote_path to name a file after the host prefix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := HandleGetFile(c.args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// Sem host e sem host padrão na config, o get_file diz quais nomes existem em vez de ler o disco local por engano.
func TestGetFileMissingHostListsConfiguredNames(t *testing.T) {
	c := config.Sample()
	c.DefaultHost = ""
	restore := config.Set(c)
	defer restore()

	_, err := HandleGetFile(GetFileArgs{RemotePath: "/home/user/x.txt", LocalPath: "x.txt"})
	if err == nil || !strings.Contains(err.Error(), "get_file needs host: no default host is configured") {
		t.Fatalf("error = %v, want the missing-host message", err)
	}
	if !strings.Contains(err.Error(), "Pass host as one of: local, devbox, router, server") {
		t.Fatalf("error = %v, want the configured host names", err)
	}
}

// Um caminho Windows sem host vai para o 'local' mesmo sem host padrão: não é caso de host faltando.
func TestGetFileWindowsPathNeedsNoHost(t *testing.T) {
	c := config.Sample()
	c.DefaultHost = ""
	restore := config.Set(c)
	defer restore()

	_, err := HandleGetFile(GetFileArgs{RemotePath: `C:\no-such-dir-fastexec\x.txt`, LocalPath: "x.txt"})
	if err != nil && strings.Contains(err.Error(), "needs host") {
		t.Fatalf("Windows path was asked for a host: %v", err)
	}
}
