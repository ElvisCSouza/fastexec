package transport

import "testing"

func TestPwshStdinInputTerminaComLinhaEmBranco(t *testing.T) {
	casos := [][]string{
		{"pwsh", "-NoProfile", "-Command", "-"},
		{"powershell.exe", "-NoProfile", "-c", "-"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "-command", "-"},
	}
	for _, argv := range casos {
		got := pwshStdinInput(argv, "try {\n 'a'\n} catch { 'b' }\r\n")
		want := "try {\n 'a'\n} catch { 'b' }\n\n"
		if got != want {
			t.Errorf("%v: input = %q, quer %q", argv, got, want)
		}
	}
}

func TestPwshStdinInputNaoMexeNosOutrosCasos(t *testing.T) {
	casos := [][]string{
		{"pwsh", "-NoProfile", "-File", "x.ps1"},
		{"pwsh", "-NoProfile", "-Command", "Get-Date"},
		{"node", "-"},
		{"python", "-c", "-"},
		{"pwsh"},
	}
	for _, argv := range casos {
		if got := pwshStdinInput(argv, "algo"); got != "algo" {
			t.Errorf("%v: input alterado para %q", argv, got)
		}
	}
	if got := pwshStdinInput([]string{"pwsh", "-Command", "-"}, ""); got != "" {
		t.Errorf("input vazio virou %q", got)
	}
}
