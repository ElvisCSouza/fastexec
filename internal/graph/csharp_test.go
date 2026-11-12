package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// csRepo monta um repositório .NET em miniatura: três projetos ligados por ProjectReference (Core <- Lib <- MyApp) e
// uma solução, num namespace file-scoped (Core.Models), um em bloco (Lib, MyApp) e um à parte (Outro). No MyApp ficam
// os casos difíceis do léxico: comentário de linha e de bloco, string normal, verbatim de uma e de várias linhas,
// string crua, interpolação, LINQ, `new()` sem tipo e membro com corpo de expressão. As fixtures ficam em t.TempDir()
// (e não em testdata/) porque o grafo indexa a raiz do repositório: arquivo em testdata entraria no índice real.
func csRepo(t *testing.T) string {
	t.Helper()
	SetDBPath(filepath.Join(t.TempDir(), "graph.db"))
	t.Cleanup(func() { SetDBPath("") })
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Core/Core.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>
`,
		"Lib/Lib.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Core\Core.csproj" />
  </ItemGroup>
</Project>
`,
		"MyApp/MyApp.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Lib\Lib.csproj" />
    <ProjectReference Include="..\Core\Core.csproj" />
  </ItemGroup>
</Project>
`,
		"App.sln": `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "MyApp", "MyApp\MyApp.csproj", "{11111111-1111-1111-1111-111111111111}"
EndProject
Project("{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}") = "Core", "Core\Core.csproj", "{22222222-2222-2222-2222-222222222222}"
EndProject
Project("{2150E333-8FDC-42A3-9474-1A3956D46DE8}") = "SolucaoItens", "SolucaoItens", "{33333333-3333-3333-3333-333333333333}"
EndProject
`,
		"Core/Models.cs": `namespace Core.Models;

public class Pedido
{
    public int Id { get; set; }
    public string Nome { get; set; } = "";
    public const string Prefixo = "PED-";
    public static int Total = 0;

    public Pedido(int id) { Id = id; Total++; }

    public string Descrever() => $"{Prefixo}{Id}: {Nome}";
}

public record PedidoResumo(int Id, string Nome);

public interface IRepositorio<T> where T : class
{
    T? Obter(int id);
    void Salvar(T item);
}

public enum Status { Ativo, Inativo }

public class Container
{
    public class Nested { }

    public Nested Criar() => new();
}

public delegate void Aviso(int id);

public class PedidoEspecial : Pedido { }
`,
		"Lib/Repo.cs": `using Core.Models;

namespace Lib
{
    public class RepositorioMemoria : IRepositorio<Pedido>
    {
        private readonly Dictionary<int, Pedido> _mapa = new();

        public Pedido? Obter([FromQuery(Name = "id")] int id)
        {
            return _mapa.TryGetValue(id, out var achado) ? achado : null;
        }

        public void Salvar(Pedido item)
        {
            _mapa[item.Id] = item;
        }

        public static int Contar(IEnumerable<Pedido> lista) => lista.Count();
    }
}
`,
		"MyApp/Services/PedidoService.cs": `using Core.Models;
using System.Collections.Generic;

namespace MyApp.Services;

public partial class PedidoService : IRepositorio<Pedido>
{
    private readonly List<Pedido> _itens = new();

    public Pedido? Obter(int id) => _itens.FirstOrDefault(p => p.Id == id);

    public void Salvar(Pedido item)
    {
        _itens.Add(item);
        Gravar();
    }

    private void Gravar() { }

    public async Task<int> ContarAsync() => await Task.FromResult(_itens.Count);
}

public partial class PedidoService
{
    public string Nome { get; set; } = "";
}
`,
		"MyApp/Program.cs": `// Comentario: NaoExiste() nao vira aresta (nem class Pedido aqui)
using System;
using Core.Models;
using MyApp.Services;

namespace MyApp
{
    internal static class Program
    {
        /* Bloco: TambemNao() e "NaoExiste(" ficam fora */
        private static void Main()
        {
            var svc = new PedidoService();
            var texto = "ChamadaFalsa() e { chave }";
            var caminho = @"C:\temp\Falsa()\x";
            var cru = """
                TambemFalsa()
                """;
            var multi = @"linha
FalsaMulti()";
            var interp = $"{texto} OutraFalsa()";
            var resumo = new PedidoResumo(1, "x");
            Console.WriteLine(texto + resumo.Nome);
            var lista = new List<Pedido>();
            var ativos = lista.Where(p => p.Id > 0).Select(p => p.Nome).ToList();
            svc.Salvar(new Pedido(1));
            svc.Obter(1);
            Mostrar(ativos);
        }

        private static void Mostrar(IEnumerable<string> nomes) { }
    }
}
`,
		"Other/Outro.cs": `namespace Outro
{
    public class Cliente
    {
        [Obsolete("nao usar Salvar")]
        public void Salvar() { }

        public void Usar()
        {
            Salvar();
        }
    }
}
`,
		// ruído: saída de build e arquivo gerado pelas ferramentas
		"bin/Debug/Extra.cs":   "namespace Lixo;\npublic class ExtraBin { }\n",
		"obj/Debug/Extra.cs":   "namespace Lixo;\npublic class ExtraObj { }\n",
		"MyApp/Widget.g.cs":    "namespace Gest;\npublic class GeradoNaoIndexado { }\n",
		"README.md":            "# demo\n",
		"Core/NaoCsharp.cs.md": "este arquivo nao e C#\n",
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

func TestCSharpIndice(t *testing.T) {
	root := csRepo(t)
	st, err := Refresh(root)
	if err != nil {
		t.Fatal(err)
	}
	// 5 fontes C# + 3 projetos (.csproj) + 1 solução; bin/, obj/, *.g.cs, o .md e o README ficam fora
	if st.Changed != 9 {
		t.Errorf(".cs/.csproj/.sln indexados: Changed=%d, quer 9", st.Changed)
	}
	d, _ := store()
	var n int
	err = d.QueryRow(`SELECT COUNT(*) FROM code_symbols WHERE repo_root = ? AND (file_path LIKE 'bin/%'
		OR file_path LIKE 'obj/%' OR file_path LIKE '%.g.cs')`, repoKey(root)).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("ruído do .NET entrou no índice (%d símbolos)", n)
	}
	for _, nome := range []string{"ExtraBin", "ExtraObj", "GeradoNaoIndexado"} {
		if got, _ := Definition(root, nome); !strings.Contains(got, "no definition of") {
			t.Errorf("%s não deveria existir no índice:\n%s", nome, got)
		}
	}
}

func TestCSharpDefinicoes(t *testing.T) {
	root := csRepo(t)
	cases := []struct {
		sym  string
		want []string
	}{
		{"Pedido", []string{"Core/Models.cs:3-13 (class)", "public class Pedido", "Core/Models.cs:10-10 (constructor)", "public Pedido(int id)"}},
		{"PedidoResumo", []string{"Core/Models.cs:15-15 (record)", "public record PedidoResumo(int Id, string Nome);"}},
		{"IRepositorio", []string{"Core/Models.cs:17-21 (interface)", "public interface IRepositorio<T> where T : class"}},
		{"Obter", []string{"Core/Models.cs:19-19 (method)", "T? Obter(int id);"}},
		{"Status", []string{"Core/Models.cs:23-23 (enum)"}},
		{"Container", []string{"Core/Models.cs:25-30 (class)"}},
		{"Nested", []string{"Core/Models.cs:27-27 (class)", "public class Nested { }"}},
		{"Aviso", []string{"Core/Models.cs:32-32 (delegate)", "public delegate void Aviso(int id);"}},
		{"Descrever", []string{"Core/Models.cs:12-12 (method)", `public string Descrever() => $"{Prefixo}{Id}: {Nome}";`}},
		{"Prefixo", []string{"Core/Models.cs:7-7 (field)", `public const string Prefixo = "PED-";`}},
		{"Total", []string{"Core/Models.cs:8-8 (field)", "public static int Total = 0;"}},
		{"Nome", []string{"(property)"}},
		{"ContarAsync", []string{"(method)", "public async Task<int> ContarAsync()"}},
		{"Gravar", []string{"MyApp/Services/PedidoService.cs:18-18 (method)"}},
		{"App", []string{"App.sln:1-7 (project)", "solution App.sln"}},
		{"Core", []string{"(project)", "csproj Core/Core.csproj"}},
	}
	for _, c := range cases {
		got, err := Definition(root, c.sym)
		if err != nil {
			t.Fatalf("def %s: %v", c.sym, err)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("def %s não trouxe %q:\n%s", c.sym, w, got)
			}
		}
	}
	// classe parcial: as duas partes saem como o mesmo nome, cada uma com seu intervalo
	got, _ := Definition(root, "PedidoService")
	for _, w := range []string{"MyApp/Services/PedidoService.cs:6-21 (class)", "MyApp/Services/PedidoService.cs:23-26 (class)"} {
		if !strings.Contains(got, w) {
			t.Errorf("def PedidoService (parcial) sem %q:\n%s", w, got)
		}
	}
}

func TestCSharpCallers(t *testing.T) {
	root := csRepo(t)

	// homônimos: o mesmo nome declarado em namespaces diferentes; a declaração não é listada como caller
	got, _ := Callers(root, "Salvar", 0, 1)
	for _, w := range []string{"MyApp/Program.cs:26", "Other/Outro.cs:10", "(calls)"} {
		if !strings.Contains(got, w) {
			t.Errorf("callers Salvar sem %q:\n%s", w, got)
		}
	}
	for _, w := range []string{"MyApp/Services/PedidoService.cs:12", "Other/Outro.cs:6"} {
		if strings.Contains(got, w) {
			t.Errorf("declaração listada como caller (%q):\n%s", w, got)
		}
	}
	// a base list vira aresta própria (`inherits`/`implements`), fora do que Callers lista: classe base e interface
	d, _ := store()
	rows, err := d.Query(`SELECT source_file, relation_type, target_symbol FROM code_edges
		WHERE repo_root = ? AND relation_type IN ('inherits','implements') ORDER BY source_file, target_symbol`, repoKey(root))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	base := map[string]bool{}
	for rows.Next() {
		var f, rel, tgt string
		if err := rows.Scan(&f, &rel, &tgt); err != nil {
			t.Fatal(err)
		}
		base[f+" "+rel+" "+tgt] = true
	}
	for _, w := range []string{
		"Core/Models.cs inherits Pedido",
		"Lib/Repo.cs implements IRepositorio",
		"MyApp/Services/PedidoService.cs implements IRepositorio",
	} {
		if !base[w] {
			t.Errorf("aresta de base ausente (%q): %v", w, base)
		}
	}

	got, _ = Callers(root, "PedidoService", 0, 1)
	if !strings.Contains(got, "MyApp/Program.cs:13") || !strings.Contains(got, "(references)") {
		t.Errorf("`new PedidoService()` deveria sair como references:\n%s", got)
	}
	if strings.Contains(got, "(calls)") {
		t.Errorf("`new Tipo(` não é chamada:\n%s", got)
	}
	got, _ = Callers(root, "Gravar", 0, 1)
	if !strings.Contains(got, "MyApp/Services/PedidoService.cs:15") || !strings.Contains(got, "(calls)") {
		t.Errorf("chamada interna ao método:\n%s", got)
	}
	// interpolação de string não é chamada: Descrever está definido e não tem caller
	got, _ = Callers(root, "Descrever", 0, 1)
	if !strings.Contains(got, "no callers of") {
		t.Errorf("interpolação virou chamada:\n%s", got)
	}
	// target-typed `new()` não tem nome de tipo: nada a apontar
	got, _ = Callers(root, "Nested", 0, 1)
	if !strings.Contains(got, "no callers of") {
		t.Errorf("`new()` sem tipo virou aresta:\n%s", got)
	}
	// LINQ e métodos de biblioteca saem como chamadas por nome, como em qualquer linguagem
	got, _ = Callers(root, "Where", 0, 1)
	if !strings.Contains(got, "MyApp/Program.cs:25") {
		t.Errorf("LINQ: %s", got)
	}
}

// TestCSharpFalsosPositivos: comentário, string normal, verbatim, crua, interpolada e atributo não geram símbolo
// nem aresta.
func TestCSharpFalsosPositivos(t *testing.T) {
	root := csRepo(t)
	for _, nome := range []string{"NaoExiste", "TambemNao", "ChamadaFalsa", "Falsa", "TambemFalsa", "FalsaMulti", "OutraFalsa"} {
		got, _ := Callers(root, nome, 0, 1)
		if strings.Contains(got, "Program.cs") {
			t.Errorf("texto virou aresta (%s):\n%s", nome, got)
		}
		if got, _ := Definition(root, nome); !strings.Contains(got, "no definition of") {
			t.Errorf("texto virou símbolo (%s):\n%s", nome, got)
		}
	}
	// atributo (na declaração e no parâmetro): ignorado como símbolo e como chamada
	for _, nome := range []string{"Obsolete", "FromQuery", "Name"} {
		if got, _ := Callers(root, nome, 0, 1); !strings.Contains(got, "no callers of") {
			t.Errorf("atributo virou aresta (%s):\n%s", nome, got)
		}
		if got, _ := Definition(root, nome); !strings.Contains(got, "no definition of") {
			t.Errorf("atributo virou símbolo (%s):\n%s", nome, got)
		}
	}
	// o código real do mesmo arquivo continua no índice
	if got, _ := Definition(root, "Program"); !strings.Contains(got, "MyApp/Program.cs:8-32 (class)") {
		t.Errorf("a classe Program do arquivo com strings sumiu:\n%s", got)
	}
}

func TestCSharpImpact(t *testing.T) {
	root := csRepo(t)
	got, err := Impact(root, "Core/Models.cs", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"- MyApp/Program.cs (uses: Pedido, PedidoResumo)",
		"- MyApp/Services/PedidoService.cs (uses: IRepositorio)",
		"- Lib/Repo.cs (uses: IRepositorio)",
		"- Lib/Lib.csproj (projref: Core)",
		"- MyApp/MyApp.csproj (projref: Core)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("impact Core/Models.cs sem %q:\n%s", w, got)
		}
	}
	// Outro namespace que não usa `using` do alvo não entra
	if strings.Contains(got, "Other/Outro.cs") {
		t.Errorf("namespace alheio entrou no impact:\n%s", got)
	}

	got, _ = Impact(root, "Core/Core.csproj", 0)
	for _, w := range []string{"- Lib/Lib.csproj (projref: Core)", "- MyApp/MyApp.csproj (projref: Core)"} {
		if !strings.Contains(got, w) {
			t.Errorf("impact Core.csproj sem %q:\n%s", w, got)
		}
	}

	// projeto sem dependente: só a solução aparece; quem não referencia não aparece
	got, _ = Impact(root, "MyApp/MyApp.csproj", 0)
	if !strings.Contains(got, "- App.sln (projref: MyApp)") {
		t.Errorf("impact MyApp.csproj sem a solução:\n%s", got)
	}
	for _, w := range []string{"Lib/Lib.csproj (projref: MyApp)", "Core/Core.csproj (projref: MyApp)"} {
		if strings.Contains(got, w) {
			t.Errorf("projeto que não referencia apareceu (%q):\n%s", w, got)
		}
	}

	if got, _ = Impact(root, "Other/Outro.cs", 0); !strings.Contains(got, "depends on") {
		t.Errorf("outro namespace não tem dependente:\n%s", got)
	}

	// pasta de solução do .sln aparece na mesma forma de um projeto, mas não é um: não gera aresta projref
	d, _ := store()
	var n int
	err = d.QueryRow(`SELECT COUNT(*) FROM code_edges WHERE repo_root = ? AND relation_type = 'projref'
		AND target_symbol = 'SolucaoItens'`, repoKey(root)).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("pasta de solução virou aresta projref (%d arestas)", n)
	}
}

func TestCSharpOutlineNaoUsaLangCS(t *testing.T) {
	src := []byte(`namespace A.B;

public class C
{
    public int P { get; set; }
    public void M() { }
}
`)
	decls, ok := ParseCSharp("x.cs", src)
	if !ok {
		t.Fatal("ParseCSharp recusou .cs")
	}
	kinds := map[string]string{}
	for _, d := range decls {
		kinds[d.Name] = d.Kind
	}
	for name, kind := range map[string]string{"A.B": "namespace", "C": "class", "P": "property", "M": "method"} {
		if kinds[name] != kind {
			t.Errorf("%s: kind %q, quer %q (declarações: %v)", name, kinds[name], kind, decls)
		}
	}
	if _, ok := ParseCSharp("x.go", src); ok {
		t.Error("ParseCSharp não deveria responder por .go")
	}
}
