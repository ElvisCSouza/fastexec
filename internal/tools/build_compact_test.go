package tools

import (
	"strings"
	"testing"
)

// Amostras reais e pequenas, copiadas da saída dos comandos (go, dotnet, tsc, npm/jest, cargo). O que estes testes
// garantem, em uma frase: nenhuma linha de erro/aviso some, e o resto encolhe.

const goTestOKSample = `ok  	homelab/fastexec/internal/tools	1.284s
?   	homelab/fastexec/internal/config	[no test files]
ok  	homelab/fastexec/cli	0.512s
ok  	homelab/fastexec/internal/telemetry	(cached)
`

const goTestFailSample = `ok  	homelab/fastexec/internal/config	0.041s
?   	homelab/fastexec/internal/telemetry	[no test files]
ok  	homelab/fastexec/cli	0.512s
--- FAIL: TestBuildCompact (0.00s)
    build_compact_test.go:12: got 1, want 2
FAIL
exit status 1
FAIL	homelab/fastexec/internal/tools	1.284s
`

const dotnetBuildOKSample = `MSBuild version 17.8.3+195e7f5a2 for .NET
  Determining projects to restore...
  Restored C:\src\App\App.csproj (in 45 ms).
  App -> C:\src\App\bin\Debug\net8.0\App.dll

Build succeeded.
    0 Warning(s)
    0 Error(s)

Time Elapsed 00:00:03.21
`

const dotnetBuildFailSample = `MSBuild version 17.8.3+195e7f5a2 for .NET
  Determining projects to restore...
  Restored C:\src\App\App.csproj (in 45 ms).
C:\src\App\Program.cs(12,20): error CS0103: The name 'Foo' does not exist in the current context [C:\src\App\App.csproj]

Build FAILED.
    0 Warning(s)
    1 Error(s)

Time Elapsed 00:00:02.10
`

const tscFailSample = `src/app.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.
src/util.ts(4,3): error TS2554: Expected 2 arguments, but got 1.
src/deep/nested/thing.ts(1,1): error TS1005: ';' expected.

Found 3 errors in 3 files.
`

const jestFailSample = `> app@1.0.0 test
> jest

 PASS  src/a.test.js
 PASS  src/b.test.js
 FAIL  src/c.test.js
  ● math › adds

    expect(received).toBe(expected)

    Expected: 3
    Received: 2

      10 | test('adds', () => {
    > 11 |   expect(add(1, 1)).toBe(3)
         |                    ^
      12 | })

Test Suites: 1 failed, 2 passed, 3 total
Tests:       1 failed, 3 passed, 4 total
Time:        1.284 s
`

const cargoFailSample = `   Compiling serde v1.0.197
   Compiling app v0.1.0 (/home/u/app)
warning: unused variable: ` + "`x`" + `
 --> src/main.rs:3:9
  |
3 |     let x = 1;
  |         ^ help: if this is intentional, prefix it with an underscore: ` + "`_x`" + `
  |

    Finished dev [unoptimized + debuginfo] target(s) in 2.31s
`

// errorLines devolve as linhas que decidem a próxima ação: erro/aviso ou diagnóstico com arquivo:linha.
func errorLines(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(l)
		if trim != "" && (buildErrRe.MatchString(trim) || codeLocRe.MatchString(trim)) {
			out = append(out, trim)
		}
	}
	return out
}

func TestBuildCompactKeepsEveryErrorLine(t *testing.T) {
	cases := []struct {
		name    string
		label   string
		exit    int
		stdout  string
		stderr  string
		shrinks bool // saída já curta (o caso do tsc) não é mexida, e isso também é resposta certa
	}{
		{"go_fail", "go test ./...", 1, goTestFailSample, "", true},
		{"dotnet_fail", "dotnet build", 1, dotnetBuildFailSample, "", true},
		{"tsc_fail", "tsc --noEmit", 1, tscFailSample, "", false},
		{"jest_fail", "npm test", 1, jestFailSample, "", true},
		{"cargo_warn", "cargo build", 0, "", cargoFailSample, false},
		{"go_ok", "go test ./...", 0, goTestOKSample, "", true},
		{"dotnet_ok", "dotnet build", 0, dotnetBuildOKSample, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
			t.Setenv("FASTEXEC_NO_BUILD_COMPACT", "")
			body, note, ok := compactBuildOutput(tc.label, tc.exit, tc.stdout, tc.stderr)
			raw := tc.stdout + tc.stderr
			if !ok {
				if tc.shrinks {
					t.Fatalf("saída não compactada (ok=false) para %q", tc.label)
				}
				if body != "" || note != "" {
					t.Fatalf("ok=false tem de devolver texto vazio (a íntegra fica com o chamador): %q / %q", body, note)
				}
				return
			}
			if !tc.shrinks {
				t.Errorf("%q não tinha o que encolher (ganho < 2 linhas) e foi mexido mesmo assim", tc.label)
			}
			if note == "" {
				t.Fatal("compactou sem avisar: toda compactação tem de se anunciar")
			}
			for _, want := range errorLines(t, raw) {
				if !strings.Contains(body, want) {
					t.Errorf("erro/aviso sumiu na compactação:\n  %s\n--- compactado ---\n%s", want, body)
				}
			}
			if len(body) >= len(raw) {
				t.Errorf("não encolheu: %d bytes → %d bytes", len(raw), len(body))
			}
			if strings.Contains(body, "ok  \thomelab/fastexec/cli") {
				t.Error("linha de pacote que passou continuou no texto compactado")
			}
		})
	}
}

