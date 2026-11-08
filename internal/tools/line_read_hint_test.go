package tools

import "testing"

func TestIsLineSliceRead(t *testing.T) {
	yes := []string{
		"Get-Content a.go | Select-Object -Skip 100 -First 40",
		"(Get-Content a.go)[10..40]",
		"sed -n '10,40p' a.go",
		"tail -n 50 server.log",
	}
	no := []string{
		"Get-Content a.go",
		"git log -n 5",
		"go test ./...",
		"Get-ChildItem | Select-Object -First 5",
	}
	for _, c := range yes {
		if !isLineSliceRead(c) {
			t.Errorf("should hint: %q", c)
		}
	}
	for _, c := range no {
		if isLineSliceRead(c) {
			t.Errorf("should not hint: %q", c)
		}
	}
}
