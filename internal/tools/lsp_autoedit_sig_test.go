package tools

import (
	"strings"
	"testing"
)

// Renomear e reescrever o comentário da declaração na mesma edição (CS4: SelfLog.WriteLine -> Write) continua sendo rename.
func TestRenameWithDocCommentChange(t *testing.T) {
	old := "class A {\n    /// <remarks>\n    /// The name is historical.\n    /// </remarks>\n    public static void WriteLine(string f) {\n        WriteLine(f);\n    }\n    public static void Enable() { }\n}\n"
	neu := "class A {\n    /// <remarks>\n    /// The suffix was removed.\n    /// </remarks>\n    public static void Write(string f) {\n        WriteLine(f);\n    }\n    public static void Enable() { }\n}\n"
	d := declDeltaOf("A.cs", old, neu)
	if d.kind != declRenamed || d.name != "WriteLine" || d.newName != "Write" || !d.extra {
		t.Fatalf("want renamed with extra, got %+v", d)
	}
	// o servidor devolve o texto velho todo renomeado (inclusive a chamada dentro do corpo)
	post := strings.ReplaceAll(old, "WriteLine", "Write")
	got, ok := mergeResidual(post, old, neu, d.name, d.newName, d.line)
	if !ok {
		t.Fatal("mergeResidual refused a clean merge")
	}
	want := strings.ReplaceAll(neu, "WriteLine(f);", "Write(f);")
	if got != want {
		t.Fatalf("merge = %q, want %q", got, want)
	}
	// o comentário perde uma linha: as declarações de baixo descem uma linha e o rename continua valendo
	short := strings.Replace(neu, "    /// The suffix was removed.\n    /// </remarks>\n", "    /// </remarks>\n", 1)
	d3 := declDeltaOf("A.cs", old, short)
	if d3.kind != declRenamed || !d3.extra {
		t.Fatalf("a shifted file must still be a rename, got %+v", d3)
	}
	if got, ok := mergeResidual(post, old, short, d3.name, d3.newName, d3.line); !ok || !strings.Contains(got, "Write(f);") || strings.Contains(got, "suffix") {
		t.Fatalf("shifted merge failed: ok=%v %q", ok, got)
	}
	// duas regiões mudadas longe uma da outra: sem prova de trecho único, não é rename
	far := strings.Replace(neu, "public static void Enable() { }", "public static void Enable() { int x; }", 1)
	if d2 := declDeltaOf("A.cs", old, far); d2.kind == declRenamed && d2.extra {
		t.Fatalf("a second unrelated change must not ride along, got %+v", d2)
	}
}

// Sobrecargas no mesmo arquivo não podem esconder a mudança de assinatura (SelfLog.Write do serilog).
func TestDeclDeltaSignatureWithHomonyms(t *testing.T) {
	old := "class A {\n    public static void Write(string f) { }\n    public static void Write(string f, int a) { }\n    public static void Enable() { }\n}\n"
	neu := "class A {\n    public static void Write(string f) { }\n    public static void Write(int level, string f, int a) { }\n    public static void Enable() { }\n}\n"
	d := declDeltaOf("A.cs", old, neu)
	if d.kind != declSigned || d.name != "Write" || d.line != 3 {
		t.Fatalf("want signed Write at line 3, got kind=%v name=%q line=%d", d.kind, d.name, d.line)
	}
}
