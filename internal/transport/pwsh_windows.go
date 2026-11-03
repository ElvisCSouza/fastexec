//go:build windows

package transport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveShellArgv normaliza o executável em argv para preferir o PowerShell 7 (pwsh)
// com fallback transparente para Windows PowerShell (powershell.exe).
// Também resolve o caminho físico caso o alias virtualizado do WindowsApps falhe.
func ResolveShellArgv(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	prog := argv[0]
	lower := strings.ToLower(filepath.Base(prog))
	lower = strings.TrimSuffix(lower, ".exe")

	if lower == "powershell" || lower == "pwsh" {
		resolved := resolvePwsh()
		if resolved != "" {
			newArgv := make([]string, len(argv))
			copy(newArgv, argv)
			newArgv[0] = resolved
			return newArgv
		}
		// Se pwsh não estiver disponível e o comando pediu powershell, mantém original
		if lower == "powershell" {
			return argv
		}
		// Se pediu pwsh mas não há pwsh instalado, fallback gracioso para powershell 5.1
		newArgv := make([]string, len(argv))
		copy(newArgv, argv)
		newArgv[0] = "powershell.exe"
		return newArgv
	}

	return argv
}

// resolvePwsh busca o caminho mais estável do executável do PowerShell 7 (pwsh).
func resolvePwsh() string {
	// 1. Tenta achar via LookPath (se pwsh estiver no PATH ativo)
	if p, err := exec.LookPath("pwsh.exe"); err == nil && p != "" {
		return p
	}
	if p, err := exec.LookPath("pwsh"); err == nil && p != "" {
		return p
	}

	// 2. Program Files padrão (MSI / winget machine)
	progFiles := os.Getenv("ProgramFiles")
	if progFiles != "" {
		msiPath := filepath.Join(progFiles, "PowerShell", "7", "pwsh.exe")
		if _, err := os.Stat(msiPath); err == nil {
			return msiPath
		}
		// 3. Pacote da Store / Appx descompactado em WindowsApps
		matches, _ := filepath.Glob(filepath.Join(progFiles, "WindowsApps", "Microsoft.PowerShell_*_x64__*", "pwsh.exe"))
		if len(matches) > 0 {
			return matches[len(matches)-1]
		}
	}

	// 4. Execution alias do usuário em %LOCALAPPDATA%\Microsoft\WindowsApps
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp != "" {
		aliasPath := filepath.Join(localApp, "Microsoft", "WindowsApps", "pwsh.exe")
		if _, err := os.Stat(aliasPath); err == nil {
			return aliasPath
		}
	}

	return ""
}
