package transport

import (
	"path/filepath"
	"strings"
)

// pwshStdinInput garante que um script lido do stdin por "pwsh|powershell -Command -" seja executado inteiro.
// Nesse modo o PowerShell (7 e 5.1) só executa um bloco multilinha final (try/catch, foreach, if...) quando recebe uma
// linha em branco depois dele; sem ela, descarta o bloco e o que vier depois, com exit 0 e stdout vazio.
// Para os demais argv o input volta intacto.
func pwshStdinInput(argv []string, input string) string {
	if !isPwshStdinScript(argv) || input == "" {
		return input
	}
	return strings.TrimRight(input, "\r\n") + "\n\n"
}

// isPwshStdinScript reconhece "pwsh|powershell [...] -Command|-c -" (script no stdin).
func isPwshStdinScript(argv []string) bool {
	if len(argv) < 3 || !isPwshExe(argv[0]) {
		return false
	}
	for i := 1; i < len(argv)-1; i++ {
		a := strings.ToLower(argv[i])
		if a == "-command" || a == "-c" {
			return argv[i+1] == "-"
		}
	}
	return false
}

// isPwshExe reconhece pwsh e powershell pelo nome do executável, com ou sem caminho e extensão.
func isPwshExe(prog string) bool {
	base := strings.ToLower(filepath.Base(prog))
	base = strings.TrimSuffix(base, ".exe")
	return base == "pwsh" || base == "powershell"
}
