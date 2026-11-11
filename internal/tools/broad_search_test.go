package tools

import "testing"

func TestIsBroadSearch(t *testing.T) {
	cases := map[string]bool{
		`powershell -c "Get-ChildItem -Recurse -Include *.cs . | Select-String -Pattern 'fee|taxa|rate' | Select-Object -First 40"`: true,
		"grep -rniE 'fee|taxa' src": true,
		"rg -i 'fee|taxa'":          true,
		"findstr /s /i /c:fee *.cs": true,
		"git grep -n 'fee|taxa'":    true,
		"grep -n foo file.go":       false, // um arquivo só: não é busca ampla
		"ls -R":                     false,
		"grep -rn 'func Foo' .":     false, // declaração: tem dica própria
		"grep -rn 'Calcular(' .":    false, // chamada: tem dica própria
	}
	for c, want := range cases {
		if got := isBroadSearch(c); got != want {
			t.Errorf("isBroadSearch(%q) = %v, quer %v", c, got, want)
		}
	}
	if nonEmptyLines("a\n\n b \n") != 2 {
		t.Error("nonEmptyLines")
	}
}
