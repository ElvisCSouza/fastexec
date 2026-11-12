package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/fastexec/internal/graph"
)

// writeFiles cria a árvore de arquivos a partir de caminho->conteúdo.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newTestRepo monta um repositório git mínimo (a pasta .git basta para o grafo tratá-lo como repositório).
func newTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, repo, files)
	return repo
}

func TestParseIdentSearch(t *testing.T) {
	yes := map[string]string{
		`grep -rn HandleReadFile .`:               "HandleReadFile",
		`grep -rn "func HandleReadFile" internal`: "HandleReadFile",
		`rg -n -t go \bloadUser\b src`:            "loadUser",
		`git grep -n PedidoService`:               "PedidoService",
		`findstr /s /n HandleExec *.go`:           "HandleExec",
		`Select-String -Pattern HandleReadFile`:   "HandleReadFile",
		`sls CalcTotal`:                           "CalcTotal",
	}
	for cmd, want := range yes {
		r, ok := parseIdentSearch(cmd)
		if !ok || r.pattern != want {
			t.Errorf("%q: got %+v ok=%v, want %s", cmd, r, ok, want)
		}
	}
	// -l e -c continuam sendo respondidos, mas na forma pedida (lista de arquivos / contagens)
	if r, ok := parseIdentSearch(`grep -rln HandleReadFile .`); !ok || r.pattern != "HandleReadFile" || !r.list {
		t.Errorf("-l must be a graph search asking for the file list: %+v ok=%v", r, ok)
	}
	if r, ok := parseIdentSearch(`grep -rc HandleReadFile .`); !ok || !r.count {
		t.Errorf("-c must be a graph search asking for the counts: %+v ok=%v", r, ok)
	}

	no := []string{
		`grep -n HandleReadFile files.go`,          // não recursivo
		`grep -rn "connection refused" .`,          // texto
		`grep -rvn HandleReadFile .`,               // -v
		`grep -rn HandleReadFile . | wc -l`,        // pipeline
		`grep -rn --include=*.md HandleReadFile .`, // só docs
		`findstr /n HandleExec *.go`,               // sem /s
		`ls -la`,
		// A caixa muda o que a busca crua acha e o grafo não promete isso: busca real.
		`grep -rni HandleReadFile .`,
		`grep -rn -i HandleReadFile .`,
		`git grep -n -i PedidoService`,
		`findstr /s /i HandleExec *.go`,
		`rg -i HandleReadFile .`,
		// Regex (metacaractere, alternação, âncora, classe): saída diferente da busca por identificador.
		`rg 'HandleReadFile\('`,
		`rg -n '\bFoo\b|Bar' .`,
		`grep -rn '^HandleReadFile' .`,
		`grep -rn 'HandleReadFile$' .`,
		`grep -rn 'Handle.*File' .`,
		`grep -rn '[Hh]andleReadFile' .`,
	}
	for _, cmd := range no {
		if r, ok := parseIdentSearch(cmd); ok {
			t.Errorf("%q must not be a graph search: %+v", cmd, r)
		}
	}
}

