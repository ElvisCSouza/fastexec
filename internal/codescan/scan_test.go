package codescan

import (
	"strings"
	"testing"
)

func refsOf(refs []Ref, rel string) []string {
	var out []string
	for _, r := range refs {
		if r.Rel == rel {
			out = append(out, r.Target)
		}
	}
	return out
}

func TestChamadasAninhadasEStringsIgnoradas(t *testing.T) {
	src := "console.log(fmt(parse(x)));\nconst s = \"skip(this)\";\n/* hidden(1)\nalso(2) */\nconst t = `tpl ${a}\nnot(3)`;\nreal(4);\n"
	_, refs := Scan([]byte(src), LangFor("a.ts"))
	got := strings.Join(refsOf(refs, "calls"), ",")
	if got != "log,fmt,parse,real" {
		t.Errorf("calls = %s", got)
	}
}

func TestFimDeBlocoPorChavesEIndentacao(t *testing.T) {
	ts := "export class A {\n  run(): void {\n    if (x) { y(\"}\") }\n  }\n}\nexport type Id = string;\n"
	decls, _ := Scan([]byte(ts), LangFor("a.ts"))
	want := map[string][2]int{"A": {1, 5}, "run": {2, 4}, "Id": {6, 6}}
	for _, d := range decls {
		if w, ok := want[d.Name]; ok && (d.Line != w[0] || d.EndLine != w[1]) {
			t.Errorf("%s: %d-%d, quer %d-%d", d.Name, d.Line, d.EndLine, w[0], w[1])
		}
	}

	py := "class Repo:\n    \"\"\"\n    def fake(self):\n    \"\"\"\n    def get(self):\n        return 1\n\ndef top():\n    pass\n"
	decls, _ = Scan([]byte(py), LangFor("m.py"))
	var names []string
	for _, d := range decls {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "Repo,get,top" {
		t.Errorf("py decls = %v (docstring não pode virar declaração)", names)
	}
	if decls[0].EndLine != 6 || decls[1].Kind != "method" || decls[2].Kind != "func" {
		t.Errorf("py: %+v", decls)
	}
}

func TestCraseCruaDoGoNaoEscapa(t *testing.T) {
	src := "package x\n\nfunc A() string {\n\treturn `D:\\`\n}\n\nfunc B() {}\n\nvar s = `multi\nline\\`\n\nfunc C() {}\n"
	decls, _ := Scan([]byte(src), LangFor("a.go"))
	var names []string
	for _, d := range decls {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "A,B,C" {
		t.Errorf("Go: decls = %v (a crase depois de '\\' fecha a string crua)", names)
	}

	js := "const a = `x \\` still inside`;\nfunction after() {}\n"
	decls, _ = Scan([]byte(js), LangFor("a.js"))
	if len(decls) != 1 || decls[0].Name != "after" {
		t.Errorf("JS: em template literal '\\`' escapa: %+v", decls)
	}
}

func TestImportsPython(t *testing.T) {
	_, refs := Scan([]byte("from pkg.mod import a, b as c\nimport os\n"), LangFor("x.py"))
	if got := strings.Join(refsOf(refs, "imports"), ","); got != "pkg.mod,os" {
		t.Errorf("imports = %s", got)
	}
	if got := strings.Join(refsOf(refs, "references"), ","); got != "a,b" {
		t.Errorf("references = %s", got)
	}
}