func TestBuildCompactSuccessCollapsesToSummary(t *testing.T) {
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
	body, note, ok := compactBuildOutput("go test ./...", 0, goTestOKSample, "")
	if !ok {
		t.Fatal("sucesso total do go test devia virar resumo")
	}
	lines := nonEmptyLines(body)
	if lines != 1 {
		t.Fatalf("sucesso total devia caber em 1 linha, veio %d:\n%s", lines, body)
	}
	if !strings.Contains(body, "3 packages ok") || !strings.Contains(body, "1 without tests") || !strings.Contains(body, "0 failures") {
		t.Errorf("resumo do go test sem as contagens que o comando não imprime: %q", body)
	}
	if !strings.Contains(note, "use --raw") {
		t.Errorf("aviso tem de dizer como pedir a íntegra: %q", note)
	}
}

func TestBuildCompactLeavesNonBuildCommandsAlone(t *testing.T) {
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
	for _, label := range []string{
		"go run ./cmd/server",
		"npm start",
		"npm run dev",
		"dotnet run",
		"vite --watch",
		`pwsh -Command "& 'C:\Program Files\dotnet\dotnet.exe' run"`,
		"ls -la",
		"git status",
	} {
		if _, _, ok := compactBuildOutput(label, 0, goTestOKSample, ""); ok {
			t.Errorf("comando fora da lista foi compactado: %q", label)
		}
	}
}

func TestBuildCompactEnvEscape(t *testing.T) {
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "1")
	if _, _, ok := compactBuildOutput("go test ./...", 0, goTestOKSample, ""); ok {
		t.Error("FASTEXEC_NO_OUTPUT_FILTER devia devolver a saída íntegra")
	}
	t.Setenv("FASTEXEC_NO_OUTPUT_FILTER", "")
	t.Setenv("FASTEXEC_NO_BUILD_COMPACT", "1")
	if _, _, ok := compactBuildOutput("go test ./...", 0, goTestOKSample, ""); ok {
		t.Error("FASTEXEC_NO_BUILD_COMPACT devia devolver a saída íntegra")
	}
}

func TestStripBuildRawEscape(t *testing.T) {
	cases := []struct {
		command string
		want    string
		wantRaw bool
	}{
		{"go test ./... --raw", "go test ./...", true},
		{"dotnet build --raw", "dotnet build", true},
		{"go test ./... -run TestX -- --raw", "go test ./... -run TestX -- --raw", false}, // depois de '--' é do programa
		{"grep -r foo . --raw", "grep -r foo . --raw", false},                             // não é comando de build
		{"go run ./cmd/server --raw", "go run ./cmd/server --raw", false},                 // não é build/teste
		{"go test ./...", "go test ./...", false},
	}
	for _, tc := range cases {
		args := ExecArgs{Host: "local", Command: tc.command}
		label, stripped := stripBuildRawEscape(&args)
		if stripped != tc.wantRaw {
			t.Errorf("%q: raw=%v, quer %v", tc.command, stripped, tc.wantRaw)
		}
		if label != tc.want || args.Command != tc.want {
			t.Errorf("%q: label=%q args=%q, quer %q", tc.command, label, args.Command, tc.want)
		}
	}

	// argv: o marcador só sai do comando de build reconhecido
	args := ExecArgs{Host: "local", Argv: []string{"go", "test", "./...", "--raw"}}
	if _, stripped := stripBuildRawEscape(&args); !stripped {
		t.Fatal("argv de go test com --raw devia marcar raw")
	}
	if len(args.Argv) != 3 || args.Argv[2] != "./..." {
		t.Errorf("argv devia perder o --raw: %v", args.Argv)
	}
	args = ExecArgs{Host: "local", Argv: []string{"curl", "-s", "url", "--raw"}}
	if _, stripped := stripBuildRawEscape(&args); stripped {
		t.Error("argv que não é de build não pode perder o --raw")
	}
	if len(args.Argv) != 4 {
		t.Errorf("argv alheio foi mexido: %v", args.Argv)
	}
}

func TestCompactOutputSkipsWhenFailureHasNoErrorSignal(t *testing.T) {
	// exit != 0 sem nenhum sinal de erro reconhecido: não se compacta às cegas.
	body, note, ok := compactBuildOutput("go build", 2, "linha neutra um\nlinha neutra dois\nlinha neutra três\n", "")
	if ok {
		t.Fatalf("falha sem erro reconhecido não devia compactar: %q / %q", body, note)
	}
}