// TestGraphSearchAnswersAndRepeats: identificador único devolve a definição com corpo, os chamadores agrupados por
// arquivo com contagem e a quebra non-test/tests; repetir o mesmo comando roda a busca crua.
func TestGraphSearchAnswersAndRepeats(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go":   "package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
		"main.go":    "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
		"a_test.go":  "package main\n\nfunc TestCalc(t *testing.T) {\n\tpkg.CalcTotal(1)\n}\n",
		"other.go":   "package main\n\nfunc Other() {\n\tpkg.CalcTotal(2)\n}\n",
		"third.go":   "package main\n\nfunc Third() {\n\tpkg.CalcTotal(4)\n}\n",
		"unused.txt": "CalcTotal\n", // texto: o índice só cobre identificadores de código
	})

	args := ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: repo, Caller: "test#gs1"}
	out, handled, note := tryGraphSearch(args, args.Command, false)
	if !handled || note != "" {
		t.Fatalf("expected a graph answer, got handled=%v note=%q", handled, note)
	}
	if !strings.HasPrefix(out, GraphMarkSearch) {
		t.Fatalf("graph answer must start with the marker:\n%s", out)
	}
	// definição única e curta: vem com o corpo, pronto para usar
	if !strings.Contains(out, "def: pkg/a.go:3-5 (func)") {
		t.Errorf("missing the definition line:\n%s", out)
	}
	if !strings.Contains(out, "func CalcTotal(x int) int {") {
		t.Errorf("a unique, short definition must carry its body:\n%s", out)
	}
	// chamadores agrupados por arquivo, com contagem, e a quebra non-test/tests no topo
	if !strings.Contains(out, "non-test: 3 file(s) | tests: 1 file(s)") {
		t.Errorf("missing the non-test/tests split:\n%s", out)
	}
	for _, want := range []string{"main.go (1)", "other.go (1)", "third.go (1)", "a_test.go (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("callers must be grouped by file with a count, missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unused.txt") {
		t.Errorf("plain text must not be promised by the index:\n%s", out)
	}
	if !strings.Contains(out, "index covers code identifiers only") {
		t.Errorf("the footer must say what the index covers:\n%s", out)
	}
	// o rodapé traz a forma que realmente funciona
	if !strings.Contains(out, "--raw") {
		t.Errorf("the footer must show the escape that works:\n%s", out)
	}

	if _, handled, note = tryGraphSearch(args, args.Command, false); handled || !strings.Contains(note, GraphMarkRepeat) {
		t.Fatalf("repeat must run the raw search: handled=%v note=%q", handled, note)
	}

	// identificador que não é declarado no código: busca normal
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn NotDeclaredAnywhere .", Cwd: repo, Caller: "test#gs1"}, "grep -rn NotDeclaredAnywhere .", false); handled {
		t.Fatal("unknown identifier must fall back to the raw search")
	}
	// fora de repositório git: busca normal
	plain := t.TempDir()
	_ = os.WriteFile(filepath.Join(plain, "a.go"), []byte("package x\nfunc CalcTotal() {}\n"), 0o644)
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: plain}, "grep -rn CalcTotal .", false); handled {
		t.Fatal("outside a git repository the search must run as usual")
	}

	// busca embrulhada em pwsh -Command
	wrappedArgs := ExecArgs{Host: "local", Command: `pwsh -NoProfile -Command "git grep -n CalcTotal"`, Cwd: repo, Caller: "test#gs2"}
	if outW, handledW, _ := tryGraphSearch(wrappedArgs, wrappedArgs.Command, false); !handledW || !strings.Contains(outW, "CalcTotal") {
		t.Fatalf("wrapped search should be handled by graph: handled=%v out=%s", handledW, outW)
	}
}

// TestGraphSearchHomonym: várias declarações do mesmo nome = ambiguidade declarada, sem escolher uma nem prometer corpo.
func TestGraphSearchHomonym(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"a.go": "package p\n\nfunc DoWork() {}\n",
		"b.go": "package p\n\nfunc DoWork(x int) int { return x }\n",
		"c.go": "package p\n\nfunc Use() {\n\tDoWork()\n\tDoWork(1)\n}\n",
	})
	args := ExecArgs{Host: "local", Command: "grep -rn DoWork .", Cwd: repo, Caller: "test#homonym"}
	out, handled, _ := tryGraphSearch(args, args.Command, false)
	if !handled {
		t.Fatal("a homonym must still be answered: the point is to say it is ambiguous")
	}
	if !strings.Contains(out, "ambiguous") || !strings.Contains(out, "2 definitions") {
		t.Errorf("must declare the ambiguity:\n%s", out)
	}
	if !strings.Contains(out, "a.go:3-3") || !strings.Contains(out, "b.go:3-3") {
		t.Errorf("must list the short definitions:\n%s", out)
	}
	if strings.Contains(out, "return x") {
		t.Errorf("an ambiguous name must not promise a body:\n%s", out)
	}
	if !strings.Contains(out, "c.go (2)") {
		t.Errorf("callers must still be grouped:\n%s", out)
	}
}

