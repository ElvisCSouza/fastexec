//go:build windows

package transport

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Testes do backend Windows da tool job: ciclo de vida, independência do exec, limite, TTL e término espontâneo.
// Estado isolado em pasta temporária (FASTEXEC_JOBS_DIR). Exigem node no PATH.

func useTempJobs(t *testing.T) {
	t.Helper()
	// Pasta própria em vez de t.TempDir(): o RemoveAll do cleanup do TempDir entra em corrida com a goroutine
	// recordExitWhenDone, que grava o rc do job no metadado logo depois do kill — se a escrita cair no meio do
	// RemoveAll, ele falha com "a pasta não está vazia" e marca o teste como falho de forma intermitente. O cleanup
	// aqui para os jobs e tenta remover de novo até a escrita pendente acabar (nenhum teste deixa node órfão).
	dir, err := os.MkdirTemp("", "fxjobs")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FASTEXEC_JOBS_DIR", dir)
	t.Cleanup(func() {
		jobs, _ := LocalJobList()
		for _, j := range jobs {
			_, _ = LocalJobStop(j.Meta.ID)
		}
		for i := 0; i < 20; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

func tickJob(marker string) []string {
	return []string{"node", "-e", "setInterval(function(){console.log('tick')},1000)", marker}
}

func TestLocalJobCicloDeVida(t *testing.T) {
	useTempJobs(t)
	marker := fmt.Sprintf("FXTEST_JOB_%d", time.Now().UnixNano())

	start := time.Now()
	m, err := StartLocalJob("", tickJob(marker), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("start deveria voltar na hora: %v", time.Since(start))
	}

	st, err := LocalJobStatus(m.ID)
	if err != nil || st.State != jobStateRunning || st.Active == 0 {
		t.Fatalf("esperava RUNNING com processo ativo: %+v %v", st, err)
	}

	time.Sleep(3 * time.Second)
	out, err := TailLocalJob(m.ID, 5)
	if err != nil || strings.Count(out, "tick") < 2 {
		t.Errorf("tail deveria mostrar os ticks: %q %v", out, err)
	}
	if n := procCount(t, marker); n == "0" {
		t.Errorf("o processo deveria estar vivo antes do stop")
	}

	stopped, err := LocalJobStop(m.ID)
	if err != nil || stopped.State != jobStateStopped {
		t.Fatalf("stop falhou: %+v %v", stopped, err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := procCount(t, marker); n != "0" {
		t.Errorf("sobrou(aram) %s processo(s) node.exe depois do stop", n)
	}
	st, _ = LocalJobStatus(m.ID)
	if st.State != jobStateStopped {
		t.Errorf("status depois do stop: %s", st.State)
	}
}

func TestLocalJobSobreviveAoExec(t *testing.T) {
	useTempJobs(t)
	server := `require('http').createServer(function(q,s){s.end('resposta do job')}).listen(0,'127.0.0.1',function(){console.log('PORT '+this.address().port)})`
	m, err := StartLocalJob("", []string{"node", "-e", server}, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var port string
	for i := 0; i < 100 && port == ""; i++ {
		out, _ := TailLocalJob(m.ID, 5)
		if _, after, ok := strings.Cut(out, "PORT "); ok {
			port = strings.TrimSpace(strings.Fields(after)[0])
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == "" {
		t.Fatal("o servidor do job não anunciou a porta")
	}

	// chamada exec separada: quando ela termina, o job NÃO pode ter morrido
	res, err := ExecLocalArgv([]string{"node", "-e", "fetch('http://127.0.0.1:'+process.argv[1]).then(function(r){return r.text()}).then(console.log)", port}, "", "", 15*time.Second)
	if err != nil || !strings.Contains(res.Stdout, "resposta do job") {
		t.Errorf("o servidor deveria responder depois do fim do exec: %+v %v", res, err)
	}
	if st, _ := LocalJobStatus(m.ID); st.State != jobStateRunning {
		t.Errorf("job deveria seguir RUNNING: %s", st.State)
	}
}

func TestLocalJobLimiteDeCinco(t *testing.T) {
	useTempJobs(t)
	marker := fmt.Sprintf("FXTEST_LIM_%d", time.Now().UnixNano())
	for i := 0; i < LocalJobMaxActive; i++ {
		if _, err := StartLocalJob("", tickJob(marker), "", ""); err != nil {
			t.Fatalf("job %d deveria iniciar: %v", i+1, err)
		}
	}
	_, err := StartLocalJob("", tickJob(marker), "", "")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("limit of %d active local Windows jobs", LocalJobMaxActive)) || !strings.Contains(err.Error(), "stop") {
		t.Errorf("o próximo deveria falhar orientando a parar um job: %v", err)
	}
}

func TestLocalJobTTLEncerraEMarca(t *testing.T) {
	useTempJobs(t)
	old := localJobTTL
	localJobTTL = time.Second
	t.Cleanup(func() { localJobTTL = old })

	marker := fmt.Sprintf("FXTEST_TTL_%d", time.Now().UnixNano())
	m, err := StartLocalJob("", []string{"node", "-e", "setInterval(function(){},1000)", marker}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	st, err := LocalJobStatus(m.ID)
	if err != nil || st.State != jobStateExpired {
		t.Fatalf("esperava EXPIRED: %+v %v", st, err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := procCount(t, marker); n != "0" {
		t.Errorf("job expirado deveria ter sido encerrado (%s)", n)
	}
}

func TestLocalJobTerminaSozinho(t *testing.T) {
	useTempJobs(t)
	m, err := StartLocalJob("", []string{"node", "-e", "console.log('rapido')"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, finished, err := LocalJobWait(m.ID, 15*time.Second)
	if err != nil || !finished || st.State != jobStateExited {
		t.Fatalf("esperava EXITED: %+v finished=%v %v", st, finished, err)
	}
	if out, _ := TailLocalJob(m.ID, 5); !strings.Contains(out, "rapido") {
		t.Errorf("tail deveria ter a saída do processo: %q", out)
	}
}

// Regressão: o log do job era aberto só para anexar (FILE_APPEND_DATA) e o bash do Git (msys) não escrevia
// nele: log vazio e rc=1 no último echo. Com leitura/escrita normal a saída chega ao log e o rc é 0.
func TestLocalJobBashEscreveNoLog(t *testing.T) {
	useTempJobs(t)
	bash := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(bash); err != nil {
		t.Skip("Git Bash não instalado")
	}
	m, err := StartLocalJob("", []string{bash, "-c", "echo ola; echo fim"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, finished, err := LocalJobWait(m.ID, 15*time.Second)
	if err != nil || !finished || st.State != jobStateExited {
		t.Fatalf("esperava EXITED: %+v %v", st, err)
	}
	if out, _ := TailLocalJob(m.ID, 5); !strings.Contains(out, "ola") || !strings.Contains(out, "fim") {
		t.Errorf("o log deveria ter a saída do bash: %q", out)
	}
	if st.Meta.ExitCode == nil || *st.Meta.ExitCode != 0 {
		t.Errorf("rc deveria ser 0: %v", st.Meta.ExitCode)
	}
}

func TestLocalJobComandoQueFalhaNaHora(t *testing.T) {
	useTempJobs(t)
	m, err := StartLocalJob("comando_inexistente_job_fastexec", nil, "", "")
	if err != nil {
		t.Fatalf("o start não deve falhar; o job só termina logo: %v", err)
	}
	st, finished, err := LocalJobWait(m.ID, 15*time.Second)
	if err != nil || !finished || st.State != jobStateExited {
		t.Fatalf("esperava EXITED: %+v %v", st, err)
	}
	if out, _ := TailLocalJob(m.ID, 5); strings.TrimSpace(out) == "" || strings.ContainsRune(out, '\ufffd') {
		t.Errorf("o log deveria ter a mensagem do sistema, legível: %q", out)
	}
}

func TestLocalJobIDeCwd(t *testing.T) {
	useTempJobs(t)
	if _, err := StartLocalJob("echo x", nil, "", "id invalido!"); err == nil {
		t.Errorf("id inválido deveria ser rejeitado")
	}
	if _, err := StartLocalJob("echo x", nil, `C:\nao\existe\fx_job`, ""); err == nil || !strings.Contains(err.Error(), "cwd does not exist") {
		t.Errorf("cwd inexistente deveria dar erro claro: %v", err)
	}
	if _, err := LocalJobStatus("nao-existe"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("job inexistente: %v", err)
	}
	m, err := StartLocalJob("", []string{"node", "-e", "setInterval(function(){},1000)"}, "", "meu-job")
	if err != nil || m.ID != "meu-job" {
		t.Fatalf("id escolhido pelo agente: %+v %v", m, err)
	}
	if _, err := StartLocalJob("", []string{"node", "-e", "1"}, "", "meu-job"); err == nil {
		t.Errorf("id repetido deveria ser rejeitado")
	}
}
