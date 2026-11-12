package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseReadScript(t *testing.T) {
	join := func(dir, p string) string {
		if dir == "" || isAbsAny(p) {
			return p
		}
		return dir + "/" + p
	}
	cases := []struct {
		script, shell string
		want          []string // "R:path" ou "L:literal"
	}{
		{`Get-Content a.go -Raw; '=====SCHEMAS'; Get-Content b.go`, "pwsh", []string{"R:/w/a.go", "L:=====SCHEMAS", "R:/w/b.go"}},
		{`cd ~/workspace; cat ops/x.zig; echo ----; cat y.zig`, "sh", []string{"R:~/workspace/ops/x.zig", "L:----", "R:~/workspace/y.zig"}},
		{`type a.go & echo --- & type b.go`, "cmd", []string{"R:/w/a.go", "L:---", "R:/w/b.go"}},
		{`Set-Location sub; Get-Content -Path main.go -Encoding utf8`, "pwsh", []string{"R:/w/sub/main.go"}},
		{`cd /d W:\repo && type x.go`, "cmd", []string{"R:W:\\repo/x.go"}},
	}
	for _, c := range cases {
		steps, ok := parseReadScript(c.script, c.shell, join, "/w")
		if !ok {
			t.Errorf("%q: deveria virar leituras", c.script)
			continue
		}
		var got []string
		for _, s := range steps {
			if s.path != "" {
				got = append(got, "R:"+s.path)
			} else {
				got = append(got, "L:"+s.literal)
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q:\n got %v\nwant %v", c.script, got, c.want)
		}
	}
	// tudo que não é só leitura inteira segue para o shell
	shell := []struct{ script, shell string }{
		{`$l=Get-Content a.go; $l[10..40]`, "pwsh"},
		{`Get-Content a.go | Select -First 20`, "pwsh"},
		{`Get-Content a.go -TotalCount 40`, "pwsh"},
		{`cat a.go > b.go`, "sh"},
		{`cat a.go; rm b.go`, "sh"},
		{`sed -n 1,40p a.go`, "sh"},
		{`cat *.go`, "sh"},
		{`cat -n a.go`, "sh"},
		{`echo oi; echo tchau`, "sh"},
		{`cd x`, "sh"},
		{`cat a.go & cat b.go`, "sh"},
		{`& 'C:\x.exe'; Get-Content a`, "pwsh"},
		{`type %TEMP%\a.txt`, "cmd"},
		{`(Get-Content a.go).Count`, "pwsh"},
	}
	for _, c := range shell {
		if _, ok := parseReadScript(c.script, c.shell, join, "/w"); ok {
			t.Errorf("%q (%s) não é só leitura e deveria ir para o shell", c.script, c.shell)
		}
	}
}

func TestUnwrapShell(t *testing.T) {
	cases := []struct {
		args         ExecArgs
		local        bool
		script, kind string
	}{
		{ExecArgs{Command: `pwsh -NoProfile -Command "Get-Content a -Raw; 'x'"`}, true, `Get-Content a -Raw; 'x'`, "pwsh"},
		{ExecArgs{Command: `powershell -NoProfile -ExecutionPolicy Bypass -Command Get-Content a`}, true, `Get-Content a`, "pwsh"},
		{ExecArgs{Command: `bash -c 'cd ~/workspace; cat x'`}, false, `cd ~/workspace; cat x`, "sh"},
		{ExecArgs{Argv: []string{"pwsh", "-NoProfile", "-Command", "Get-Content a"}}, true, "Get-Content a", "pwsh"},
		{ExecArgs{Argv: []string{"bash", "-c", "cat x; cat y"}}, false, "cat x; cat y", "sh"},
		{ExecArgs{Command: `type a & type b`}, true, `type a & type b`, "cmd"},
		{ExecArgs{Command: `cat a; cat b`}, false, `cat a; cat b`, "sh"},
	}
	for _, c := range cases {
		s, k := unwrapShell(c.args, c.local)
		if s != c.script || k != c.kind {
			t.Errorf("%+v: got (%q, %s), want (%q, %s)", c.args, s, k, c.script, c.kind)
		}
	}
}

// Ponta a ponta no host local: duas leituras embrulhadas, uma delas fonte grande -> índice; repetir o mesmo
// comando devolve o texto completo; o arquivo pequeno vem inteiro.
func TestExecWrappedReadUsesGraph(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.go")
	var b strings.Builder
	b.WriteString("package x\n\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "func F%d(x int) int {\n", i)
		for j := 0; j < 30; j++ {
			fmt.Fprintf(&b, "\tx += %d // ajusta o total corrente no passo %d do cálculo de teste\n", j, j)
		}
		b.WriteString("\treturn x\n}\n\n")
	}
	if err := os.WriteFile(big, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(dir, "small.txt")
	_ = os.WriteFile(small, []byte("conteudo pequeno\n"), 0o644)

	graphSeenMu.Lock()
	graphSeen = map[string]time.Time{}
	graphSeenMu.Unlock()

	args := ExecArgs{Host: "local", Argv: []string{"pwsh", "-NoProfile", "-Command", fmt.Sprintf("Get-Content '%s' -Raw; '=====PEQ'; Get-Content '%s'", big, small)}}
	out, handled := tryRemediateReadScript(args)
	if !handled {
		t.Fatal("deveria ser atendido pelo read_file")
	}
	if !strings.Contains(out, GraphMarkOutline) || strings.Contains(out, "x += 29") || !strings.Contains(out, "=====PEQ\nconteudo pequeno") {
		t.Fatalf("esperava índice do grande, separador e o pequeno inteiro:\n%s", out)
	}
	if !strings.Contains(out, "repeat this same command") {
		t.Fatal("o cabeçalho deve dizer como obter o texto completo")
	}
	full, _ := tryRemediateReadScript(args)
	if !strings.Contains(full, "x += 29") {
		t.Fatal("repetir o mesmo comando deve trazer o texto completo")
	}
	// arquivo inexistente: o shell mostra o erro original
	if _, handled := tryRemediateReadScript(ExecArgs{Host: "local", Argv: []string{"pwsh", "-NoProfile", "-Command", "Get-Content '" + filepath.Join(dir, "nao-existe.go") + "'; 'x'"}}); handled {
		t.Fatal("arquivo inexistente deve seguir para o shell")
	}
}
