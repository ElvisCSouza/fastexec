package graph

import (
	"os"
	"path/filepath"
	"testing"
)

// feFixture monta um repositório de front-end: dois componentes React que se renderizam (Header > Card > Badge), um
// hook em .tsx, uma folha de estilo usada por className (com e sem import) e uma página que linka o CSS e o script.
// É fixture própria de propósito: graph_test.go conta 7 arquivos indexados e não pode ser tocada.
func feFixture(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"web/src/components/Card.tsx": "import '../styles/card.css';\n" +
			"import { Badge } from './Badge';\n" +
			"import { useCard } from '../hooks/useCard';\n" +
			"\n" +
			"export function Card({ title }: CardProps) {\n" +
			"  const state = useCard(title);\n" +
			"  return <div className=\"card wide\"><Badge label={title} /></div>;\n" +
			"}\n",
		"web/src/components/Badge.tsx": "export function Badge({ label }: { label: string }) {\n" +
			"  return <span className=\"card badge\">{label}</span>;\n" +
			"}\n",
		"web/src/components/Header.tsx": "export function Header() {\n" +
			"  return <header className=\"card wide\"><Card title=\"oi\" /></header>;\n" +
			"}\n",
		"web/src/hooks/useCard.tsx": "export function useCard(id: string) {\n  return { id };\n}\n",
		"web/src/styles/card.css":   ".card {\n  color: var(--ink);\n}\n:root {\n  --ink: #111;\n}\n",
		"web/src/index.html": "<html>\n  <head>\n    <link rel=\"stylesheet\" href=\"./styles/card.css\">\n" +
			"    <script src=\"./main.js\"></script>\n  </head>\n  <body>\n    <div id=\"app\"></div>\n  </body>\n</html>\n",
		"web/src/main.js": "import { Badge } from './components/Badge';\n",
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
	return root
}

func TestFrontendDefinicoes(t *testing.T) {
	root := feFixture(t)
	cases := map[string]string{
		"Card":     "=== definition of 'Card' ===\n- web/src/components/Card.tsx:5-8 (component)\n  component Card({ title }: CardProps)",
		"Badge":    "=== definition of 'Badge' ===\n- web/src/components/Badge.tsx:1-3 (component)\n  component Badge({ label }: { label: string })",
		"useCard":  "=== definition of 'useCard' ===\n- web/src/hooks/useCard.tsx:1-3 (hook)\n  hook useCard(id: string)",
		"card":     "=== definition of 'card' ===\n- web/src/styles/card.css:1-3 (css_class)\n  .card",
		"app":      "=== definition of 'app' ===\n- web/src/index.html:7-7 (html_id)\n  <div id=\"app\">",
	}
	for sym, want := range cases {
		got, err := Definition(root, sym)
		if err != nil {
			t.Fatalf("Definition(%s): %v", sym, err)
		}
		if body(got) != want {
			t.Errorf("def %s:\n%s\nquer:\n%s", sym, body(got), want)
		}
	}
}

func TestFrontendCallers(t *testing.T) {
	root := feFixture(t)
	// quem renderiza (<Badge />) e quem só importa aparecem com a relação de cada um
	got, err := Callers(root, "Badge", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	// o import também é um site de referência (é o único de main.js): as duas linhas de Card.tsx saem
	want := "=== callers of 'Badge' ===\n" +
		"- web/src/components/Card.tsx:2 (references)\n" +
		"- web/src/components/Card.tsx:7 in Card (renders)\n" +
		"- web/src/main.js:1 (references)\n" +
		"Total: 3 references in 2 files."
	if body(got) != want {
		t.Errorf("callers Badge:\n%s\nquer:\n%s", body(got), want)
	}
	got, _ = Callers(root, "useCard", 0, 1)
	want = "=== callers of 'useCard' ===\n" +
		"- web/src/components/Card.tsx:3 (references)\n" +
		"- web/src/components/Card.tsx:6 in Card (calls)\n" +
		"Total: 2 references in 1 files."
	if body(got) != want {
		t.Errorf("callers useCard:\n%s\nquer:\n%s", body(got), want)
	}
}

// o BFS segue "renders" como segue "calls": a cadeia Header > Card > Badge é um salto por nível.
func TestFrontendCallersTransitivo(t *testing.T) {
	root := feFixture(t)
	got, err := Callers(root, "Badge", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== callers of 'Badge' (depth 2) ===\n" +
		"depth 1:\n" +
		"- web/src/components/Card.tsx:2 (references)\n" +
		"- web/src/components/Card.tsx:7 in Card (renders)\n" +
		"- web/src/main.js:1 (references)\n" +
		"depth 2:\n" +
		"- web/src/components/Header.tsx:2 in Header (renders)\n" +
		"Total: 4 references in 3 files (4 symbols)."
	if body(got) != want {
		t.Errorf("callers Badge depth 2:\n%s\nquer:\n%s", body(got), want)
	}
}

// classe de CSS vale no documento inteiro: Card importa a folha, Badge e Header só usam className e mesmo assim
// entram; a página que linka o CSS entra pelo <link href>. "uses" lista só o que a folha DECLARA (a classe "wide",
// usada no JSX mas ausente do CSS, não é motivo para o arquivo aparecer aqui — quem a declara é outra folha).
func TestFrontendImpactEstilo(t *testing.T) {
	root := feFixture(t)
	got, err := Impact(root, "web/src/styles/card.css", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== impact of 'web/src/styles/card.css' ===\n" +
		"- web/src/components/Badge.tsx (uses: card)\n" +
		"- web/src/components/Card.tsx (uses: card)\n" +
		"- web/src/components/Header.tsx (uses: card)\n" +
		"- web/src/index.html (html_ref)\n" +
		"Total: 4 files."
	if body(got) != want {
		t.Errorf("impact card.css:\n%s\nquer:\n%s", body(got), want)
	}
}

func TestFrontendImpactComponenteEAsset(t *testing.T) {
	root := feFixture(t)
	// componente: quem o importa ou o renderiza
	got, err := Impact(root, "web/src/components/Badge.tsx", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== impact of 'web/src/components/Badge.tsx' ===\n" +
		"- web/src/components/Card.tsx (uses: Badge)\n" +
		"- web/src/main.js (uses: Badge)\n" +
		"Total: 2 files."
	if body(got) != want {
		t.Errorf("impact Badge.tsx:\n%s\nquer:\n%s", body(got), want)
	}
	// <script src="./main.js">: a página depende do script mesmo sem usar símbolo nenhum dele
	got, _ = Impact(root, "web/src/main.js", 0)
	want = "=== impact of 'web/src/main.js' ===\n- web/src/index.html (html_ref)\nTotal: 1 files."
	if body(got) != want {
		t.Errorf("impact main.js:\n%s\nquer:\n%s", body(got), want)
	}
}