// TestGraphSearchListAndCount: -l devolve só a lista de arquivos, -c as contagens por arquivo.
func TestGraphSearchListAndCount(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go":  "package pkg\n\nfunc CalcTotal(x int) int { return x }\n",
		"main.go":   "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n\tpkg.CalcTotal(4)\n}\n",
		"a_test.go": "package main\n\nfunc TestCalc(t *testing.T) {\n\tpkg.CalcTotal(1)\n}\n",
	})

	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rln CalcTotal .", Cwd: repo, Caller: "test#list"}, "grep -rln CalcTotal .", false)
	if !handled {
		t.Fatal("-l must be answered by the graph")
	}
	if !strings.Contains(out, "pkg/a.go") || !strings.Contains(out, "main.go") {
		t.Errorf("-l must list the files:\n%s", out)
	}
	if strings.Contains(out, "callers:") {
		t.Errorf("-l must not carry the caller block:\n%s", out)
	}
	// não-testes primeiro
	if strings.Index(out, "pkg/a.go") > strings.Index(out, "a_test.go") {
		t.Errorf("-l must list non-test files first:\n%s", out)
	}

	out, handled, _ = tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rc CalcTotal .", Cwd: repo, Caller: "test#count"}, "grep -rc CalcTotal .", false)
	if !handled {
		t.Fatal("-c must be answered by the graph")
	}
	if !strings.Contains(out, "main.go: 2") {
		t.Errorf("-c must count per file:\n%s", out)
	}
	if !strings.Contains(out, "pkg/a.go: 0") || !strings.Contains(out, "declares it") {
		t.Errorf("-c must show the declaring file even with no reference:\n%s", out)
	}
}

// TestGraphSearchStaleIndexReindexes: arquivo editado por fora (mtime e conteúdo novos) é reindexado na hora e a
// resposta já reflete o repositório; a reindexação se anuncia.
func TestGraphSearchStaleIndexReindexes(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
		"main.go":  "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
	})
	// primeira passada: indexa
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: repo, Caller: "test#stale1"}, "grep -rn CalcTotal .", false); !handled {
		t.Fatal("first answer must come from the graph")
	}

	// edição por fora: novo arquivo chamador, com mtime à frente do índice
	newFile := filepath.Join(repo, "late.go")
	if err := os.WriteFile(newFile, []byte("package main\n\nfunc Late() {\n\tpkg.CalcTotal(9)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(newFile, future, future); err != nil {
		t.Fatal(err)
	}

	// comando diferente do anterior (repetir o mesmo comando é o escape: roda a busca crua), mesmo alvo
	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn CalcTotal ./", Cwd: repo, Caller: "test#stale2"}, "grep -rn CalcTotal ./", false)
	if !handled {
		t.Fatal("the stale index must be reindexed and the search still answered from the graph")
	}
	if !strings.Contains(out, "late.go (1)") {
		t.Errorf("the answer must reflect the file edited outside:\n%s", out)
	}
	if !strings.Contains(out, "reindexed") || !strings.Contains(out, GraphMarkIndex) {
		t.Errorf("an automatic reindex must announce itself:\n%s", out)
	}
}

