//go:build windows

package transport

import (
	"strings"
	"testing"
)

// Bug 4: script em stdin com try/catch e tipo genérico (System.Collections.Generic.List) saía com exit 0 e sem saída.
// Reproduz o caso de uso real: argv pwsh -NoProfile -Command - com o script em input.
const scriptComTryEList = `try {
  $l = New-Object System.Collections.Generic.List[string]
  $l.Add("ola")
  $l
} catch { "ERRO: $_" }
`

func TestPwshStdinComTryNaoPerdeSaida(t *testing.T) {
	for _, shell := range []string{"pwsh", "powershell"} {
		res, err := ExecLocalArgv([]string{shell, "-NoProfile", "-Command", "-"}, "", scriptComTryEList, 30000000000)
		if err != nil {
			t.Fatalf("%s: erro: %v", shell, err)
		}
		if strings.TrimSpace(res.Stdout) != "ola" {
			t.Errorf("%s: saída perdida: stdout=%q stderr=%q exit=%d", shell, res.Stdout, res.Stderr, res.ExitCode)
		}
	}
}
