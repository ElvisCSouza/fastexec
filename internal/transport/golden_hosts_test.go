package transport

import (
	"fmt"
	"strings"
	"testing"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/golden"
)

// TestGoldenHostResolution trava a resolução nome → alvo com a config de exemplo e sem config.
// COMPUTERNAME pinado para não depender da máquina. Regenerar: go test ./internal/transport -run TestGoldenHostResolution -update
func TestGoldenHostResolution(t *testing.T) {
	t.Setenv("COMPUTERNAME", "PC-TESTE")

	var b strings.Builder
	resolve := func(inputs ...string) {
		for _, in := range inputs {
			fmt.Fprintf(&b, "%q -> ", in)
			target, err := ResolveHost(in)
			if err != nil {
				fmt.Fprintf(&b, "error: %v\n", err)
				continue
			}
			fmt.Fprintf(&b, "name=%s local=%t ssh=%t sshHost=%s tcp=%s cwd=%s\n",
				target.Name, target.IsLocal, target.IsSSH, target.SSHHost, target.TCPAddr, target.DefaultCwd)
		}
	}

	b.WriteString("# config: sample\n")
	restore := config.Set(config.Sample())
	resolve("local", "localhost", "127.0.0.1", "server", "srv", "router", "devbox",
		"SERVER",     // caixa mista
		"  router  ", // espaços
		"",           // host padrão da config
		"pc-teste",   // COMPUTERNAME
		"ssh:other-host", "10.0.0.5", "10.0.0.6:9200", "no-such-host")
	restore()

	b.WriteString("# config: none\n")
	restore = config.Set(&config.Config{})
	resolve("", "local", "server", "ssh:other-host", "10.0.0.5")
	restore()

	golden.Check(t, "host_resolution.txt", []byte(b.String()))
}
