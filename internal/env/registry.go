package env

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type RegistryEnv struct {
	Machine map[string]string `json:"m"`
	User    map[string]string `json:"u"`
}

func ReadRegistry() (*RegistryEnv, error) {
	psExes := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "pwsh.exe"),
		filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
	}
	if os.Getenv("SystemRoot") == "" {
		psExes[1] = filepath.Join("C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}

	script := "[Console]::OutputEncoding=[Text.Encoding]::UTF8; @{ m=[Environment]::GetEnvironmentVariables('Machine'); u=[Environment]::GetEnvironmentVariables('User') } | ConvertTo-Json -Compress -Depth 3"

	var lastErr error
	for _, exe := range psExes {
		cmd := exec.Command(exe, "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.SysProcAttr = nil // usar defaults do Windows

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err != nil {
			lastErr = fmt.Errorf("exec %s: %w (%s)", exe, err, stderr.String())
			continue
		}

		var reg RegistryEnv
		if err := json.Unmarshal(stdout.Bytes(), &reg); err != nil {
			lastErr = fmt.Errorf("parse JSON: %w", err)
			continue
		}

		return &reg, nil
	}

	return nil, lastErr
}

// ExpandSZ simula REG_EXPAND_SZ: %VAR% é expandido com os valores do env
func ExpandSZ(s string, env map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return ""
	})
}
