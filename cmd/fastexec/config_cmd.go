package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/transport"
)

const configUsage = `Usage: fastexec config [show|example|init]
  show     effective configuration: file, hosts, default host, data folders, telemetry (default)
  example  print a sample config.json
  init     write the sample to the config file if it does not exist yet`

// cmdConfig atende: config.
func cmdConfig(subArgs []string) {
	action := "show"
	if len(subArgs) > 0 {
		action = subArgs[0]
	}
	switch action {
	case "show":
		fmt.Print(renderConfig(config.Get()))
	case "example":
		fmt.Print(config.SampleJSON)
	case "init":
		p := config.FilePath()
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintf(os.Stderr, "%s already exists; edit it or remove it first\n", p)
			os.Exit(1)
		}
		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err == nil {
			err = os.WriteFile(p, []byte(config.SampleJSON), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (sample hosts: edit before use)\n", p)
	default:
		fmt.Println(configUsage)
		os.Exit(1)
	}
}

func renderConfig(c *config.Config) string {
	var b strings.Builder
	file := c.Path()
	if file == "" {
		file = config.FilePath() + " (not found: defaults, host 'local' only)"
	}
	fmt.Fprintf(&b, "Config file:  %s\n", file)
	if err := c.Err(); err != nil {
		fmt.Fprintf(&b, "ERROR:        %v (running with defaults)\n", err)
	}
	fmt.Fprintf(&b, "Default host: %s\n", c.Default())
	fmt.Fprintf(&b, "Data folder:  %s\n", c.Data())
	fmt.Fprintf(&b, "Handoffs:     %s\n", c.Handoff())
	tel := "on"
	if !c.TelemetryOn() {
		tel = "off"
	}
	fmt.Fprintf(&b, "Telemetry:    %s (%s)\n", tel, filepath.Join(c.Data(), "telemetry"))
	b.WriteString("Hosts:\n  local\n")
	for _, name := range c.Remote(false) {
		t, err := transport.ResolveHost(name)
		if err != nil {
			fmt.Fprintf(&b, "  %s: %v\n", name, err)
			continue
		}
		_, h, _ := c.Lookup(name)
		where := t.TCPAddr
		if t.IsSSH {
			where = "ssh " + t.SSHHost
		}
		extra := ""
		if t.DefaultCwd != "" {
			extra += " cwd=" + t.DefaultCwd
		}
		if len(h.Aliases) > 0 {
			extra += " aliases=" + strings.Join(h.Aliases, ",")
		}
		if h.Hidden {
			extra += " hidden"
		}
		fmt.Fprintf(&b, "  %s: %s%s\n", name, where, extra)
	}
	return b.String()
}
