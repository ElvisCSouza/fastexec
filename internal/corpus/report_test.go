package corpus

import (
	"strings"
	"testing"
)

func TestToolSeesThroughWrappers(t *testing.T) {
	cases := map[string]string{
		`git log --oneline -n 5`:                                              "git",
		`bash -lc "git status && go test ./..."`:                              "git",
		`bash -lc 'cd /w/repo && go build ./...'`:                             "go",
		`C:\Program Files\Git\bin\bash.exe -c "grep -rn foo ."`:               "grep",
		`powershell -NoProfile -Command "Get-ChildItem W:\Data | Out-String"`: "get-childitem",
		`pwsh -NoProfile -Command "$env:X='1'; fastexec version"`:             "fastexec",
		`powershell -NoProfile -ExecutionPolicy Bypass -File W:\a\b.ps1`:      "script",
		`cmd /c "dir /b & echo x"`:                                            "dir",
		`cd W:\ReposHomeLab\fastexec && git add -A`:                           "git",
		`FASTEXEC_X=1 go test ./...`:                                          "go",
		`set A=1& sqlite3 db.sqlite "select 1"`:                               "sqlite3",
		`C:\Users\me\bin\sqlite3.exe -readonly x.db "select 1"`:               "sqlite3",
		``: "other",
	}
	for in, want := range cases {
		if got := Tool(in); got != want {
			t.Errorf("Tool(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCategory(t *testing.T) {
	for tool, want := range map[string]string{"git": "git", "go": "build_test", "get-childitem": "listagem", "rg": "busca", "tail": "logs", "zzz": "other"} {
		if got := Category(tool); got != want {
			t.Errorf("Category(%q) = %q, want %q", tool, got, want)
		}
	}
	RegisterCategory("novo", "meu-tool")
	if Category("meu-tool") != "novo" {
		t.Error("a registered tool must be found")
	}
}

func TestSummarizeExcludesDedupesAndCounts(t *testing.T) {
	big := strings.Repeat("x", BigRaw)
	samples := []Sample{
		{Label: "git status", Raw: "a", RawBytes: 1},
		{Label: "git status", Raw: "a", RawBytes: 1}, // same command, same output: duplicate
		{Label: "git log", Raw: big, RawBytes: len(big), ExitCode: 1},
		{Label: `powershell -Command "Get-ChildItem W:\Data\corpus"`, Raw: "q", RawBytes: 1}, // about the corpus
		{Label: `bash -lc "go test ./..."`, Raw: "ok", RawBytes: 2},
	}
	s := Summarize(samples, 2, DefaultExcludes)
	if s.Total != 5 || s.Excluded != 1 || s.Duplicates != 1 || s.Used != 3 || s.Skipped != 2 {
		t.Fatalf("unexpected summary %+v", s)
	}
	var git *CategoryStat
	for i := range s.Categories {
		if s.Categories[i].Name == "git" {
			git = &s.Categories[i]
		}
	}
	if git == nil || git.N != 2 || git.Errors != 1 || git.Big != 1 {
		t.Fatalf("git stat wrong: %+v", git)
	}
	out := Render(s)
	for _, want := range []string{"faltam 197", "build_test", "Famílias sem nenhuma amostra: listagem, busca, logs"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}
