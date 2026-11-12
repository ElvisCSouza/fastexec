package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/lsp"
	"homelab/fastexec/internal/transport"
)

const configUsage = `Usage: fastexec config [show|example|init|set]
  show     effective configuration: file, hosts, default host, data folders, telemetry (default)
  example  print a sample config.json
  init     write the sample to the config file if it does not exist yet
  set <key> <value>
           write one setting into the config file, leaving the rest untouched
           keys: dotnet_sdk (folder of a .NET SDK for the C# server; "-" clears it)`

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
	case "set":
		configSet(subArgs[1:])
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

// configSet writes one setting into the config file and leaves everything else
// as it was. The file is re-read as raw JSON rather than through config.Load:
// Load refuses a file with a key it does not know, and a `set` that quietly
// dropped someone's hand-written key (or their typo) would be a data loss.
//
// Nothing is downloaded and nothing is started: pointing at a folder that is not
// yet an SDK is allowed and warned about, because unpacking the SDK later is a
// normal order of events and the doctor already reports the folder that does not
// work.
func configSet(args []string) {
	if len(args) != 2 {
		fmt.Println(configUsage)
		os.Exit(1)
	}
	key, value := args[0], args[1]
	if key != "dotnet_sdk" {
		fmt.Fprintf(os.Stderr, "unknown setting %q: the only setting this writes is dotnet_sdk\n\n%s\n", key, configUsage)
		os.Exit(1)
	}
	path := config.FilePath()
	stored, cleared, err := writeConfigSetting(path, key, value)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cleared {
		fmt.Printf("cleared %s in %s\n", key, path)
		return
	}
	fmt.Printf("set %s = %s in %s\n", key, stored, path)
	if !lsp.HasDotnetSDK(stored) {
		fmt.Fprintf(os.Stderr, "warning: %s has no dotnet executable with an sdk/ folder; `fastexec lsp doctor <repo>` will report it until it does\n", stored)
	}
}

// writeConfigSetting puts one key into the file at path and reports the value it
// stored ("" when it removed the key instead). It is separate from configSet so a
// test can check what lands on disk without a process: the file is the promise
// here, and the messages around it are not.
func writeConfigSetting(path, key, value string) (stored string, cleared bool, err error) {
	doc := map[string]any{}
	if data, rerr := os.ReadFile(path); rerr == nil {
		if uerr := json.Unmarshal(data, &doc); uerr != nil {
			return "", false, fmt.Errorf("%s is not valid JSON: %v (nothing was written; fix it by hand)", path, uerr)
		}
	} else if !os.IsNotExist(rerr) {
		return "", false, rerr
	}
	cleared = value == "-" || strings.TrimSpace(value) == ""
	if cleared {
		delete(doc, key)
	} else {
		stored = filepath.Clean(value)
		doc[key] = stored
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", cleared, err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", cleared, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", cleared, err
	}
	return stored, cleared, nil
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
	if sdk := c.DotnetSDKPath(); sdk != "" {
		state := "ok"
		if !lsp.HasDotnetSDK(sdk) {
			state = "no SDK there (dotnet executable with an sdk/ folder)"
		}
		fmt.Fprintf(&b, "dotnet_sdk:   %s (%s)\n", sdk, state)
	}
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
		if h.Token != "" {
			extra += " token=set"
		}
		fmt.Fprintf(&b, "  %s: %s%s\n", name, where, extra)
	}
	return b.String()
}
