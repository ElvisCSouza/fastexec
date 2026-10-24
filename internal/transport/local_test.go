package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Matriz de testes do exec local: timeout real com limpeza da árvore (A), encodings mistos (C), argv sem shell (B),
// stdin com EOF, limite de captura e validação de cwd. Exigem node no PATH.

func procCount(t *testing.T, marker string) string {
	t.Helper()
	// o próprio teste usa argv: o script do PowerShell chega sem passar pelo parser do cmd
	script := fmt.Sprintf(`(@(Get-CimInstance Win32_Process -Filter "Name='node.exe'" | Where-Object { $_.CommandLine -like '*%s*' })).Count`, marker)
	res, err := ExecLocalArgv([]string{"powershell", "-NoProfile", "-Command", script}, "", "", 30*time.Second)
	if err != nil {
		t.Fatalf("consulta de processos falhou: %v", err)
	}
	return strings.TrimSpace(res.Stdout)
}

func TestTimeoutEncerraArvoreDeProcessos(t *testing.T) {
	marker := fmt.Sprintf("FXTEST_TREE_%d", time.Now().UnixNano())
	cmd := fmt.Sprintf(`node -e "setTimeout(function(){console.log('filho terminou')},30000)" %s`, marker)

	start := time.Now()
	res, err := ExecLocalWithInput(cmd, "", "", 1500*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("esperava TimedOut=true, veio %+v", res)
	}
	if elapsed > 4*time.Second {
		t.Errorf("deveria voltar em ~1,6s, levou %v", elapsed)
	}
	if strings.Contains(res.Stdout, "filho terminou") {
		t.Errorf("o filho não podia ter chegado ao fim: %q", res.Stdout)
	}
	if n := procCount(t, marker); n != "0" {
		t.Errorf("sobrou(aram) %s processo(s) node.exe órfão(s)", n)
	}
}

func TestFimNormalNaoDeixaOrfao(t *testing.T) {
	marker := fmt.Sprintf("FXTEST_ORFAO_%d", time.Now().UnixNano())
	// o comando principal termina logo; um neto continuaria vivo se o job não existisse
	script := fmt.Sprintf(`require('child_process').spawn(process.execPath,['-e','setTimeout(function(){},30000)','%s'],{detached:true,stdio:'ignore'}).unref();console.log('pai terminou')`, marker)
	res, err := ExecLocalArgv([]string{"node", "-e", script}, "", "", 10*time.Second)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("falha inesperada: %v %+v", err, res)
	}
	time.Sleep(500 * time.Millisecond) // o kernel encerra o job de forma assíncrona; margem curta
	if n := procCount(t, marker); n != "0" {
		t.Errorf("neto destacado sobreviveu ao fim da chamada (%s)", n)
	}
}

func TestEncodingMistoCmd(t *testing.T) {
	res, err := ExecLocalWithInput(`echo Suíte & comando_inexistente_para_teste_fastexec`, "", "", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "Suíte") {
		t.Errorf("stdout deveria conter Suíte, veio %q", res.Stdout)
	}
	if strings.ContainsRune(res.Stdout+res.Stderr, '\ufffd') {
		t.Errorf("caractere corrompido na saída: %q / %q", res.Stdout, res.Stderr)
	}
	low := strings.ToLower(res.Stderr)
	if !strings.Contains(low, "não é reconhecido") && !strings.Contains(low, "not recognized") {
		t.Errorf("mensagem do sistema ilegível: %q", res.Stderr)
	}
}

func TestEncodingUTF8DoNodeFicaIntacto(t *testing.T) {
	res, err := ExecLocalArgv([]string{"node", "-e", "console.log('Suíte çã ✓')"}, "", "", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "Suíte çã ✓" {
		t.Errorf("UTF-8 do node foi alterado: %q", res.Stdout)
	}
}

func TestDecodeMistoNoMesmoFluxo(t *testing.T) {
	// linha 1 em UTF-8, linha 2 em CP850 ("não" = 6e c6 6f), linha 3 em UTF-8, com CRLF preservado
	in := []byte("Suíte\r\nn\xc6o foi\r\nfim ✓\r\n")
	got := decodeMixed(in)
	want := "Suíte\r\nnão foi\r\nfim ✓\r\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestArgvNaoInterpretaMetacaracteres(t *testing.T) {
	dir := t.TempDir()
	lit := "teste > arquivo_inexistente_fx.txt && %TEMP% | x"
	res, err := ExecLocalArgv([]string{"node", "-e", "console.log(process.argv[1])", lit}, dir, "", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != lit {
		t.Errorf("argumento foi alterado: %q", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "arquivo_inexistente_fx.txt")); err == nil {
		t.Errorf("o redirecionamento foi interpretado e criou arquivo")
	}
}

func TestStdinSemInputRecebeEOF(t *testing.T) {
	script := `process.stdin.on('data',function(){});process.stdin.on('end',function(){console.log('eof')})`
	start := time.Now()
	res, err := ExecLocalArgv([]string{"node", "-e", script}, "", "", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.TimedOut || !strings.Contains(res.Stdout, "eof") {
		t.Errorf("stdin deveria dar EOF imediato: %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("demorou demais: %v", time.Since(start))
	}
}

func TestInputViaStdinComScriptComMetacaracteres(t *testing.T) {
	script := "var f = x => x > 2; console.log(f(5) && '%PATH%');\n"
	res, err := ExecLocalArgv([]string{"node", "-"}, "", script, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "%PATH%" {
		t.Errorf("script via stdin falhou: %q / %q", res.Stdout, res.Stderr)
	}
}

func TestCwdInexistente(t *testing.T) {
	_, err := ExecLocalWithInput("echo x", `C:\nao\existe\fastexec_teste`, "", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "cwd inexistente") {
		t.Errorf("esperava erro claro de cwd, veio %v", err)
	}
}

func TestCapBuffer(t *testing.T) {
	var b capBuffer
	b.Write([]byte("pequeno"))
	if string(b.Bytes()) != "pequeno" {
		t.Errorf("saída curta deve passar intacta: %q", b.Bytes())
	}

	b = capBuffer{}
	b.Write([]byte(strings.Repeat("a", capHeadMax)))
	b.Write([]byte(strings.Repeat("b", 2*capTailMax)))
	b.Write([]byte("FIM"))
	out := b.Bytes()
	if len(out) > capHeadMax+capTailMax+200 {
		t.Errorf("buffer passou do limite: %d", len(out))
	}
	if out[0] != 'a' || !strings.HasSuffix(string(out), "bbbFIM") {
		t.Errorf("começo e fim deveriam ser preservados")
	}
	if !strings.Contains(string(out), "descartados pelo limite de captura") {
		t.Errorf("faltou o marcador do corte")
	}
}

func TestSaidaInfinitaNaoEstouraMemoria(t *testing.T) {
	script := `while(true){process.stdout.write('x'.repeat(65536)+'\n')}`
	start := time.Now()
	res, err := ExecLocalArgv([]string{"node", "-e", script}, "", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Errorf("esperava timeout: %+v", res.ExitCode)
	}
	if len(res.Stdout) > capHeadMax+capTailMax+200 {
		t.Errorf("captura sem limite: %d bytes", len(res.Stdout))
	}
	if time.Since(start) > 6*time.Second {
		t.Errorf("demorou %v", time.Since(start))
	}
}
