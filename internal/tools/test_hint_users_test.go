package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// Nenhum teste tem o nome do arquivo editado: o teste que o cobre tem o nome de quem o usa (Serilog:
// LevelOutputFormat.cs <- MessageTemplateTextFormatter.cs <- MessageTemplateTextFormatterTests.cs).
func TestRelatedTestsLineFollowsTheUserOfTheFile(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"src/Serilog/Formatting/Display/LevelOutputFormat.cs":                "static class LevelOutputFormat {}",
		"src/Serilog/Formatting/Display/MessageTemplateTextFormatter.cs":     "class MessageTemplateTextFormatter { void F() { LevelOutputFormat.Render(); } }",
		"src/Serilog/Formatting/Display/Unrelated.cs":                        "class Unrelated {}",
		"test/Serilog.Tests/Formatting/MessageTemplateTextFormatterTests.cs": "class MessageTemplateTextFormatterTests {}",
		"test/Serilog.Tests/Formatting/UnrelatedTests.cs":                    "class UnrelatedTests {}",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "src", "Serilog", "Formatting", "Display", "LevelOutputFormat.cs"), lang: "csharp"}
	line := a.relatedTestsLine()
	if !strings.Contains(line, "MessageTemplateTextFormatterTests.cs") || !strings.Contains(line, "FullyQualifiedName~MessageTemplateTextFormatterTests") {
		t.Fatalf("second hop missed the test of the user: %q", line)
	}
	if strings.Contains(line, "UnrelatedTests") {
		t.Fatalf("unrelated test listed: %q", line)
	}
}

// Um teste que cita o tipo diretamente vale mais que o teste de nome parecido com o do usuário.
func TestRelatedTestsLinePrefersATestThatNamesTheType(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"src/Lib/Widget.cs":              "class Widget {}",
		"src/Lib/Host.cs":                "class Host { Widget w; }",
		"test/Lib.Tests/HostTests.cs":    "class HostTests {}",
		"test/Lib.Tests/SpecialCases.cs": "class SpecialCases { void T() { new Widget(); } }",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib", "Widget.cs"), lang: "csharp"}
	line := a.relatedTestsLine()
	if !strings.Contains(line, "SpecialCases.cs") || strings.Contains(line, "HostTests") {
		t.Fatalf("direct reference should win: %q", line)
	}
}

// Ninguém usa o arquivo e nenhum teste tem o nome dele: silêncio, nunca um palpite.
func TestRelatedTestsLineSilentWhenNobodyUsesTheFile(t *testing.T) {
	root := writeTestTree(t, map[string]string{
		"src/Lib/Lonely.cs":            "class Lonely {}",
		"src/Lib/Other.cs":             "class Other {}",
		"test/Lib.Tests/OtherTests.cs": "class OtherTests {}",
	})
	a := &autoEdit{root: root, abs: filepath.Join(root, "src", "Lib", "Lonely.cs"), lang: "csharp"}
	if line := a.relatedTestsLine(); line != "" {
		t.Fatalf("expected silence, got %q", line)
	}
}
