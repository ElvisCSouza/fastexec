package tools

import (
	"runtime"
	"strings"
	"testing"
)

// start com await devolve a saída no mesmo turno quando o job termina no prazo.
func TestJobStartAwaitFinishes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("job local: Windows")
	}
	t.Setenv("FASTEXEC_DATA_DIR", t.TempDir())
	out, err := HandleJob(JobArgs{Host: "local", Action: "start", Argv: []string{"cmd", "/c", "echo pronto-await"}, Await: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "finished") || !strings.Contains(out, "pronto-await") {
		t.Errorf("sem saída inline: %q", out)
	}
}
