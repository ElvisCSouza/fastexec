package tools

import (
	"runtime"
	"strings"
	"testing"
)

func TestIsBroadRoot(t *testing.T) {
	sim := []string{`C:\`, `C:`, `c:/`, `D:\`, `C:\Users`, `C:\Users\`, `"C:\Users"`, `C:\*.go`, `/`, `/home`, `/home/`, `/Users`, `/root`}
	nao := []string{`C:\Users\user`, `D:\repos`, `C:\Workspace`, `/home/user/workspace`, `.`, ``, `src`, `*.go`, `/etc`}
	for _, p := range sim {
		if !isBroadRoot(p, true) {
			t.Errorf("deveria ser raiz ampla: %q", p)
		}
	}
	for _, p := range nao {
		if isBroadRoot(p, true) {
			t.Errorf("não é raiz ampla: %q", p)
		}
	}
	// host Linux: letra de unidade é nome de pasta comum; as raízes Unix continuam amplas
	for _, p := range []string{`W:`, `W:/`, `C:\`, `C:\Users`} {
		if isBroadRoot(p, false) {
			t.Errorf("no host Linux %q não é raiz ampla", p)
		}
	}
	if !isBroadRoot("/home", false) {
		t.Error("/home é ampla no host Linux")
	}
}

func TestBroadScanBloqueia(t *testing.T) {
	win := []ExecArgs{
		{Command: `grep -rn password C:\`},
		{Command: `grep -rn foo .`, Cwd: `C:\`},
		{Command: `rg secret C:\Users`},
		{Command: `findstr /s /i token C:\*.json`},
		{Command: `dir /s /b C:\`},
		{Command: `Get-ChildItem -Path C:\ -Recurse -Filter *.log`},
		{Command: `tree C:\`},
	}
	unix := []ExecArgs{
		{Command: `grep -r foo /`},
		{Command: `grep -r foo`, Cwd: `/home`},
		{Command: `find / -name "*.go"`},
		{Command: `ls -R /home`},
		{Command: `cat x | grep -r foo /`},
		{Argv: []string{"rg", "-n", "foo", "/"}},
	}
	for _, a := range win {
		if root := broadScanOn(a, true); root == "" {
			t.Errorf("deveria bloquear no Windows: %+v", a)
		}
	}
	for _, a := range unix {
		if root := broadScanOn(a, false); root == "" {
			t.Errorf("deveria bloquear no Linux: %+v", a)
		}
	}
}

func TestBroadScanDeixaPassar(t *testing.T) {
	passa := []ExecArgs{
		{Command: `grep -rn foo .`, Cwd: `D:\repos\fastexec`},
		{Command: `grep -n foo C:\Windows\win.ini`},
		{Command: `rg foo src`},
		{Command: `find . -name "*.go"`, Cwd: `/home/user/workspace`},
		{Command: `findstr /i token C:\x.json`},
		{Command: `dir C:\`},
		{Command: `ls -la /`},
		{Command: `Get-ChildItem C:\`},
		{Command: `git grep -n foo`},
	}
	for _, a := range passa {
		if root := broadScanOn(a, true); root != "" {
			t.Errorf("não deveria bloquear (%s): %+v", root, a)
		}
	}
	// host Linux: uma pasta chamada 'W:' (criada por engano por um caminho Windows) pode ser listada e removida
	linux := []ExecArgs{
		{Command: `ls -R W:`},
		{Command: `find W: -type f`},
		{Command: `grep -rn foo W:/ReposHomeLab`},
	}
	for _, a := range linux {
		if root := broadScanOn(a, false); root != "" {
			t.Errorf("no host Linux não deveria bloquear (%s): %+v", root, a)
		}
	}
	t.Setenv("FASTEXEC_NO_SEARCH_GUARD", "1")
	if broadScanOn(ExecArgs{Command: `grep -r foo /`}, false) != "" {
		t.Error("guard desligado não deveria bloquear")
	}
}

func TestBroadScanPeloExec(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("raiz de disco só existe no host local Windows")
	}
	_, err := HandleExec(ExecArgs{Host: "local", Command: `grep -rn password C:\`})
	if err == nil || !strings.Contains(err.Error(), "[FastExec Guard] Recursive scan of 'C:\\' blocked") {
		t.Errorf("exec deveria recusar antes de executar: %v", err)
	}
}