// TestGraphSearchRawEscape: '--raw' (e o prefixo 'raw:') tira o grafo do caminho e o comando roda de verdade.
func TestGraphSearchRawEscape(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc CalcTotal(x int) int { return x }\n",
		"main.go":  "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
	})

	for _, cmd := range []string{
		"grep -rn CalcTotal . --raw",
		"--raw grep -rn CalcTotal .", // posição irrelevante fora do primeiro token
		"raw: grep -rn CalcTotal .",
	} {
		args := ExecArgs{Host: "local", Command: cmd, Cwd: repo, Caller: "test#raw-" + cmd}
		label, stripped := stripRawEscape(&args)
		if !stripped {
			t.Fatalf("%q: the raw escape must be recognized and stripped", cmd)
		}
		if strings.Contains(label, "--raw") || strings.Contains(label, "raw:") {
			t.Fatalf("%q: the escape must leave the command: %q", cmd, label)
		}
		if _, handled, _ := tryGraphSearch(args, label, true); handled {
			t.Fatalf("%q: after --raw the graph must step aside", cmd)
		}
		// o comando que sobrou ainda é a busca original (a dica do rodapé funciona como está)
		if r, ok := parseIdentSearch(label); !ok || r.pattern != "CalcTotal" {
			t.Fatalf("%q: the stripped command must still be the search: %q", cmd, label)
		}
	}

	// fora de uma busca, o '--raw' não é tocado (comando byte a byte)
	args := ExecArgs{Host: "local", Command: "git status --raw", Cwd: repo}
	if label, stripped := stripRawEscape(&args); stripped || label != "git status --raw" || args.Command != "git status --raw" {
		t.Fatalf("a non-search command must pass through byte for byte: %q stripped=%v cmd=%q", label, stripped, args.Command)
	}
	// o rodapé mostra a forma que o stripRawEscape aceita
	if got := withRawEscape("grep -rn CalcTotal ."); got != "grep -rn CalcTotal . --raw" {
		t.Fatalf("withRawEscape: got %q", got)
	}
	if got := withRawEscape("grep -rn CalcTotal -- ."); got != "grep -rn CalcTotal --raw -- ." {
		t.Fatalf("withRawEscape must stay before the '--': got %q", got)
	}
}

// TestGraphSearchFileish: busca por NOME DE ARQUIVO (find -name, listagem canalizada) responde com os arquivos.
func TestGraphSearchFileish(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"internal/graph/store.go": "package graph\n\nfunc store() {}\n",
		"internal/graph/query.go": "package graph\n\nfunc query() {}\n",
		"internal/tools/exec.go":  "package tools\n\nfunc handleExec() {}\n",
		"docs/graph-notes.md":     "notes\n",
	})
	for _, cmd := range []string{"find . -name 'store.go'", "ls -R . | grep store", "git ls-files | grep store"} {
		out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: cmd, Cwd: repo, Caller: "test#file-" + cmd}, cmd, false)
		if !handled {
			t.Fatalf("%q: a file-name search must be answered by the index", cmd)
		}
		if !strings.Contains(out, "internal/graph/store.go") {
			t.Fatalf("%q: the answer must name the file:\n%s", cmd, out)
		}
	}
}

