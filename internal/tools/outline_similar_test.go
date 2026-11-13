package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/graph"
)

// setupSimilarRepo cria um repositório temporário com os arquivos dados (caminho relativo → conteúdo), indexa e devolve
// a raiz. O índice fica isolado num graph.db temporário.
func setupSimilarRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	graph.SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { graph.SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := graph.Refresh(root); err != nil {
		t.Fatal(err)
	}
	return root
}

const csClass = "namespace X\n{\n    public class %s\n    {\n    }\n}\n"

// TestOutlineExatoNaoMuda: símbolo que existe no arquivo sai como antes, sem bloco de sugestão.
func TestOutlineExatoNaoMuda(t *testing.T) {
	root := setupSimilarRepo(t, map[string]string{
		"src/Core/LevelOverrideMap.cs": strings.Replace(csClass, "%s", "LevelOverrideMap", 1),
	})
	out, err := HandleCodeOutline(CodeOutlineArgs{Path: filepath.Join(root, "src", "Core", "LevelOverrideMap.cs"), Symbol: "LevelOverrideMap"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "L3") || strings.Contains(out, "similar names") || strings.Contains(out, "no declaration") {
		t.Fatalf("exato deve sair como antes: %q", out)
	}
}

// TestOutlineSimilarDoExemplo: LoggerLevelOverrideMap não existe; o índice tem LevelOverrideMap e LevelOverride.
func TestOutlineSimilarDoExemplo(t *testing.T) {
	root := setupSimilarRepo(t, map[string]string{
		"src/Serilog/Core/LevelOverrideMap.cs": strings.Replace(csClass, "%s", "LevelOverrideMap", 1),
		"src/Serilog/Core/LevelOverride.cs":    strings.Replace(csClass, "%s", "LevelOverride", 1),
		"src/Serilog/Core/Other.cs":            strings.Replace(csClass, "%s", "Unrelated", 1),
	})
	out, err := HandleCodeOutline(CodeOutlineArgs{Path: filepath.Join(root, "src", "Serilog", "Core", "Other.cs"), Symbol: "LoggerLevelOverrideMap"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no declaration matching") || !strings.Contains(out, "similar names in the index") {
		t.Fatalf("sugestão esperada: %q", out)
	}
	first := strings.Index(out, "src/Serilog/Core/LevelOverrideMap.cs:L")
	second := strings.Index(out, "src/Serilog/Core/LevelOverride.cs:L")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("LevelOverrideMap deve vir antes de LevelOverride: %q", out)
	}
}

// TestOutlineSemSimilarMantemMensagem: nada parecido → só a mensagem de antes.
func TestOutlineSemSimilarMantemMensagem(t *testing.T) {
	root := setupSimilarRepo(t, map[string]string{
		"src/Other.cs": strings.Replace(csClass, "%s", "Unrelated", 1),
	})
	out, err := HandleCodeOutline(CodeOutlineArgs{Path: filepath.Join(root, "src", "Other.cs"), Symbol: "Zzqqxxwwvv"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no declaration matching \"Zzqqxxwwvv\")" {
		t.Fatalf("mensagem deve ser a de antes: %q", out)
	}
}

// TestOutlineSimilarLimiteDeCinco: sete candidatos no índice, no máximo cinco linhas de sugestão.
func TestOutlineSimilarLimiteDeCinco(t *testing.T) {
	files := map[string]string{"src/Host.cs": strings.Replace(csClass, "%s", "Unrelated", 1)}
	for _, n := range []string{"AlphaOne", "BetaTwo", "GammaThree", "DeltaFour", "AlphaFive", "BetaSix", "GammaSeven"} {
		files["src/"+n+".cs"] = strings.Replace(csClass, "%s", n, 1)
	}
	root := setupSimilarRepo(t, files)
	out, err := HandleCodeOutline(CodeOutlineArgs{Path: filepath.Join(root, "src", "Host.cs"), Symbol: "AlphaBetaGammaDelta"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "similar names in the index") {
		t.Fatalf("sugestão esperada: %q", out)
	}
	if n := strings.Count(out, ".cs:L"); n != 5 {
		t.Fatalf("esperava 5 candidatos, veio %d: %q", n, out)
	}
}
