package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csFixture: o mesmo método (Calcular) em duas classes, chamado por receptores de nomes diferentes, como no T7.
func csFixture(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Services/ScfService.cs": `namespace X
{
    public class ScfService
    {
        public int Calcular(int a)
        {
            return a + 1;
        }
    }
}
`,
		"Services/McmsService.cs": `namespace X
{
    public class McmsService
    {
        public int Calcular(int a)
        {
            return a + 2;
        }

        public int Duplicata(int a)
        {
            return Calcular(a);
        }
    }
}
`,
		"App/AppService.cs": `namespace X
{
    public class AppService
    {
        private readonly McmsService _mcmsService;
        private readonly ScfService _scfService;

        public void Vincular()
        {
            var x = _mcmsService.Calcular(1);
        }

        public void Outro()
        {
            var y = _scfService.Calcular(2);
        }

        public void Obscuro(object svc)
        {
            var z = svc.Calcular(3);
        }
    }
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCallersHomonimosMostramReceptorETipo(t *testing.T) {
	root := csFixture(t)
	got, err := Callers(root, "Calcular", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"NOTE: 'Calcular' is declared in 2 types (McmsService, ScfService)",
		"in AppService.Vincular via _mcmsService -> McmsService (calls)",
		"in AppService.Outro via _scfService -> ScfService (calls)",
		"in AppService.Obscuro via svc -> ? (calls)",
		"in McmsService.Duplicata -> McmsService (calls)", // sem receptor: this implícito = a própria classe
		"Total: 4 references",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
}

func TestCallersQualificadoFiltraOutroTipo(t *testing.T) {
	root := csFixture(t)
	got, err := Callers(root, "ScfService.Calcular", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "_mcmsService") || strings.Contains(got, "Duplicata") {
		t.Errorf("referências de McmsService não deveriam aparecer:\n%s", got)
	}
	for _, want := range []string{"in AppService.Outro via _scfService -> ScfService", "in AppService.Obscuro via svc -> ?", "Total: 2 references", "[excluded 2 reference(s)", "McmsService (2)"} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
}

func TestCallersSemHomonimoNaoGastaTokenExtra(t *testing.T) {
	root := csFixture(t)
	got, err := Callers(root, "Duplicata", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "NOTE:") || strings.Contains(got, " via ") || strings.Contains(got, "->") {
		t.Errorf("nome único não deve ganhar anotação:\n%s", got)
	}
}
