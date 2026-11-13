package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstQualifiedMember(t *testing.T) {
	cases := map[string][2]string{
		`grep -rn "SelfLog.WriteLine" . --include=*.cs`:         {"SelfLog", "WriteLine"},
		`grep -rn "SelfLog\.WriteLine" .`:                       {"SelfLog", "WriteLine"},
		`findstr /s /n /c:"SelfLog.WriteLine" *.cs`:             {"SelfLog", "WriteLine"},
		`cd "W:/x/y" && grep -rn 'SelfLog.WriteLine(' . | head`: {"SelfLog", "WriteLine"},
		`grep -rn "SelfLog.WriteLine\|SelfLog\.Write" --include=*.cs .`: {"SelfLog", "WriteLine"},
		`sed -i 's/SelfLog\.WriteLine(/SelfLog.Write(/g' src/a.cs`:       {"SelfLog", "WriteLine"},
		`perl -pi -e 's/SelfLog\.WriteLine\(/SelfLog.Write(/g' src/a.cs`:  {"SelfLog", "WriteLine"},
	}
	for c, want := range cases {
		q, m, ok := firstQualifiedMember(c)
		if !ok || q != want[0] || m != want[1] {
			t.Errorf("%s: got %q.%q ok=%v", c, q, m, ok)
		}
	}
	for _, c := range []string{`grep -rn foo W:/Data/Serilog.Core/x.cs`, `grep -rn "SelfLog" .`, `grep -rn "a.b" .`} {
		if _, _, ok := firstQualifiedMember(c); ok {
			t.Errorf("not a member search: %s", c)
		}
	}
}

// O homônimo (NullWriter.WriteLine) não confunde: o qualificador acha o arquivo certo e a chamada vem pronta.
func TestMemberRenamePlanPicksOwnerByQualifier(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"src/Debugging/SelfLog.cs":   "namespace S\n{\n    public static class SelfLog\n    {\n        public static void WriteLine(string format)\n        {\n        }\n    }\n}\n",
		"test/Support/NullWriter.cs": "namespace S\n{\n    public class NullWriter\n    {\n        public void WriteLine(string v)\n        {\n        }\n    }\n}\n",
		"src/Use.cs":                 "namespace S\n{\n    class U\n    {\n        void M() { SelfLog.WriteLine(\"x\"); }\n    }\n}\n",
	})
	plan := memberRenamePlan(repo, `grep -rn "SelfLog.WriteLine" . --include=*.cs`)
	if !strings.Contains(plan, "SelfLog.WriteLine is declared in src/Debugging/SelfLog.cs:5") ||
		!strings.Contains(plan, "symbol='WriteLine' replacement='<NewName>'") || !strings.Contains(plan, "SelfLog.cs'") {
		t.Fatalf("plan wrong:\n%s", plan)
	}
	if got := memberRenamePlan(repo, `grep -rn "Nothing.WriteLine" .`); got != "" {
		t.Fatalf("unknown owner must fall back to the generic hint, got:\n%s", got)
	}
}

// Pasta sem .git (o workspace do bench, um download): o plano pronto também vem, pela indexação avulsa.
func TestMemberRenamePlanWorksWithoutGit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"src/Debugging/SelfLog.cs":   "namespace S\n{\n    public static class SelfLog\n    {\n        public static void WriteLine(string format)\n        {\n        }\n    }\n}\n",
		"test/Support/NullWriter.cs": "namespace S\n{\n    public class NullWriter\n    {\n        public void WriteLine(string v)\n        {\n        }\n    }\n}\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := memberRenamePlan(dir, `cd `+filepath.ToSlash(dir)+` && grep -rn "SelfLog.WriteLine" --include=*.cs .`)
	if !strings.Contains(plan, "SelfLog.WriteLine is declared in src/Debugging/SelfLog.cs:5") {
		t.Fatalf("plan without git wrong:\n%s", plan)
	}
}
