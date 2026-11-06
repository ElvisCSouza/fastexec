package tools

import (
	"strings"
	"testing"
)

func TestIsBroadRoot(t *testing.T) {
	sim := []string{`C:\`, `C:`, `c:/`, `D:\`, `C:\Users`, `C:\Users\`, `"C:\Users"`, `C:\*.go`, `/`, `/home`, `/home/`, `/Users`, `/root`}
	nao := []string{`C:\Users\elvis`, `W:\ReposHomeLab`, `C:\Workspace`, `/home/elvis/workspace`, `.`, ``, `src`, `*.go`, `/etc`}
	for _, p := range sim {
		if !isBroadRoot(p) {
			t.Errorf("deveria ser raiz ampla: %q", p)
		}
	}
	for _, p := range nao {
		if isBroadRoot(p) {
			t.Errorf("não é raiz ampla: %q", p)
		}
	}
}

func TestBroadScanBloqueia(t *testing.T) {
	bloqueia := []ExecArgs{
		{Command: `grep -rn password C:\`},
		{Command: `grep -r foo /`},
		{Command: `grep -rn foo .`, Cwd: `C:\`},
		{Command: `grep -r foo`, Cwd: `/home`},
		{Command: `rg secret C:\Users`},
		{Command: `find / -name "*.go"`},
		{Command: `findstr /s /i token C:\*.json`},
		{Command: `dir /s /b C:\`},
		{Command: `ls -R /home`},
		{Command: `Get-ChildItem -Path C:\ -Recurse -Filter *.log`},
		{Command: `cat x | grep -r foo /`},
		{Argv: []string{"rg", "-n", "foo", "/"}},
		{Command: `tree C:\`},
	}
	for _, a := range bloqueia {
		if root := broadScan(a); root == "" {
			t.Errorf("deveria bloquear: %+v", a)
		}
	}
}

func TestBroadScanDeixaPassar(t *testing.T) {
	passa := []ExecArgs{
		{Command: `grep -rn foo .`, Cwd: `W:\ReposHomeLab\fastexec`},
		{Command: `grep -n foo C:\Windows\win.ini`},
		{Command: `rg foo src`},
		{Command: `find . -name "*.go"`, Cwd: `/home/elvis/workspace`},
		{Command: `findstr /i token C:\x.json`},
		{Command: `dir C:\`},
		{Command: `ls -la /`},
		{Command: `Get-ChildItem C:\`},
		{Command: `git grep -n foo`},
	}
	for _, a := range passa {
		if root := broadScan(a); root != "" {
			t.Errorf("não deveria bloquear (%s): %+v", root, a)
		}
	}
	t.Setenv("FASTEXEC_NO_SEARCH_GUARD", "1")
	if broadScan(ExecArgs{Command: `grep -r foo /`}) != "" {
		t.Error("guard desligado não deveria bloquear")
	}
}

func TestBroadScanPeloExec(t *testing.T) {
	_, err := HandleExec(ExecArgs{Host: "local", Command: `grep -rn password C:\`})
	if err == nil || !strings.Contains(err.Error(), "[FastExec Guard] Recursive scan of 'C:\\' blocked") {
		t.Errorf("exec deveria recusar antes de executar: %v", err)
	}
}
