package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// doctor_csharp_test.go: a seção C# do `lsp doctor` e o `config set dotnet_sdk`,
// ponta a ponta no processo filho (o CLI chama os.Exit, então não dá para chamar
// Main aqui dentro).
//
// O SDK é sempre um que o teste monta e passa por FASTEXEC_DOTNET_SDK: sem isso
// o resultado dependeria do que existe na máquina que roda o teste, e um doctor
// que só funciona na máquina de quem escreveu não está testado.

// fakeSDK escreve uma raiz de SDK .NET com as versões dadas e devolve o caminho.
// O executável nunca é rodado: só precisa existir para a pasta contar.
func fakeSDK(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	exe := "dotnet"
	if runtime.GOOS == "windows" {
		exe = "dotnet.exe"
	}
	if err := os.WriteFile(filepath.Join(root, exe), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if err := os.MkdirAll(filepath.Join(root, "sdk", v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fakeRepo monta um repositório C# mínimo: um .csproj e, quando pedido, o
// global.json e o assets file que o restore deixaria.
func fakeRepo(t *testing.T, pin, roll string, restored bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "demo.csproj"), []byte("<Project/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if pin != "" {
		body := `{"sdk":{"version":"` + pin + `"`
		if roll != "" {
			body += `,"rollForward":"` + roll + `"`
		}
		if err := os.WriteFile(filepath.Join(root, "global.json"), []byte(body+"}}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if restored {
		dir := filepath.Join(root, "obj")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "project.assets.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// --- a seção C# do doctor ---

// runDoctorWithSDK roda o CLI num filho com o SDK do teste apontado por
// FASTEXEC_DOTNET_SDK. O ambiente do filho é montado a partir do ambiente de quem
// roda o teste, mas a variável é reescrita aqui: a resposta não pode mudar porque
// a máquina tem um SDK.
func runDoctorWithSDK(t *testing.T, sdk string, args ...string) (string, int) {
	t.Helper()
	return runLSPHome(t, t.TempDir(), []string{"FASTEXEC_DOTNET_SDK=" + sdk}, args...)
}

// TestLspDoctorSaysTheProjectIsReady: o caso que faz o doctor valer a pena. Uma
// saída que só sabe apontar problema vira ruído e ninguém lê.
func TestLspDoctorSaysTheProjectIsReady(t *testing.T) {
	sdk := fakeSDK(t, "8.0.428")
	repo := fakeRepo(t, "", "", true)

	out, code := runDoctorWithSDK(t, sdk, "lsp", "doctor", repo)
	if code != 0 {
		t.Fatalf("doctor devia sair com 0, saiu %d:\n%s", code, out)
	}
	if !strings.Contains(out, "C#: project ready") {
		t.Fatalf("o doctor devia dizer que o projeto carrega:\n%s", out)
	}
}

// TestLspDoctorNamesTheRestoreThatNeverRan: a causa que o usuário via só como
// "by name" na consulta, agora dita antes de ele perguntar.
func TestLspDoctorNamesTheRestoreThatNeverRan(t *testing.T) {
	sdk := fakeSDK(t, "8.0.428")
	repo := fakeRepo(t, "", "", false)

	out, code := runDoctorWithSDK(t, sdk, "lsp", "doctor", repo)
	if code != 0 {
		t.Fatalf("problema de projeto não é erro de exit no doctor, saiu %d:\n%s", code, out)
	}
	if !strings.Contains(out, "restore needed: run dotnet restore (demo.csproj)") {
		t.Fatalf("o doctor devia nomear o restore que falta:\n%s", out)
	}
	if !strings.Contains(out, "C# project (") {
		t.Fatalf("o doctor devia abrir uma seção C# quando há repositório C#:\n%s", out)
	}
}

// TestLspDoctorExplainsThePinnedSDK: o caso que motivou a task — o global.json
// fixa um SDK que a máquina não tem, e o doctor diz qual e o que fazer.
func TestLspDoctorExplainsThePinnedSDK(t *testing.T) {
	sdk := fakeSDK(t, "8.0.100")
	repo := fakeRepo(t, "7.0.102", "", true)

	out, code := runDoctorWithSDK(t, sdk, "lsp", "doctor", repo)
	if code != 0 {
		t.Fatalf("saiu %d:\n%s", code, out)
	}
	want := "global.json pins SDK 7.0.102; installed: 8.0.100 (set rollForward or install 7.0.102)"
	if !strings.Contains(out, want) {
		t.Fatalf("o doctor devia dizer %q:\n%s", want, out)
	}
}

// TestLspDoctorStaysQuietAboutCsharpOutsideACsharpRepo: rodar o doctor na pasta
// de casa não pode virar um parágrafo sobre C#.
func TestLspDoctorStaysQuietAboutCsharpOutsideACsharpRepo(t *testing.T) {
	sdk := fakeSDK(t, "8.0.428")

	out, code := runDoctorWithSDK(t, sdk, "lsp", "doctor")
	if code != 0 {
		t.Fatalf("saiu %d:\n%s", code, out)
	}
	if strings.Contains(out, "C# project") {
		t.Fatalf("sem repositório C# o doctor não devia falar de C#:\n%s", out)
	}
}

// TestLspDoctorRejectsASecondFolder: um caminho a mais é erro de uso, não uma
// checagem silenciosa do primeiro.
func TestLspDoctorRejectsASecondFolder(t *testing.T) {
	out, code, _ := runLSP(t, "lsp", "doctor", t.TempDir(), t.TempDir())
	if code == 0 {
		t.Fatalf("dois caminhos deviam falhar:\n%s", out)
	}
	if !strings.Contains(out, "at most one folder") {
		t.Fatalf("o erro devia dizer que só cabe uma pasta:\n%s", out)
	}
}

// --- `config set dotnet_sdk` ---

// TestConfigSetDotnetSDKIsReadBackByTheDoctor: o caminho inteiro, que é o que
// importa — gravar a pasta e o doctor seguinte usá-la, sem variável de ambiente
// nenhuma no meio. É o motivo de o campo existir: o daemon não herda o ambiente
// com que o usuário testou.
func TestConfigSetDotnetSDKIsReadBackByTheDoctor(t *testing.T) {
	sdk := fakeSDK(t, "8.0.428")
	repo := fakeRepo(t, "", "", true)
	// Uma casa só para os três comandos: o config é o estado entre eles.
	home := t.TempDir()

	out, code := runLSPHome(t, home, nil, "config", "set", "dotnet_sdk", sdk)
	if code != 0 {
		t.Fatalf("config set falhou (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "set dotnet_sdk = "+sdk) {
		t.Fatalf("config set devia confirmar o que gravou:\n%s", out)
	}
	if !strings.Contains(out, home) {
		t.Fatalf("config set devia dizer em que arquivo gravou:\n%s", out)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("config set não gravou o arquivo: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("o arquivo gravado não é JSON: %v\n%s", err, body)
	}
	if doc["dotnet_sdk"] != sdk {
		t.Fatalf("dotnet_sdk no arquivo = %v, want %q", doc["dotnet_sdk"], sdk)
	}

	// O mesmo arquivo, de novo, agora lido pelo doctor. FASTEXEC_DOTNET_SDK não
	// está setada neste teste: quem responde é o config.
	out, code = runLSPHome(t, home, nil, "config", "show")
	if code != 0 || !strings.Contains(out, "dotnet_sdk:   "+sdk+" (ok)") {
		t.Fatalf("config show devia mostrar a pasta e que ela é um SDK:\n%s", out)
	}
	out, code = runLSPHome(t, home, nil, "lsp", "doctor", repo)
	if code != 0 || !strings.Contains(out, "C#: project ready") {
		t.Fatalf("o doctor devia usar o SDK do config:\n%s", out)
	}
}

// TestConfigSetWarnsAboutAFolderThatIsNotAnSDK: gravar é permitido (descompactar
// o SDK depois é uma ordem normal das coisas), mas o aviso é imediato e nomeia a
// pasta — o contrário é o usuário achando que configurou.
func TestConfigSetWarnsAboutAFolderThatIsNotAnSDK(t *testing.T) {
	empty := t.TempDir()
	out, code, home := runLSP(t, "config", "set", "dotnet_sdk", empty)
	if code != 0 {
		t.Fatalf("uma pasta sem SDK devia ser gravada mesmo assim (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "has no dotnet executable with an sdk/ folder") {
		t.Fatalf("faltou o aviso:\n%s", out)
	}
	if body, err := os.ReadFile(filepath.Join(home, "config.json")); err != nil || !strings.Contains(string(body), "dotnet_sdk") {
		t.Fatalf("o valor devia ficar no arquivo de qualquer forma (err=%v):\n%s", err, body)
	}
}

// TestConfigSetClearsWithDash: tirar o que foi posto precisa ser possível sem
// editar o arquivo à mão.
func TestConfigSetClearsWithDash(t *testing.T) {
	sdk := fakeSDK(t, "8.0.428")
	if _, code, _ := runLSP(t, "config", "set", "dotnet_sdk", sdk); code != 0 {
		t.Fatal("o set inicial falhou")
	}
	out, code, home := runLSP(t, "config", "set", "dotnet_sdk", "-")
	if code != 0 {
		t.Fatalf("limpar falhou (%d):\n%s", code, out)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "dotnet_sdk") {
		t.Fatalf("a chave devia sair do arquivo:\n%s", body)
	}
}

// TestConfigSetRefusesAnotherKey: o comando grava uma coisa só, e dizer isso é
// melhor do que aceitar a chave e não fazer nada.
func TestConfigSetRefusesAnotherKey(t *testing.T) {
	out, code, home := runLSP(t, "config", "set", "default_host", "server")
	if code == 0 {
		t.Fatalf("chave desconhecida devia falhar:\n%s", out)
	}
	if !strings.Contains(out, "dotnet_sdk") {
		t.Fatalf("o erro devia dizer qual chave existe:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "config.json")); err == nil {
		t.Fatal("nada devia ter sido gravado")
	}
}

// TestWriteConfigSettingKeepsTheRestOfTheFile: o `set` não pode ser um
// reescrevedor do arquivo inteiro. Carregar pelo config.Load recusaria uma chave
// desconhecida e gravar por cima apagaria o que a pessoa escreveu à mão.
func TestWriteConfigSettingKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := "{\n  \"hosts\": {\n    \"server\": {\n      \"addr\": \"10.0.0.5\"\n    }\n  },\n  \"minha_chave\": \"feita à mão\"\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	sdk := fakeSDK(t, "8.0.428")
	stored, cleared, err := writeConfigSetting(path, "dotnet_sdk", sdk)
	if err != nil || cleared || stored != sdk {
		t.Fatalf("writeConfigSetting = (%q, %v, %v)", stored, cleared, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("o arquivo ficou inválido: %v\n%s", err, body)
	}
	if doc["minha_chave"] != "feita à mão" {
		t.Fatalf("a chave escrita à mão sumiu:\n%s", body)
	}
	if _, ok := doc["hosts"]; !ok {
		t.Fatalf("os hosts sumiram:\n%s", body)
	}
	if doc["dotnet_sdk"] != sdk {
		t.Fatalf("dotnet_sdk = %v", doc["dotnet_sdk"])
	}

	// Um arquivo ilegível é recusado inteiro: gravar por cima perderia o que
	// está lá, e o erro diz que nada foi escrito.
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeConfigSetting(broken, "dotnet_sdk", sdk); err == nil {
		t.Fatal("JSON inválido devia ser recusado")
	} else if !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("o erro devia dizer que nada foi escrito: %v", err)
	}
	if body, _ := os.ReadFile(broken); string(body) != "{" {
		t.Fatalf("o arquivo quebrado foi mexido: %s", body)
	}
}
