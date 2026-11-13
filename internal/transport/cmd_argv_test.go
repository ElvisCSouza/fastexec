package transport

import "testing"

func TestCmdCPayload(t *testing.T) {
	cases := []struct {
		argv []string
		want string
		ok   bool
	}{
		{[]string{"cmd", "/c", `findstr /S /N /C:"SelfLog.Write" *.cs`}, `findstr /S /N /C:"SelfLog.Write" *.cs`, true},
		{[]string{`C:\Windows\System32\cmd.exe`, "/C", `echo "a b"`}, `echo "a b"`, true},
		{[]string{"cmd.exe", "/s", "/c", `dir "x y"`}, `dir "x y"`, true},
		{[]string{"cmd", "/c", "dir /b"}, "", false},          // sem aspas: nada a corrigir
		{[]string{"cmd", "/c", "echo", `"a"`}, "", false},     // já separado em argumentos: o autor escolheu
		{[]string{"pwsh", "-Command", `echo "a"`}, "", false}, // outro programa
		{[]string{"cmd", "/k", `echo "a"`}, "", false},        // só /c
		{nil, "", false},
	}
	for _, c := range cases {
		got, ok := cmdCPayload(c.argv)
		if ok != c.ok || got != c.want {
			t.Errorf("cmdCPayload(%q) = %q,%v; want %q,%v", c.argv, got, ok, c.want, c.ok)
		}
	}
}
