package transport

import (
	"fmt"
	"strings"
)

type HostTarget struct {
	Name    string
	IsLocal bool
	TCPAddr string
}

var knownHosts = map[string]HostTarget{
	"local":      {Name: "local", IsLocal: true},
	"pc-elvis":   {Name: "pc-elvis", IsLocal: true},
	"windows":    {Name: "windows", IsLocal: true},
	"noteserver": {Name: "noteserver", TCPAddr: "192.168.31.142:9142"},
	"servidor":   {Name: "servidor", TCPAddr: "192.168.31.142:9142"},
	"ax6000":     {Name: "ax6000", TCPAddr: "192.168.31.1:9142"},
	"ax3000":     {Name: "ax3000", TCPAddr: "192.168.31.34:9142"},
}

func ResolveHost(name string) (HostTarget, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		n = "noteserver"
	}
	if target, ok := knownHosts[n]; ok {
		return target, nil
	}
	// Suporte a IP direto com ou sem porta
	if strings.Contains(n, ".") {
		if !strings.Contains(n, ":") {
			n += ":9142"
		}
		return HostTarget{Name: name, TCPAddr: n}, nil
	}
	return HostTarget{}, fmt.Errorf("host desconhecido: '%s' (hosts válidos: local, noteserver, ax6000, ax3000 ou IP)", name)
}