// TestGraphSearchAnswerSizeCap: a resposta pronta nunca passa do teto, e o rodapé (a saída de emergência) sobrevive.
func TestGraphSearchAnswerSizeCap(t *testing.T) {
	files := map[string]string{
		"pkg/a.go": "package pkg\n\n// CalcTotal soma.\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
	}
	for i := 0; i < 60; i++ {
		files[filepath.ToSlash(filepath.Join("callers", "c"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"))] =
			"package callers\n\nfunc Call" + string(rune('A'+i%26)) + string(rune('A'+i/26)) + "() {\n\tpkg.CalcTotal(" + string(rune('0'+i%10)) + ")\n}\n"
	}
	repo := newTestRepo(t, files)
	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn CalcTotal .", Cwd: repo, Caller: "test#cap"}, "grep -rn CalcTotal .", false)
	if !handled {
		t.Fatal("expected a graph answer")
	}
	if len(out) > graphAnswerMax {
		t.Fatalf("answer is %d bytes, over the %d cap", len(out), graphAnswerMax)
	}
	if !strings.Contains(out, "index covers code identifiers only") || !strings.Contains(out, "--raw") {
		t.Fatalf("the footer must survive the cut:\n%s", out)
	}
}

// TestGraphSearchBodyTooLong: definição única mas longa demais vira assinatura + faixa de linhas (read_file é mais barato).
func TestGraphSearchBodyTooLong(t *testing.T) {
	var b strings.Builder
	b.WriteString("package pkg\n\nfunc BigOne() {\n")
	for i := 0; i < 60; i++ {
		b.WriteString("\t_ = 1\n")
	}
	b.WriteString("}\n")
	repo := newTestRepo(t, map[string]string{"pkg/big.go": b.String()})
	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: "grep -rn BigOne .", Cwd: repo, Caller: "test#big"}, "grep -rn BigOne .", false)
	if !handled {
		t.Fatal("expected a graph answer")
	}
	if !strings.Contains(out, "read_file('pkg/big.go'") {
		t.Errorf("a long body must turn into the line range for read_file:\n%s", out)
	}
	if strings.Contains(out, "_ = 1") {
		t.Errorf("a long body must not be inlined:\n%s", out)
	}
}

// TestGraphSearchThroughExec: a interceptação mora no HandleExec — o agente recebe a resposta pronta sem passar pelo
// shell, e o escape do rodapé funciona pelo caminho de verdade (texto e argv), que foi como o agente contornou no C4.
func TestGraphSearchThroughExec(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
		"main.go":  "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
	})

	out, err := HandleExec(ExecArgs{Host: "local", Command: "findstr /s /n CalcTotal *.go", Cwd: repo, Caller: "test#e2e1"})
	if err != nil {
		t.Fatalf("a resposta pronta não deveria falhar: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, GraphMarkSearch) || !strings.Contains(out, "def: pkg/a.go:3-5 (func)") {
		t.Fatalf("a busca simples tem de sair respondida do grafo:\n%s", out)
	}
	// a linha do rodapé tem de rodar de verdade: mesmo comando + o escape que ele mostra
	raw, err := HandleExec(ExecArgs{Host: "local", Command: "findstr /s /n CalcTotal *.go --raw", Cwd: repo, Caller: "test#e2e2"})
	if err != nil {
		t.Fatalf("a busca crua pedida no rodapé falhou: %v\n%s", err, raw)
	}
	if strings.Contains(raw, GraphMarkSearch) {
		t.Errorf("com --raw a resposta pronta não pode aparecer:\n%s", raw)
	}
	if !strings.Contains(raw, GraphMarkRaw) {
		t.Errorf("o escape tem de se anunciar:\n%s", raw)
	}
	if !strings.Contains(raw, "a.go:3:") {
		t.Errorf("a busca crua tem de sair do shell:\n%s", raw)
	}
	// o mesmo escape por argv: a dica do rodapé vale nos dois caminhos
	argvOut, err := HandleExec(ExecArgs{Host: "local", Argv: []string{"findstr", "/s", "/n", "CalcTotal", "*.go", "--raw"}, Cwd: repo, Caller: "test#e2e3"})
	if err != nil {
		t.Fatalf("a busca crua por argv falhou: %v\n%s", err, argvOut)
	}
	if strings.Contains(argvOut, GraphMarkSearch) || !strings.Contains(argvOut, GraphMarkRaw) {
		t.Errorf("o escape por argv tem de valer também:\n%s", argvOut)
	}
}

