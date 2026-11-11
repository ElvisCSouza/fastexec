package tools

import "testing"

func TestIsFileAggregateAndPythonMissing(t *testing.T) {
	cases := map[string]bool{
		`powershell -c "Get-Content app.log | ?{$_ -match 'ERROR'} | group | sort Count -desc"`: true,
		"cat app.log | grep ERROR | sort | uniq -c":                                             true,
		`pwsh -c "Select-String -Path app.log -Pattern ERROR | Measure-Object"`:                 true,
		"cat app.log": false,
		"dir | sort":  false,
	}
	for c, want := range cases {
		if got := isFileAggregate(c); got != want {
			t.Errorf("isFileAggregate(%q) = %v, quer %v", c, got, want)
		}
	}
	if !isPythonMissing("[exit 9009] Python was not found; run without arguments to install") || isPythonMissing("ok") {
		t.Error("isPythonMissing")
	}
}
