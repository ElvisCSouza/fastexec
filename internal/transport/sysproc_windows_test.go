//go:build windows

package transport

import (
	"strings"
	"testing"
	"time"
)

func TestPsInlineEscapes(t *testing.T) {
	yes := []string{
		`powershell -NoProfile -Command "\"a | b\""`,
		`  pwsh -c "\"x\""`,
		`PowerShell.exe -Command "\"x\""`,
	}
	no := []string{
		`powershell -NoProfile -Command "Write-Output ok"`, // sem \" : segue pelo cmd como sempre
		`echo \"a\" | findstr a`,                           // não é PowerShell
		`powershellx -Command "\"x\""`,                     // outro programa
		`dir & echo "\"x\""`,
	}
	for _, s := range yes {
		if !psInlineEscapes(s) {
			t.Errorf("deveria ir direto ao PowerShell: %s", s)
		}
	}
	for _, s := range no {
		if psInlineEscapes(s) {
			t.Errorf("deveria seguir pelo cmd: %s", s)
		}
	}
}

// O caso real do T8: ' | ' dentro de \"...\" virava pipe do cmd.exe e a chamada falhava sem mensagem.
func TestExecPowerShellComAspasEscapadasEPipe(t *testing.T) {
	line := `powershell -NoProfile -Command "'um','dois','um' | group | sort Count -desc | %{ \"$($_.Count) | $($_.Name)\" }; 'FIM'"`
	res, err := runLocal(localSpec{cmdStr: line}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "2 | um") || !strings.Contains(res.Stdout, "1 | dois") || !strings.Contains(res.Stdout, "FIM") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// Linhas sem \" continuam pelo cmd.exe: operadores do cmd e %VAR% seguem funcionando.
func TestExecCmdContinuaIntacto(t *testing.T) {
	res, err := runLocal(localSpec{cmdStr: `echo um & echo dois`}, 20*time.Second)
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "um") || !strings.Contains(res.Stdout, "dois") {
		t.Fatalf("cmd: %v %+v", err, res)
	}
	res, err = runLocal(localSpec{cmdStr: `powershell -NoProfile -Command "Write-Output ok"`}, 20*time.Second)
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "ok") {
		t.Fatalf("powershell simples: %v %+v", err, res)
	}
}