// TestParsersPorFerramenta: a MESMA consulta interna sai de findstr, Select-String, rg e grep. Cada ferramenta tem o
// seu leitor (a sintaxe é diferente), mas o resultado é um searchReq só — é isso que faz o findstr e o Select-String
// do benchmark caírem no mesmo caminho do grep inteligente. '/m' do findstr é a lista de arquivos, como o '-l'.
func TestParsersPorFerramenta(t *testing.T) {
	casos := []struct {
		cmd  string
		want searchReq
	}{
		{`findstr /s /m /c:"CalcTotal" src\*.cs`, searchReq{pattern: "CalcTotal", paths: []string{`src\*.cs`}, list: true}},
		{`findstr /s /n CalcTotal src\Sub\*.cs`, searchReq{pattern: "CalcTotal", paths: []string{`src\Sub\*.cs`}}},
		{`Select-String -Path src\*.cs -Pattern CalcTotal`, searchReq{pattern: "CalcTotal", paths: []string{`src\*.cs`}}},
		{`Select-String -Path:src\*.cs -Pattern:CalcTotal`, searchReq{pattern: "CalcTotal", paths: []string{`src\*.cs`}}},
		{`rg -l CalcTotal src`, searchReq{pattern: "CalcTotal", paths: []string{"src"}, list: true}},
		{`grep -rn -c CalcTotal src`, searchReq{pattern: "CalcTotal", paths: []string{"src"}, count: true}},
	}
	for _, c := range casos {
		got, ok := parseIdentSearch(c.cmd)
		if !ok {
			t.Errorf("%q: devia ser uma busca por identificador", c.cmd)
			continue
		}
		if got.pattern != c.want.pattern || got.list != c.want.list || got.count != c.want.count ||
			strings.Join(got.paths, "|") != strings.Join(c.want.paths, "|") {
			t.Errorf("%q: got %+v, want %+v", c.cmd, got, c.want)
		}
	}

	// Negativos: o comando roda de verdade. Sem /s o findstr varre uma pasta só (o grafo responderia de menos),
	// /i muda quais linhas casam, /g tira os padrões de um arquivo e a regex não é identificador.
	no := []string{
		`findstr /n /m CalcTotal *.go`,
		`findstr /s /i /m CalcTotal *.go`,
		`findstr /s /m /g:padroes.txt CalcTotal`,
		`findstr /s /m /x CalcTotal *.go`,
		`Select-String -Path src -Pattern 'Calc.*'`,
	}
	for _, cmd := range no {
		if r, ok := parseIdentSearch(cmd); ok {
			t.Errorf("%q não pode virar resposta do grafo: %+v", cmd, r)
		}
	}
}

