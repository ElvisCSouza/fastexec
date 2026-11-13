package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/fastexec/internal/transport"
)

// semFerramenta/semDrive/semBash: cenário do host Windows sem Git Bash e sem as ferramentas Unix, sem depender do
// PATH da máquina que roda o teste.
func semAmbienteUnix(t *testing.T, existentes ...string) {
	t.Helper()
	origPath, origBash, origBashPath, origDrive := toolPath, bashOnHost, bashPathOnHost, driveExists
	toolPath = func(name string) (string, error) {
		for _, e := range existentes {
			if name == e {
				return `C:\Windows\System32\` + name + ".exe", nil
			}
		}
		return "", exec.ErrNotFound
	}
	// A sonda real acharia o Git Bash desta máquina e mudaria o caminho de execução dos testes.
	bashPathOnHost = func() string { return "" }
	bashOnHost = func() bool { return false }
	driveExists = func(l byte) bool { return strings.ToUpper(string(l)) == "W" || strings.ToUpper(string(l)) == "C" }
	t.Cleanup(func() {
		toolPath, bashOnHost, bashPathOnHost, driveExists = origPath, origBash, origBashPath, origDrive
	})
}

// comBashReal: cenário do host Windows que tem Git Bash de verdade. Pula quando a máquina não tem bash.
func comBashReal(t *testing.T) string {
	t.Helper()
	semAmbienteUnix(t)
	bashPathOnHost = probeBashPathOnHost
	bashOnHost = func() bool { return bashPathOnHost() != "" }
	bash := bashPathOnHost()
	if bash == "" {
		t.Skip("sem Git Bash nesta máquina")
	}
	return bash
}

// TestUnixShellHintFerramentaAusente: os comandos do benchmark no cmd.exe ganham nota com a alternativa nativa.
func TestUnixShellHintFerramentaAusente(t *testing.T) {
	semAmbienteUnix(t)
	casos := []struct {
		nome  string
		label string
		tool  string
	}{
		{"grep em pipeline", `grep -rnE "func main" . | wc -l`, "grep"},
		{"python -I -c", `python -I -c "print(1)"`, "python"},
		{"cat em pipeline", `cat README.md | head -20`, "cat"},
		{"segundo estágio", `type x.txt | sort | head -5`, "head"},
		{"rg ausente", `rg -n "func main" .`, "rg"},
		{"sed no lugar do read_file", `sed -n '1,20p' README.md`, "sed"},
	}
	for _, c := range casos {
		hint := unixShellHint(c.label)
		if !strings.Contains(hint, "[FastExec Hint]") || !strings.Contains(hint, "'"+c.tool+"'") {
			t.Errorf("%s: nota errada: %q", c.nome, hint)
		}
		if !strings.Contains(hint, "read_file") && !strings.Contains(hint, "pwsh") {
			t.Errorf("%s: nota sem alternativa nativa: %q", c.nome, hint)
		}
		if strings.Contains(hint, "bash exists") {
			t.Errorf("%s: sem bash no host a nota não pode oferecer bash: %q", c.nome, hint)
		}
	}
}

func TestUnixShellHintOfereceBashQuandoExiste(t *testing.T) {
	semAmbienteUnix(t)
	bashOnHost = func() bool { return true }
	if hint := unixShellHint(`grep -rn x .`); !strings.Contains(hint, "argv ['bash','-lc'") {
		t.Errorf("com bash no host a nota deve oferecê-lo: %q", hint)
	}
}

// TestUnixShellHintNaoDispara: comando que funciona, ferramenta presente no host e homônimo que não é o do Windows
// não produzem nota nenhuma.
func TestUnixShellHintNaoDispara(t *testing.T) {
	semAmbienteUnix(t, "grep", "wc")
	naoDispara := []struct {
		nome  string
		label string
	}{
		{"comando nativo", `dir /b`},
		{"go build", `go build ./...`},
		{"ferramenta presente", `grep -rn x . | wc -l`},
		{"find do Git Bash (não é o do System32)", `find . -name "*.go"`},
		{"sort do Git Bash", `sort -rn notas.txt`},
	}
	for _, c := range naoDispara {
		if hint := unixShellHint(c.label); hint != "" {
			t.Errorf("%s: não deveria haver nota: %q", c.nome, hint)
		}
	}
}

// TestUnixShellHintHomonimoDoWindows: find/sort do System32 com sintaxe Unix: existe o binário, mas é outra ferramenta.
func TestUnixShellHintHomonimoDoWindows(t *testing.T) {
	semAmbienteUnix(t, "find", "sort")
	for _, label := range []string{`find . -printf "%p\n"`, `find src -type f`, `sort -rn notas.txt | head`} {
		hint := unixShellHint(label)
		if !strings.Contains(hint, "System32") || !strings.Contains(hint, "Windows") {
			t.Errorf("%q: esperava a nota de homônimo do Windows: %q", label, hint)
		}
	}
	if hint := unixShellHint(`find /c "x" notas.txt`); hint != "" {
		t.Errorf("find com sintaxe do Windows não deve gerar nota: %q", hint)
	}
}

// TestUnixShellHintNaoFalaQuandoALinhaJaVaiParaUmShellUnix: 'bash -lc "... | wc -l"' é o caminho certo neste host;
// acusar o 'wc' do texto (que roda dentro do bash) seria um falso positivo.
func TestUnixShellHintNaoFalaQuandoALinhaJaVaiParaUmShellUnix(t *testing.T) {
	semAmbienteUnix(t, "bash")
	for _, label := range []string{`bash -lc "grep -rn x . | wc -l"`, `wsl grep -rn x .`, `sh -c "sort -rn x | head"`} {
		if hint := unixShellHint(label); hint != "" {
			t.Errorf("%q: linha que já vai para um shell Unix não gera nota: %q", label, hint)
		}
	}
}

// TestUnixToolAlternativas: a nota diz a alternativa da ferramenta que falhou, não um texto genérico.
func TestUnixToolAlternativas(t *testing.T) {
	semAmbienteUnix(t, "find", "sort")
	casos := []struct{ label, esperado string }{
		{`find . -printf "%p\n"`, "Get-ChildItem"},
		{`ls -la src`, "file_ops list"},
		{`grep -rn x .`, "Select-String"},
		{`awk '{print $1}' x.txt`, "pwsh"},
	}
	for _, c := range casos {
		hint := unixShellHint(c.label)
		if !strings.Contains(hint, c.esperado) {
			t.Errorf("%q: esperava %q na nota, veio %q", c.label, c.esperado, hint)
		}
	}
}

// TestUnixLabel: chamada por argv só olha o argv[0]; o rótulo da linha só vale quando não há argv (senão a nota
// culparia argumentos por serem texto Unix).
func TestUnixLabel(t *testing.T) {
	if got := unixLabel(`awk '{print $1}'`, true, []string{"pwsh", "-NoProfile", "-Command", "-"}); got != "pwsh" {
		t.Errorf("argv: esperava 'pwsh', veio %q", got)
	}
	if got := unixLabel(`grep -rn x .`, false, nil); got != `grep -rn x .` {
		t.Errorf("linha: esperava a própria linha, veio %q", got)
	}
	if got := unixLabel(`grep -rn x .`, true, nil); got != `grep -rn x .` {
		t.Errorf("argv vazio cai na linha, veio %q", got)
	}
}

// TestUnixShellNoteForGate: a nota só sai em host local, com erro e sem saída.
func TestUnixShellNoteForGate(t *testing.T) {
	semAmbienteUnix(t)
	label := `grep -rnE x . | wc -l`
	casos := []struct {
		nome string
		res  transport.ExecResult
	}{
		{"sucesso", transport.ExecResult{Local: true, ExitCode: 0}},
		{"com saída", transport.ExecResult{Local: true, ExitCode: 1, Stdout: "algo"}},
		{"host remoto", transport.ExecResult{Local: false, ExitCode: 1}},
		{"timeout", transport.ExecResult{Local: true, ExitCode: -1, TimedOut: true}},
	}
	for _, c := range casos {
		if got := unixShellNoteFor(label, &c.res); got != "" {
			t.Errorf("%s: não deveria haver nota: %q", c.nome, got)
		}
	}
	res := transport.ExecResult{Local: true, ExitCode: 1}
	if got := unixShellNoteFor(label, &res); !strings.Contains(got, "'grep'") {
		t.Errorf("erro sem saída no host local deveria gerar nota: %q", got)
	}
}

// TestGitBashToWindows: tradução do caminho estilo Git Bash, só com a unidade existente.
func TestGitBashToWindows(t *testing.T) {
	semAmbienteUnix(t)
	casos := []struct{ in, out, ex string }{
		{`cd /w/Data/bench && ls`, `cd W:\Data\bench && ls`, `'/w/Data/bench' -> 'W:\Data\bench'`},
		{`cat /c/tmp/a.txt`, `cat C:\tmp\a.txt`, `'/c/tmp/a.txt' -> 'C:\tmp\a.txt'`},
		{`ls /usr/local/bin`, `ls /usr/local/bin`, ""}, // U: não existe: nada de tradução
		{`dir /b`, `dir /b`, ""},                       // flag do cmd, não caminho
		{`curl http://x/c/y`, `curl http://x/c/y`, ""}, // URL não é caminho de unidade
	}
	for _, c := range casos {
		got, ex := gitBashToWindows(c.in)
		if got != c.out || ex != c.ex {
			t.Errorf("%q: esperava %q / %q, veio %q / %q", c.in, c.out, c.ex, got, ex)
		}
	}
}

// TestRemediateGitBashPathsNaoMexeEmShellUnix: quando a linha já vai para um shell Unix, o caminho Unix está certo.
func TestRemediateGitBashPathsNaoMexeEmShellUnix(t *testing.T) {
	semAmbienteUnix(t)
	if bashOnHost() {
		t.Fatal("cenário de teste sem bash")
	}
	for _, label := range []string{`bash -lc "ls /w/Data"`, `wsl ls /c/tmp`} {
		cmd, _, note := remediateGitBashPaths("local", label, "")
		if cmd != label || note != "" {
			t.Errorf("%q: comando de shell Unix não pode ser traduzido: %q / %q", label, cmd, note)
		}
	}
}

// TestExecUnixSemSaidaGanhaNota: o caso do ds-full3 — host local, linha Unix, exit 1 sem saída. Passa pela composição
// real (formatExecResult + execFailureHints) com o resultado que o cmd.exe devolve nesse caso, sem depender do PATH
// desta máquina (aqui o grep do Git Bash existe e o comando funcionaria de verdade).
func TestExecUnixSemSaidaGanhaNota(t *testing.T) {
	semAmbienteUnix(t)
	label := `grep -rnE "func main" . | wc -l`

	// exit 1 sem saída nenhuma: é o "no result (exit 1)" do benchmark.
	res := transport.ExecResult{Local: true, ExitCode: 1}
	out, err := formatExecResult(label, res)
	if err != nil {
		t.Fatalf("exit 1 sem saída é resultado normal, não erro: %v", err)
	}
	out = execFailureHints(out, label, label, &res)
	if !strings.Contains(out, "[FastExec Hint]") || !strings.Contains(out, "'grep'") {
		t.Errorf("saída sem nota do comando Unix: %q", out)
	}
	if !strings.Contains(out, NoResultText) {
		t.Errorf("a nota escondeu o resultado original: %q", out)
	}

	// erro na stderr (o "'grep' não é reconhecido" do cmd): a nota acompanha, não substitui.
	res = transport.ExecResult{Local: true, ExitCode: 1,
		Stderr: "'grep' is not recognized as an internal or external command,\r\noperable program or batch file."}
	out, _ = formatExecResult(label, res)
	out = execFailureHints(out, label, label, &res)
	if !strings.Contains(out, "is not recognized") {
		t.Errorf("a nota escondeu o erro original: %q", out)
	}
	if !strings.Contains(out, "[FastExec Hint]") {
		t.Errorf("erro do cmd sem a nota do comando Unix: %q", out)
	}
}

// TestExecComandoNativoNaoEAlterado: o caminho real (HandleExec no host local) não toca em comando que funciona.
func TestExecComandoNativoNaoEAlterado(t *testing.T) {
	semAmbienteUnix(t)
	out, err := HandleExec(ExecArgs{Host: "local", Command: `echo ok`})
	if err != nil || strings.TrimSpace(out) != "ok" || strings.Contains(out, "FastExec") {
		t.Errorf("comando nativo alterado: %q %v", out, err)
	}
}

// TestExecGitBashPathTraduzidoComAviso: '/w/...' vira unidade Windows antes de rodar, com nota de auto-remediação.
func TestExecGitBashPathTraduzidoComAviso(t *testing.T) {
	semAmbienteUnix(t)
	out, err := HandleExec(ExecArgs{Host: "local", Command: `echo /w/ReposHomeLab`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if strings.Contains(out, "was not recognized") || strings.Contains(out, "não é reconhecido") {
		t.Fatalf("o caminho não foi traduzido antes de rodar: %q", out)
	}
	if !strings.Contains(out, `W:\ReposHomeLab`) {
		t.Errorf("saída sem o caminho Windows: %q", out)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "'/w/ReposHomeLab'") {
		t.Errorf("saída sem a nota de tradução: %q", out)
	}
}

// TestUnixStyleLineClassifica: a decisão de shell é da linha inteira, antes de rodar. Sinal Unix forte sem sinal de
// cmd/PowerShell vai para o bash; qualquer sinal de cmd (ou dúvida) fica no cmd.exe, como antes desta correção.
func TestUnixStyleLineClassifica(t *testing.T) {
	semAmbienteUnix(t)
	casos := []struct {
		label string
		unix  bool
		nota  string
	}{
		{`grep -rn "func main" . | wc -l`, true, "pipeline com wc"},
		{`cat README.md | sort | head -5`, true, "sort no pipeline"},
		{`cut -d: -f1 /etc/passwd`, true, "cut"},
		{`ls -la`, true, "ls -la"},
		{`sed -n '1,20p' README.md`, true, "sed"},
		{`awk '{print $1}' x.txt`, true, "awk"},
		{`cd /w/Data/bench && ls`, true, "caminho estilo Git Bash"},
		{`echo $(date)`, true, "substituição de comando"},
		{`grep x y.txt > /dev/null`, true, "/dev/null"},
		{`find . -name "*.go"`, true, "find com flag Unix"},
		{`cat x.txt | grep y >/dev/null 2>&1`, true, "2>&1 com ferramenta Unix"},
		{`findstr /s /i foo *.go`, false, "findstr do cmd"},
		{`dir /b`, false, "dir"},
		{`type x.txt`, false, "type"},
		{`echo %TEMP%`, false, "variável do cmd"},
		{`for /r . %f in (*.go) do echo %f`, false, "for /r do cmd"},
		{`set X=1 && echo %X%`, false, "set do cmd"},
		{`Select-String -Path a.cpp -Pattern 'struct Row'`, false, "cmdlet do PowerShell"},
		{`powershell -c "ls"`, false, "powershell explícito"},
		{`type x.txt | sort | head -5`, false, "type vence o sort"},
		{`go build ./...`, false, "sem sinal nenhum"},
		{`echo ok`, false, "ambíguo"},
		{`python -c "print(1)"`, false, "ferramenta dos dois lados"},
		{`bash -lc "grep -rn x . | wc -l"`, false, "já vai para um shell Unix"},
		{`wsl grep -rn x .`, false, "wsl"},
		{"", false, "linha vazia"},
	}
	for _, c := range casos {
		if got := unixStyleLine(c.label); got != c.unix {
			t.Errorf("%s (%q): esperava unixStyleLine=%v, veio %v", c.nota, c.label, c.unix, got)
		}
	}
}

// TestBashPlanForSemBash: sem bash no host nada muda — o caminho segue o cmd.exe e a nota acionável de unixShellHint
// é quem orienta a próxima chamada.
func TestBashPlanForSemBash(t *testing.T) {
	semAmbienteUnix(t)
	argv, note, ok := bashPlanFor("local", `grep -rn x . | wc -l`)
	if ok || argv != nil || note != "" {
		t.Errorf("sem bash no host não pode haver plano: %v / %q / %v", argv, note, ok)
	}
	if hint := unixShellHint(`grep -rn x . | wc -l`); !strings.Contains(hint, "[FastExec Hint]") {
		t.Errorf("sem bash deve sobrar a nota acionável: %q", hint)
	}
}

// TestBashPlanForComBash: linha Unix com bash no host vira argv ['bash','-lc',<linha>] — uma execução só, sem passar
// pelo cmd.exe.
func TestBashPlanForComBash(t *testing.T) {
	bash := comBashReal(t)
	argv, note, ok := bashPlanFor("local", `grep -rn "func main" . | wc -l`)
	if !ok {
		t.Fatal("linha Unix com bash no host deveria virar plano de bash")
	}
	if len(argv) != 3 || argv[0] != bash || argv[1] != "-lc" || argv[2] != `grep -rn "func main" . | wc -l` {
		t.Errorf("argv errado: %#v", argv)
	}
	if !strings.Contains(note, "[FastExec Auto-Remediation]") || !strings.Contains(note, "ran in bash") ||
		!strings.Contains(note, "do not run it again") {
		t.Errorf("nota sem o que rodou e sem dizer que o resultado e final: %q", note)
	}

	// comando de cmd não vira plano, mesmo com bash presente.
	for _, label := range []string{`findstr /s /i foo *.go`, `dir /b`, `echo ok`, `powershell -c "ls"`} {
		if _, _, ok := bashPlanFor("local", label); ok {
			t.Errorf("%q: linha de cmd não pode virar plano de bash", label)
		}
	}
	// argv explícito nunca passa por aqui (o chamador só olha 'command').
	if _, _, ok := bashPlanFor("local", "bash -lc \"grep x . | wc -l\""); ok {
		t.Error("linha que já chama um shell Unix não pode ser convertida de novo")
	}
}

// TestBashPlanForTraduzCaminhoWindows: no ramo bash o 'W:\x' sem aspas perderia as barras, então o caminho é
// traduzido antes de rodar e a nota diz o que mudou.
func TestBashPlanForTraduzCaminhoWindows(t *testing.T) {
	comBashReal(t)
	argv, note, ok := bashPlanFor("local", `ls W:\ReposHomeLab\fastexec | head -3`)
	if !ok {
		t.Fatal("linha Unix com caminho Windows deveria virar plano de bash")
	}
	if argv[2] != `ls /w/ReposHomeLab/fastexec | head -3` {
		t.Errorf("caminho não traduzido para o bash: %q", argv[2])
	}
	if !strings.Contains(note, `'W:\ReposHomeLab\fastexec' -> '/w/ReposHomeLab/fastexec'`) {
		t.Errorf("nota sem a tradução feita: %q", note)
	}
}

// TestWindowsPathsToPosix: tradução Windows -> POSIX (o sentido inverso é gitBashToWindows) só com unidade existente.
func TestWindowsPathsToPosix(t *testing.T) {
	semAmbienteUnix(t)
	casos := []struct{ in, out, ex string }{
		{`ls W:\ReposHomeLab\fastexec`, `ls /w/ReposHomeLab/fastexec`, `'W:\ReposHomeLab\fastexec' -> '/w/ReposHomeLab/fastexec'`},
		{`cat "C:\tmp\a.txt"`, `cat "/c/tmp/a.txt"`, `'C:\tmp\a.txt' -> '/c/tmp/a.txt'`},
		{`ls /w/ReposHomeLab`, `ls /w/ReposHomeLab`, ""},   // já POSIX
		{`ls U:\nada`, `ls U:\nada`, ""},                   // unidade inexistente
		{`curl http://x/y:1/z`, `curl http://x/y:1/z`, ""}, // URL não é caminho de unidade
		{`echo ok`, `echo ok`, ""},
	}
	for _, c := range casos {
		got, ex := windowsPathsToPosix(c.in)
		if got != c.out || ex != c.ex {
			t.Errorf("%q: esperava %q / %q, veio %q / %q", c.in, c.out, c.ex, got, ex)
		}
	}
}

// TestExecUnixPipelineRodaNoBash: ponta a ponta no host local — a linha Unix roda de primeira no bash, com a nota, e
// o comando de cmd continua no cmd.exe.
func TestExecUnixPipelineRodaNoBash(t *testing.T) {
	comBashReal(t)
	out, err := HandleExec(ExecArgs{Host: "local", Command: `printf 'b\na\n' | sort | head -1`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "[FastExec Auto-Remediation]") || !strings.Contains(out, "ran in bash") {
		t.Fatalf("saída sem a nota do bash: %q", out)
	}
	if !strings.Contains(out, "\na\n") {
		t.Errorf("o pipeline não rodou: %q", out)
	}
	if strings.Contains(out, "[FastExec Hint]") {
		t.Errorf("depois de rodar no bash não cabe a dica de ferramenta ausente: %q", out)
	}

	// linha de cmd: nem nota de bash, nem troca de shell.
	out, err = HandleExec(ExecArgs{Host: "local", Command: `findstr /c:"a" nul`})
	if err != nil && !strings.Contains(out, "[FastExec") {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if strings.Contains(out, "ran in bash") {
		t.Errorf("linha de cmd não pode ir para o bash: %q", out)
	}
}

// TestExecPipelineDoBenchmarkRodaNoBash: a linha exata do ds-full4 que morria no cmd.exe ('grep ... | wc -l', com o
// guard de busca ampla e as exclusões de grep já aplicadas) roda de primeira no bash e devolve a contagem.
func TestExecPipelineDoBenchmarkRodaNoBash(t *testing.T) {
	comBashReal(t)
	dir := t.TempDir()
	body := "package main\n\nfunc main() {}\n\nfunc other() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleExec(ExecArgs{Host: "local", Cwd: dir, Command: `grep -rn "func main" . | wc -l`})
	if err != nil {
		t.Fatalf("HandleExec falhou: %v", err)
	}
	if !strings.Contains(out, "ran in bash") {
		t.Errorf("a linha do benchmark devia rodar no bash: %q", out)
	}
	if !strings.Contains(out, "\n1\n") && !strings.Contains(out, "\r\n1\r\n") {
		t.Errorf("a contagem do pipeline não saiu: %q", out)
	}
}

// TestLeituraNativaComCaminhoGitBash: numa linha que vai para o bash o 'command' fica em forma POSIX, mas a remediação
// nativa (read_file/file_ops) lê o caminho antes disso — ali ele precisa da unidade Windows. Sem isso 'cat /c/...'
// deixaria de ser lido e 'ls /c/...' deixaria de ser listado nativamente.
func TestLeituraNativaComCaminhoGitBash(t *testing.T) {
	comBashReal(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(file, []byte("conteudo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := strings.ToUpper(file[:1]); d != "C" && d != "W" {
		t.Skipf("diretório temporário em unidade fora do cenário de teste: %s", d)
	}
	posix := "/" + strings.ToLower(file[:1]) + strings.ReplaceAll(file[2:], `\`, "/")

	out, err := HandleExec(ExecArgs{Host: "local", Command: "cat " + posix})
	if err != nil || !strings.Contains(out, "conteudo") {
		t.Errorf("'cat %s' devia ser lido nativamente: err=%v out=%q", posix, err, out)
	}
	if !strings.Contains(out, "was run as the native 'read_file'") {
		t.Errorf("saída sem a remediação de leitura: %q", out)
	}

	out, err = HandleExec(ExecArgs{Host: "local", Command: "ls " + posix[:strings.LastIndex(posix, "/")]})
	if err != nil || !strings.Contains(out, "p.txt") {
		t.Errorf("'ls <dir posix>' devia ser listado nativamente: err=%v out=%q", err, out)
	}
}
