package graph

// bgindex_test.go: o contrato da indexação de fundo — uma passada por repositório por mais chamadas que cheguem,
// consulta que não espera para sempre nem falha, desligamento por ambiente, pasta fora de git intocada e
// cancelamento no fechamento.

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// bgTest devolve o estado global da indexação de fundo ao zero depois do teste (e solta o semáforo, se o teste o
// prendeu): sem isso uma passada presa contaminaria o teste seguinte.
func bgTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		resetBackground()
		for len(bgSem) > 0 {
			<-bgSem
		}
	})
}

// TestKickBackgroundSingleflight: 8 chamadas no mesmo repositório viram UMA passada de fundo, e logo depois dela o
// mesmo repositório não dispara de novo (histerese: a passada acabou de olhar o estado).
func TestKickBackgroundSingleflight(t *testing.T) {
	isolated(t)
	bgTest(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
		"b.go": "package p\n\nfunc B() { A() }\n",
	})

	var mu sync.Mutex
	passes := 0
	bgOnStart = func(string) { mu.Lock(); passes++; mu.Unlock() }
	defer func() { bgOnStart = nil }()

	bgSem <- struct{}{} // prende a passada: nenhuma termina antes de as outras chegarem

	kicks := make([]bool, 8)
	var wg sync.WaitGroup
	for i := range kicks {
		wg.Add(1)
		go func(i int) { defer wg.Done(); kicks[i] = KickBackground(root) }(i)
	}
	wg.Wait()

	for i, ok := range kicks {
		if !ok {
			t.Fatalf("disparo %d não pegou a passada em curso: o singleflight deixou passar mais de uma", i)
		}
	}
	<-bgSem

	if finished, _, _ := WaitBackground(root, 10*time.Second); !finished {
		t.Fatal("a passada de fundo não terminou no prazo")
	}
	mu.Lock()
	n := passes
	mu.Unlock()
	if n != 1 {
		t.Fatalf("8 chamadas viraram %d passadas", n)
	}
	if f := TrackedFiles(root); f != 2 {
		t.Fatalf("a passada de fundo não indexou o repositório: %d arquivos", f)
	}
	if KickBackground(root) {
		t.Fatal("recém-indexado, o mesmo repositório não deveria disparar outra passada")
	}
}

// TestKickBackgroundDisabledByEnv: FASTEXEC_BG_INDEX=off desliga a indexação de fundo por inteiro.
func TestKickBackgroundDisabledByEnv(t *testing.T) {
	isolated(t)
	bgTest(t)
	root := gitRepo(t, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})

	t.Setenv(BgOffEnv, "off")
	if !BgDisabled() {
		t.Fatalf("%s=off deveria desligar a indexação de fundo", BgOffEnv)
	}
	if KickBackground(root) {
		t.Fatal("desligada por ambiente, nenhum disparo deveria acontecer")
	}
	if _, _, ok := Building(root); ok {
		t.Fatal("desligada, nenhuma passada deveria existir")
	}
}

// TestKickBackgroundOutsideGit: pasta sem .git não é indexada por conta própria (nada de varrer o disco sozinho).
func TestKickBackgroundOutsideGit(t *testing.T) {
	isolated(t)
	bgTest(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})

	if KickBackground(dir) {
		t.Fatal("fora de repositório git não há indexação de fundo")
	}
	if f := TrackedFiles(dir); f != 0 {
		t.Fatalf("nada deveria ter sido indexado: %d arquivos", f)
	}
}

// TestQueryDuringBackgroundIndexAnswersPartial: consulta que chega com a indexação em curso espera o portão e, se
// ele estourar, responde com o que já existe e o aviso de índice em construção — nunca erro.
func TestQueryDuringBackgroundIndexAnswersPartial(t *testing.T) {
	isolated(t)
	bgTest(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
		"b.go": "package p\n\nfunc B() { A() }\n",
	})
	if _, err := Reindex(root); err != nil { // o índice existe: a resposta parcial tem de onde sair
		t.Fatal(err)
	}

	bgSem <- struct{}{} // a passada fica presa: é o "está indexando agora" de verdade
	if !KickBackground(root) {
		t.Fatal("esperava uma passada de fundo em curso")
	}
	old := BgWaitBudget
	BgWaitBudget = 30 * time.Millisecond
	defer func() { BgWaitBudget = old }()

	start := time.Now()
	out, err := Callers(root, "A", 0, 1)
	if err != nil {
		t.Fatalf("consulta durante a indexação não pode falhar: %v", err)
	}
	if !strings.Contains(out, "still being built in the background") {
		t.Fatalf("faltou o aviso de índice em construção:\n%s", out)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("a consulta estourou o portão e esperou demais: %v", el)
	}
	if !strings.Contains(out, "b.go") { // a resposta parcial sai do índice que já existia
		t.Fatalf("a resposta parcial perdeu o que já estava indexado:\n%s", out)
	}
	<-bgSem
}

// TestQueryWaitsGateForCompleteAnswer: com a passada de fundo em curso, a consulta espera o portão e responde
// completo (sem aviso de construção) quando ela cabe no prazo.
func TestQueryWaitsGateForCompleteAnswer(t *testing.T) {
	isolated(t)
	bgTest(t)
	root := gitRepo(t, map[string]string{
		"a.go": "package p\n\nfunc A() {}\n",
		"b.go": "package p\n\nfunc B() { A() }\n",
	})

	if !KickBackground(root) {
		t.Fatal("esperava uma passada de fundo em curso")
	}
	out, err := Callers(root, "A", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "still being built") {
		t.Fatalf("a consulta deveria ter esperado o portão:\n%s", out)
	}
	if !strings.Contains(out, "b.go") {
		t.Fatalf("resposta incompleta depois do portão:\n%s", out)
	}
}

// TestStopBackgroundCancels: no fechamento a passada de fundo é cancelada, a espera é curta, nada é indexado
// depois do cancelamento e nenhum disparo novo é aceito.
func TestStopBackgroundCancels(t *testing.T) {
	isolated(t)
	bgTest(t)
	root := gitRepo(t, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})

	bgSem <- struct{}{} // a passada espera a vez; o cancelamento chega antes de ela começar a varrer
	go func() {
		time.Sleep(150 * time.Millisecond)
		<-bgSem
	}()
	if !KickBackground(root) {
		t.Fatal("esperava uma passada de fundo em curso")
	}

	start := time.Now()
	StopBackground()
	if el := time.Since(start); el > bgStopWait {
		t.Fatalf("StopBackground passou do próprio teto: %v", el)
	}
	if f := TrackedFiles(root); f != 0 {
		t.Fatalf("passada cancelada não deveria indexar nada: %d arquivos", f)
	}
	if KickBackground(root) {
		t.Fatal("depois do fechamento nenhuma passada nova é aceita")
	}
}
