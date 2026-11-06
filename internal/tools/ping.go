package tools

import (
	"fmt"
	"net"
	"strings"
	"time"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/transport"
)

type PingArgs struct {
	Host string `json:"host,omitempty"`
}

func pingHost(target transport.HostTarget) string {
	if target.IsLocal {
		start := time.Now()
		_, err := transport.ExecLocal("echo ok", 2*time.Second)
		lat := time.Since(start).Milliseconds()
		if err != nil {
			return fmt.Sprintf("%-12s : FAILED (%v)", target.Name, err)
		}
		return fmt.Sprintf("%-12s : OK (%dms - local)", target.Name, lat)
	}
	if target.IsSSH {
		return fmt.Sprintf("%-12s : SSH (%s, not checked)", target.Name, target.SSHHost)
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", target.TCPAddr, 2*time.Second)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return fmt.Sprintf("%-12s : OFFLINE (%s - %v)", target.Name, target.TCPAddr, err)
	}
	defer conn.Close()
	return fmt.Sprintf("%-12s : ONLINE (%dms - TCP daemon :9142)", target.Name, lat)
}

func HandlePing(args PingArgs) (string, error) {
	if args.Host != "" && args.Host != "all" {
		target, err := transport.ResolveHost(args.Host)
		if err != nil {
			return "", err
		}
		return pingHost(target), nil
	}

	targets := append([]string{"local"}, config.Get().Remote(true)...)
	var results []string
	for _, name := range targets {
		t, _ := transport.ResolveHost(name)
		results = append(results, pingHost(t))
	}

	return strings.Join(results, "\n"), nil
}
