package tools

import "testing"

// Sobrecargas no mesmo arquivo não podem esconder a mudança de assinatura (SelfLog.Write do serilog).
func TestDeclDeltaSignatureWithHomonyms(t *testing.T) {
	old := "class A {\n    public static void Write(string f) { }\n    public static void Write(string f, int a) { }\n    public static void Enable() { }\n}\n"
	neu := "class A {\n    public static void Write(string f) { }\n    public static void Write(int level, string f, int a) { }\n    public static void Enable() { }\n}\n"
	d := declDeltaOf("A.cs", old, neu)
	if d.kind != declSigned || d.name != "Write" || d.line != 3 {
		t.Fatalf("want signed Write at line 3, got kind=%v name=%q line=%d", d.kind, d.name, d.line)
	}
}
