package tools

import (
	"fmt"
	"strings"
	"testing"
)

func slWarn(proj, tfm string) string {
	return fmt.Sprintf(`W:\nuget\packages\microsoft.build.tasks.git\1.1.1\build\Microsoft.Build.Tasks.Git.targets(25,5): warning : Nao e possivel localizar o repositorio. [W:\r\src\%s.csproj::TargetFramework=%s]`, proj, tfm)
}

func TestDotnetCompactCollapsesSourceLinkAndPortuguesePassedTests(t *testing.T) {
	var b strings.Builder
	for _, tfm := range []string{"netstandard2.0", "net8.0", "net6.0"} {
		b.WriteString(slWarn("A", tfm) + "\n")
	}
	b.WriteString("1 arquivos de teste no total corresponderam ao padrao especificado.\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "  Aprovado Suite.Teste%d [< 1 ms]\n", i)
	}
	b.WriteString("Total de testes: 20\n     Aprovados: 20\nTempo total: 0,49 Segundos\n")
	out, note, ok := compactBuildOutput(`cd /d X && set A=B&& W:\sdk\dotnet.exe test T.csproj`, 0, b.String(), "")
	if !ok {
		t.Fatal("deveria compactar")
	}
	if strings.Contains(out, "Aprovado Suite") || strings.Contains(out, "Microsoft.Build.Tasks.Git") {
		t.Fatalf("ruido ficou:\n%s", out)
	}
	if !strings.Contains(out, "3 SourceLink/Git warnings collapsed") || !strings.Contains(out, "Aprovados: 20") {
		t.Fatalf("faltou o resumo:\n%s", out)
	}
	if note == "" {
		t.Fatal("sem nota")
	}
}

func TestDotnetCompactKeepsRealErrorsAndFirstOfRepeatedWarnings(t *testing.T) {
	raw := strings.Join([]string{
		`src\A.cs(10,5): warning CS0168: variavel 'x' nunca usada [W:\r\A.csproj::TargetFramework=net6.0]`,
		`src\A.cs(10,5): warning CS0168: variavel 'x' nunca usada [W:\r\A.csproj::TargetFramework=net8.0]`,
		`src\B.cs(3,1): error CS1002: ; esperado [W:\r\B.csproj::TargetFramework=net6.0]`,
		`src\B.cs(3,1): error CS1002: ; esperado [W:\r\B.csproj::TargetFramework=net8.0]`,
		"Build started", "Build started", "Build started",
	}, "\n")
	out, _, ok := compactBuildOutput(`dotnet build`, 1, raw, "")
	if !ok {
		t.Fatal("deveria compactar")
	}
	if strings.Count(out, "CS0168") != 1 || !strings.Contains(out, "1 repeated warnings collapsed") {
		t.Fatalf("aviso repetido:\n%s", out)
	}
	if strings.Count(out, "CS1002") != 2 {
		t.Fatalf("erro nao pode sumir (nem ser deduplicado):\n%s", out)
	}
}