// TestGlobNarrow: um caminho com coringa só estreita a busca quando o coringa é o último componente e é '*<ext>' de
// uma linguagem indexada; fora disso a resposta do grafo poderia ser menor que a do comando e o comando roda.
func TestGlobNarrow(t *testing.T) {
	sim := map[string][2]string{
		`src\*.cs`:         {`src\`, ".cs"},
		`src/Serilog/*.cs`: {`src/Serilog/`, ".cs"},
		`*.go`:             {"", ".go"},
		`src\*.py`:         {`src\`, ".py"},
		`src\*.TS`:         {`src\`, ".TS"},
	}
	for in, want := range sim {
		dir, ext, ok := globNarrow(in)
		if !ok || dir != want[0] || ext != want[1] {
			t.Errorf("%q: got dir=%q ext=%q ok=%v, want dir=%q ext=%q", in, dir, ext, ok, want[0], want[1])
		}
	}
	nao := []string{
		`src\*`,        // sem extensão: o findstr acharia também o que o grafo não indexa
		`src\F*.cs`,    // coringa no meio do nome
		`src\*.txt`,    // extensão fora do índice
		`src\*.csx`,    // idem
		`src\*.cs.bak`, // idem
		`?oo.go`,       // interrogação
		`src\Foo.cs`,   // sem coringa: é o caminho de sempre
	}
	for _, in := range nao {
		if dir, ext, ok := globNarrow(in); ok {
			t.Errorf("%q não pode estreitar: dir=%q ext=%q", in, dir, ext)
		}
	}
}

// TestExtFiltroDaResposta: quando o caminho tinha coringa, a resposta fica na extensão dele — 'src\*.cs' não pode
// citar um arquivo .py de src.
func TestExtFiltroDaResposta(t *testing.T) {
	defs := []graph.Def{{File: "pkg/a.go"}, {File: "pkg/notes.py"}, {File: "pkg/sub/b.go"}, {File: "outra/c.go"}}
	files := func(ds []graph.Def) string {
		var names []string
		for _, d := range ds {
			names = append(names, d.File)
		}
		return strings.Join(names, "|")
	}
	if got := files(defsUnderSub(defs, "pkg", ".go")); got != "pkg/a.go|pkg/sub/b.go" {
		t.Errorf("ext .go sob pkg: got %q", got)
	}
	if got := files(defsUnderSub(defs, "pkg", "")); got != "pkg/a.go|pkg/notes.py|pkg/sub/b.go" {
		t.Errorf("sem extensão sob pkg: got %q", got)
	}
	if got := files(defsUnderSub(defs, "", "")); got != "pkg/a.go|pkg/notes.py|pkg/sub/b.go|outra/c.go" {
		t.Errorf("raiz sem filtro tem de aceitar tudo: got %q", got)
	}
	counts := map[string]int{"pkg/a.go": 2, "pkg/notes.py": 1, "outra/c.go": 3}
	got := countsUnderSub(counts, "pkg", ".go")
	if len(got) != 1 || got["pkg/a.go"] != 2 {
		t.Errorf("contagens com ext .go sob pkg: got %v", got)
	}
}

// TestGraphSearchCaminhoComCoringa: 'findstr /s /m /c:"X" pkg\*.go' (o comando do benchmark) responde pelo grafo,
// restrito à pasta do coringa e à extensão dele.
func TestGraphSearchCaminhoComCoringa(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"pkg/a.go":   "package pkg\n\nfunc CalcTotal(x int) int {\n\treturn x * 2\n}\n",
		"pkg/z.py":   "def CalcTotal():\n    pass\n",
		"main.go":    "package main\n\nfunc main() {\n\tpkg.CalcTotal(3)\n}\n",
		"outra/d.go": "package outra\n\nfunc D() {\n\tpkg.CalcTotal(1)\n}\n",
	})
	out, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: `findstr /s /m /c:"CalcTotal" pkg\*.go`, Cwd: repo, Caller: "test#glob1"}, `findstr /s /m /c:"CalcTotal" pkg\*.go`, false)
	if !handled {
		t.Fatalf("o findstr com coringa tinha de ser respondido pelo grafo:\n%s", out)
	}
	if !strings.Contains(out, "pkg/a.go") {
		t.Errorf("a resposta tem de citar a definição em pkg/a.go:\n%s", out)
	}
	for _, fora := range []string{"main.go", "outra/d.go", "pkg/z.py"} {
		if strings.Contains(out, fora) {
			t.Errorf("a resposta não pode sair da pasta/extensão do coringa (%s):\n%s", fora, out)
		}
	}

	// Coringa que o grafo não cobre (pasta inteira, sem extensão): o comando roda de verdade.
	cmd := `findstr /s /m /c:"CalcTotal" pkg\*`
	if _, handled, _ := tryGraphSearch(ExecArgs{Host: "local", Command: cmd, Cwd: repo, Caller: "test#glob2"}, cmd, false); handled {
		t.Errorf("%q: coringa sem extensão não pode ser respondido pelo grafo", cmd)
	}
}

// TestGraphSearchOverBudgetNote: quando a passada de frescor não cabe no teto, a nota diz qual dos dois casos foi —
// índice que ainda não existe ou índice atrasado — e nos dois a busca real é quem responde.
func TestGraphSearchOverBudgetNote(t *testing.T) {
	stale, fresh := overBudgetNote(true), overBudgetNote(false)
	if !strings.Contains(stale, "was behind") || strings.Contains(stale, "no code-graph index yet") {
		t.Errorf("índice atrasado: nota errada: %q", stale)
	}
	if !strings.Contains(fresh, "no code-graph index yet") {
		t.Errorf("índice inexistente: a nota tem de dizer isso: %q", fresh)
	}
	for _, n := range []string{stale, fresh} {
		if !strings.HasPrefix(n, GraphMarkIndex) || !strings.Contains(n, "raw search answers") || !strings.Contains(n, "900 ms") {
			t.Errorf("a nota tem de se anunciar, dizer o teto e a saída: %q", n)
		}
	}
}
