package transport

// legacy_line.go: monta a linha de comando do caminho antigo (daemon sem SX, sem argv/stdin como campos).
// Servidor Windows antigo executa a linha no PowerShell: sem "export", sem "&&", sem printf|base64.
// Servidor POSIX segue com o prefixo export, "cd ... &&" e printf|base64.

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

var winCwdRe = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\)`)

// IsWindowsCwd diz se o cwd é caminho Windows (letra de unidade ou UNC).
func IsWindowsCwd(cwd string) bool { return winCwdRe.MatchString(cwd) }

func buildLegacyLine(cmd, cwd, input string) string {
	if IsWindowsCwd(cwd) {
		return buildLegacyLinePowerShell(cmd, cwd, input)
	}
	line := cmd
	if input != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(input))
		line = fmt.Sprintf("printf '%%s' '%s' | base64 -d | %s", b64, cmd)
	}
	if cwd != "" {
		line = "cd \"" + cwd + "\" && " + line
	}
	return "export CI=1 DEBIAN_FRONTEND=noninteractive PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb NO_COLOR=1; " + line
}

func buildLegacyLinePowerShell(cmd, cwd, input string) string {
	line := cmd
	if input != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(input))
		line = fmt.Sprintf("[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')) | %s", b64, cmd)
	}
	q := strings.ReplaceAll(cwd, "'", "''")
	return "$env:CI='1'; $env:GIT_PAGER='cat'; $env:GIT_TERMINAL_PROMPT='0'; $env:NO_COLOR='1'; " +
		"Set-Location -LiteralPath '" + q + "'; " + line
}
