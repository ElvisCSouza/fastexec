package codescan

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// scanOf roda a varredura da extensão e devolve as linhas "decl ..." / "ref ..." ordenadas: a comparação é com o
// texto inteiro, então uma declaração a mais ou um alvo diferente quebra o teste (é o ponto).
func scanOf(t *testing.T, name, src string) ([]string, []string) {
	t.Helper()
	decls, refs := Scan([]byte(src), LangFor(name))
	var ds, rs []string
	for _, d := range decls {
		ds = append(ds, fmt.Sprintf("decl %s %s L%d-%d %s", d.Kind, d.Name, d.Line, d.EndLine, d.Sig))
	}
	for _, r := range refs {
		rs = append(rs, fmt.Sprintf("ref %s %s L%d", r.Rel, r.Target, r.Line))
	}
	sort.Strings(ds)
	sort.Strings(rs)
	return ds, rs
}

func checkLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got:\n%s\nquer:\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCSSSeletoresVariaveisEAninhamento(t *testing.T) {
	src := `:root {
  --gap: 8px;
}

.card, .card--wide {
  padding: var(--gap);
}

#main .card:hover {
  color: red;
}

@media (min-width: 600px) {
  .card { color: green; }
}
`
	decls, refs := scanOf(t, "a.css", src)
	checkLines(t, "decls", decls, []string{
		// EndLine é o "}" do bloco em que o seletor foi aberto, não a linha do seletor
		"decl css_class card L14-14 .card",
		"decl css_class card L5-7 .card",
		"decl css_class card L9-11 #main .card:hover",
		"decl css_class card--wide L5-7 .card--wide",
		"decl css_id main L9-11 #main .card:hover",
		"decl css_var --gap L2-3 --gap: 8px",
	})
	checkLines(t, "refs", refs, []string{"ref references --gap L6"})
}

func TestCSSScssInterpolacaoNaoViraNo(t *testing.T) {
	src := ".a {\n  color: $cor;\n  #{$x} { color: red; }\n}\n"
	decls, refs := scanOf(t, "a.scss", src)
	checkLines(t, "decls", decls, []string{"decl css_class a L1-4 .a"})
	checkLines(t, "refs", refs, nil)
}

func TestHTMLIDsClassesEAssets(t *testing.T) {
	src := `<!doctype html>
<html>
  <head>
    <link rel="stylesheet" href="styles/main.css">
    <script src="./js/app.js"></script>
    <script src="https://cdn.example.com/lib.js"></script>
  </head>
  <body>
    <div id="app" class="container wide">oi</div>
    <script>
      document.getElementById('app');
    </script>
  </body>
</html>
`
	decls, refs := scanOf(t, "index.html", src)
	checkLines(t, "decls", decls, []string{
		`decl html_id app L9-9 <div id="app" class="container wide">`,
	})
	// a URL do CDN não vira aresta de arquivo: só referência local entra
	checkLines(t, "refs", refs, []string{
		"ref html_ref ./js/app.js L5",
		"ref html_ref styles/main.css L4",
		"ref references app L11",
		"ref references container L9",
		"ref references wide L9",
	})
}

func TestRazorComponentesECodeIgnorado(t *testing.T) {
	src := `@page "/cards"

<div id="main" class="row">
  <Card Title="A" />
  <My.Ns.Widget></My.Ns.Widget>
</div>

@code {
  private Card _card = new();
  void DoWork() { <Card /> }
}
`
	decls, refs := scanOf(t, "Cards.razor", src)
	checkLines(t, "decls", decls, []string{`decl html_id main L3-3 <div id="main" class="row">`})
	// o corpo do @code é C#: nada dele vira nó nem aresta (nem o <Card /> interno)
	checkLines(t, "refs", refs, []string{
		"ref references row L3",
		"ref renders Card L4",
		"ref renders Widget L5",
	})
}

func TestTSXComponenteHookPropsERenders(t *testing.T) {
	src := `import { useState } from 'react';

export function Card({ title }: CardProps) {
  const [open, setOpen] = useState(false);
  return (
    <div className="card wide">
      <h2 className="card__title">{title}</h2>
      {open ? <Actions.Close /> : null}
    </div>
  );
}

export const helper = (a: number) => a + 1;

export function useCard(id: string) {
  const [state] = useState(null);
  return { state, id };
}
`
	decls, refs := scanOf(t, "Card.tsx", src)
	checkLines(t, "decls", decls, []string{
		"decl component Card L3-11 component Card({ title }: CardProps)",
		"decl func helper L13-13 export const helper = (a: number) => a + 1;",
		"decl hook useCard L15-18 hook useCard(id: string)",
	})
	checkLines(t, "refs", refs, []string{
		"ref calls useState L16",
		"ref calls useState L4",
		"ref imports react L1",
		"ref references card L6",
		"ref references card__title L7",
		"ref references useState L1",
		"ref references wide L6",
		"ref renders Close L8",
	})
}

func TestTSXNaoInventaComponente(t *testing.T) {
	// comparação e divisão não são marcação; "user" não é hook; className montado por template não vira classe
	src := "export function pick(a, b) {\n  return a < b ? a / b : user(1);\n}\n\n" +
		"export function Wrapper(props) {\n  return <div className={`c-${props.x}`} />;\n}\n"
	decls, refs := scanOf(t, "w.tsx", src)
	checkLines(t, "decls", decls, []string{
		"decl component Wrapper L5-7 component Wrapper(props)",
		"decl func pick L1-3 export function pick(a, b)",
	})
	checkLines(t, "refs", refs, []string{"ref calls user L2"})
}
