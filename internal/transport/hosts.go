package transport

import (
	"fmt"
	"os"
	"strings"

	"homelab/fastexec/internal/config"
)

type HostTarget struct {
	Name       string
	IsLocal    bool
	IsSSH      bool
	SSHHost    string
	TCPAddr    string
	DefaultCwd string
}

// ResolveHost traduz um nome em alvo. Ordem: vazio = host padrão da config; nome ou alias da config; nome do
// próprio computador; 'ssh:<host>'; IP ou nome com ponto (porta 9142 se omitida).
func ResolveHost(name string) (HostTarget, error) {
	cfg := config.Get()
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		n = cfg.Default()
	}
	if canonical, h, ok := cfg.Lookup(n); ok {
		switch {
		case canonical == "local":
			return HostTarget{Name: "local", IsLocal: true}, nil
		case h.SSH != "":
			return HostTarget{Name: canonical, IsSSH: true, SSHHost: h.SSH, DefaultCwd: h.Cwd}, nil
		default:
			return HostTarget{Name: canonical, TCPAddr: withPort(h.Addr), DefaultCwd: h.Cwd}, nil
		}
	}
	if comp := strings.ToLower(os.Getenv("COMPUTERNAME")); comp != "" && n == comp {
		return HostTarget{Name: "local", IsLocal: true}, nil
	}

	// Prefixo explícito ssh: ex: ssh:server ou ssh:usuario@ip
	if strings.HasPrefix(n, "ssh:") {
		return HostTarget{Name: name, IsSSH: true, SSHHost: strings.TrimPrefix(n, "ssh:")}, nil
	}

	// IP ou nome DNS direto, com ou sem porta
	if strings.Contains(n, ".") {
		return HostTarget{Name: name, TCPAddr: withPort(n)}, nil
	}

	valid := append([]string{"local"}, cfg.Remote(false)...)
	return HostTarget{}, fmt.Errorf("unknown host '%s' (valid: %s, ssh:<host> or an IP)", name, strings.Join(valid, ", "))
}

// IsDefaultName: o nome é vazio, 'local' ou o host padrão (os que um prefixo 'host:' no caminho pode substituir).
func IsDefaultName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "" || n == "local" || n == config.Get().Default()
}

func withPort(addr string) string {
	if strings.Contains(addr, ":") {
		return addr
	}
	return addr + ":" + config.DefaultPort
}
