package transport

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"homelab/fastexec/internal/golden"
)

// TestGoldenHostResolution trava a resolução nome → alvo de todos os hosts conhecidos, do host padrão e dos erros.
// Env pinado (FASTEXEC_DEFAULT_HOST, COMPUTERNAME) para não depender da máquina nem do SO.
// Regenerar: go test ./internal/transport -run TestGoldenHostResolution -update
func TestGoldenHostResolution(t *testing.T) {
	t.Setenv("FASTEXEC_DEFAULT_HOST", "noteserver")
	t.Setenv("COMPUTERNAME", "PC-TESTE")

	inputs := make([]string, 0, len(knownHosts)+8)
	for name := range knownHosts {
		inputs = append(inputs, name)
	}
	sort.Strings(inputs)
	inputs = append(inputs,
		"NOTESERVER", // caixa mista
		"  ax6000  ", // espaços
		"",           // padrão via FASTEXEC_DEFAULT_HOST
		"pc-teste",   // COMPUTERNAME
		"ssh:outro-host",
		"10.0.0.5",
		"10.0.0.6:9200",
		"host-inexistente",
	)

	var b strings.Builder
	for _, in := range inputs {
		fmt.Fprintf(&b, "%q -> ", in)
		target, err := ResolveHost(in)
		if err != nil {
			fmt.Fprintf(&b, "erro: %v\n", err)
			continue
		}
		fmt.Fprintf(&b, "name=%s local=%t ssh=%t sshHost=%s tcp=%s cwd=%s\n",
			target.Name, target.IsLocal, target.IsSSH, target.SSHHost, target.TCPAddr, target.DefaultCwd)
	}

	golden.Check(t, "host_resolution.txt", []byte(b.String()))
}
