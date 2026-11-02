package transport

import (
	"fmt"
	"os"
	"strings"
)

type HostTarget struct {
	Name       string
	IsLocal    bool
	IsSSH      bool
	SSHHost    string
	TCPAddr    string
	DefaultCwd string
}

var knownHosts = map[string]HostTarget{
	"local":           {Name: "local", IsLocal: true},
	"pc-elvis":        {Name: "pc-elvis", IsLocal: true},
	"localhost":       {Name: "local", IsLocal: true},
	"127.0.0.1":       {Name: "local", IsLocal: true},
	"windows":         {Name: "windows", IsLocal: true},
	"noteserver":      {Name: "noteserver", TCPAddr: "192.168.31.142:9142", DefaultCwd: "/home/elvis/workspace"},
	"servidor":        {Name: "servidor", TCPAddr: "192.168.31.142:9142", DefaultCwd: "/home/elvis/workspace"},
	"ax6000":          {Name: "ax6000", TCPAddr: "192.168.31.1:9142"},
	"ax3000":          {Name: "ax3000", TCPAddr: "192.168.31.34:9142"},
	"firestick-suite": {Name: "firestick-suite", TCPAddr: "192.168.31.37:9142"},
	"firestick-sala":  {Name: "firestick-sala", TCPAddr: "192.168.31.21:9142"},
	// Hosts de trabalho mapeados para SSH BatchMode transparente
	"vm-ia-core": {Name: "vm-ia-core", IsSSH: true, SSHHost: "vm-ia-core"},
	"vm-obs-dev": {Name: "vm-obs-dev", IsSSH: true, SSHHost: "vm-obs-dev"},
	"vm-ia-mssp": {Name: "vm-ia-mssp", IsSSH: true, SSHHost: "vm-ia-mssp"},
}

func ResolveHost(name string) (HostTarget, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		n = "noteserver"
	}
	if target, ok := knownHosts[n]; ok {
		return target, nil
	}
	if comp := strings.ToLower(os.Getenv("COMPUTERNAME")); comp != "" && n == comp {
		return HostTarget{Name: "local", IsLocal: true}, nil
	}

	// Prefixo explícito ssh: ex: ssh:servidor ou ssh:usuario@ip
	if strings.HasPrefix(n, "ssh:") {
		sshHost := strings.TrimPrefix(n, "ssh:")
		return HostTarget{Name: name, IsSSH: true, SSHHost: sshHost}, nil
	}

	// Suporte a IP direto com ou sem porta
	if strings.Contains(n, ".") {
		if !strings.Contains(n, ":") {
			n += ":9142"
		}
		return HostTarget{Name: name, TCPAddr: n}, nil
	}

	return HostTarget{}, fmt.Errorf("host desconhecido: '%s' (hosts válidos: local, noteserver, ax6000, ax3000, vm-ia-core, prefixo ssh:<host> ou IP direto)", name)
}
