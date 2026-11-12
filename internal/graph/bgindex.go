package graph

// bgindex.go: indexação em segundo plano. O primeiro code_graph num repositório grande (ou logo depois de um
// checkout) pagava a varredura inteira dentro da chamada — e o modelo esperava por ela, ou pior, recebia um erro
// seco quando a passada não cabia. Aqui a passada sai da frente: qualquer chamada de tool com cwd dentro de um
// repositório git dispara UMA indexação de fundo por repositório (singleflight por raiz), que roda abaixo do normal
// e some sozinha. Quem consultar o grafo enquanto ela corre espera um portão curto (BgWaitBudget) e, se não der
// tempo, recebe o que já está indexado + o aviso "índice ainda em construção (N/M arquivos)" — nunca erro seco.
//
// Regras: só raiz de repositório git (pasta avulsa fica fora: nada de varrer o disco por conta própria), uma
// passada de fundo por vez no processo (teto de CPU, e o teto de memória é o do próprio indexador: arquivo a
// arquivo, maxRepoFiles/maxFileSize), cancelável, e nada é gravado fora do diretório de dados do FastExec.
// FASTEXEC_BG_INDEX=off desliga tudo.

import (
	"os"
	"strings"
	"sync"
	"time"
)

// BgWaitBudget é o teto de espera de uma consulta pelo construtor de índice em curso: passou disso, a resposta sai
// parcial com o aviso, em vez de o modelo ficar pendurado. Variável para os testes.
var BgWaitBudget = 8 * time.Second

const (
	// bgRecheck é a histerese entre duas conferências de frescor do mesmo repositório: um exec por segundo numa
	// pasta não roda `git status` uma vez por exec (e o estado do repositório não muda nessa escala).
	bgRecheck = 3 * time.Second
	// bgStopWait é o teto de espera no fechamento do processo (o cancelamento já foi pedido).
	bgStopWait = 3 * time.Second
	// BgOffEnv desliga a indexação de fundo: FASTEXEC_BG_INDEX=off.
	BgOffEnv = "FASTEXEC_BG_INDEX"
)

// bgJob é uma passada de fundo: um canal fechado quando ela termina, outro quando o cancelamento é pedido, e o
// contador N/M que a consulta lê enquanto espera.
type bgJob struct {
	root string
	done chan struct{}
	stop chan struct{}
	once sync.Once

	mu    sync.Mutex
	n     int
	total int

	ended time.Time // escrito antes de fechar done, lido depois: a ordem é garantida pelo canal
}

func (j *bgJob) cancelled() bool {
	select {
	case <-j.stop:
		return true
	default:
		return false
	}
}

func (j *bgJob) report(done, total int) {
	j.mu.Lock()
	j.n, j.total = done, total
	j.mu.Unlock()
}

func (j *bgJob) progress() (int, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.n, j.total
}

var (
	bgMu     sync.Mutex
	bgJobs   = map[string]*bgJob{}
	bgClosed bool
	bgWG     sync.WaitGroup
	bgSem    = make(chan struct{}, 1) // uma passada de fundo por vez: o teto de CPU do processo
	// bgOnStart avisa quando uma passada começa de verdade. O singleflight é o contrato mais fácil de quebrar sem
	// ninguém notar, então os testes contam passadas por aqui. Variável para os testes.
	bgOnStart func(root string)
)

// BgDisabled diz se a indexação de fundo está desligada (FASTEXEC_BG_INDEX=off). Lido a cada disparo: ligar e
// desligar vale sem reiniciar o daemon.
func BgDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(BgOffEnv))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// KickBackground dispara a indexação de fundo da raiz, se ela precisar. Devolve true quando uma passada foi
// iniciada e false quando não havia o que fazer: já em curso (singleflight), recém-conferida (histerese), fora de
// repositório git ou desligada por ambiente. É barato e chamado a cada tool, então não bloqueia em nada.
func KickBackground(root string) bool {
	if root == "" || BgDisabled() || !isRepoRoot(root) {
		return false
	}
	key := repoKey(root)
	bgMu.Lock()
	defer bgMu.Unlock()
	if bgClosed {
		return false
	}
	if j, ok := bgJobs[key]; ok {
		select {
		case <-j.done: // a anterior já acabou: só reabre depois da histerese
			if time.Since(j.ended) < bgRecheck {
				return false
			}
		default:
			return true // em curso: uma passada só, por mais chamadas que cheguem ("singleflight")
		}
	}
	j := &bgJob{root: root, done: make(chan struct{}), stop: make(chan struct{})}
	bgJobs[key] = j
	bgWG.Add(1)
	go bgRun(j)
	return true
}

// bgRun é a passada propriamente dita: espera a vez (semáforo), confere o frescor e só então indexa — com
// cancelamento e contador. Nada dela pode derrubar o processo: os erros são descartados de propósito (a passada de
// fundo é uma ajuda; a consulta que chegar depois refaz o que faltou).
func bgRun(j *bgJob) {
	defer func() {
		j.ended = time.Now()
		close(j.done)
		bgWG.Done()
	}()
	if bgOnStart != nil {
		bgOnStart(j.root)
	}
	bgSem <- struct{}{}
	defer func() { <-bgSem }()
	restore := lowerThreadPriority() // abaixo do normal: a chamada que disparou tem a CPU primeiro
	defer restore()
	if j.cancelled() || bgIsClosed() {
		return
	}
	if repoFresh(j.root) {
		return // estado igual ao da última passada: nada a varrer
	}
	_, _ = syncWith(j.root, syncOpts{progress: j.report, cancelled: j.cancelled})
}

// repoFresh: o repositório não mudou desde a última passada completa (mesmo teste barato que abre o Sync).
func repoFresh(root string) bool {
	if !isRepoRoot(root) {
		return false
	}
	head, work, ok := gitState(root)
	if !ok {
		return false
	}
	d, err := store()
	if err != nil {
		return false
	}
	return sameState(d, repoKey(root), head, work)
}

func bgIsClosed() bool {
	bgMu.Lock()
	defer bgMu.Unlock()
	return bgClosed
}

// WaitBackground espera a passada de fundo de root terminar, no máximo d. finished=false significa que ela ainda
// está correndo e devolve o progresso observado (done/total arquivos) para a resposta parcial. Sem passada em
// curso, finished=true na hora.
func WaitBackground(root string, d time.Duration) (finished bool, done, total int) {
	bgMu.Lock()
	j := bgJobs[repoKey(root)]
	bgMu.Unlock()
	if j == nil {
		return true, 0, 0
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-j.done:
		n, tot := j.progress()
		return true, n, tot
	case <-timer.C:
		n, tot := j.progress()
		select {
		case <-j.done: // terminou entre o estouro do prazo e a leitura: a resposta já é a completa
			return true, n, tot
		default:
		}
		return false, n, tot
	}
}

// Building diz se há (e quanto falta de) uma passada de fundo em curso para a raiz.
func Building(root string) (done, total int, ok bool) {
	bgMu.Lock()
	j := bgJobs[repoKey(root)]
	bgMu.Unlock()
	if j == nil {
		return 0, 0, false
	}
	select {
	case <-j.done:
		return 0, 0, false
	default:
	}
	n, tot := j.progress()
	return n, tot, true
}

// StopBackground cancela as passadas de fundo e espera um pouco por elas (fechamento do processo). Depois dela
// nenhum disparo novo é aceito — o processo está indo embora e não vale começar trabalho.
func StopBackground() {
	bgMu.Lock()
	if bgClosed {
		bgMu.Unlock()
		return
	}
	bgClosed = true
	jobs := make([]*bgJob, 0, len(bgJobs))
	for _, j := range bgJobs {
		jobs = append(jobs, j)
	}
	bgMu.Unlock()
	for _, j := range jobs {
		j.once.Do(func() { close(j.stop) })
	}
	waitGroup(bgWG.Wait, bgStopWait)
}

// waitGroup espera a função de espera terminar dentro do prazo.
func waitGroup(wait func(), d time.Duration) {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// resetBackground devolve o estado global ao zero. Só os testes chamam (o processo de verdade encerra depois do
// StopBackground); sem isso o cancelamento de um teste contaminaria o seguinte.
func resetBackground() {
	bgMu.Lock()
	bgClosed = true // barra disparo novo enquanto as passadas antigas morrem
	old := bgJobs
	bgJobs = map[string]*bgJob{}
	bgMu.Unlock()
	for _, j := range old {
		j.once.Do(func() { close(j.stop) })
	}
	waitGroup(bgWG.Wait, bgStopWait)
	bgMu.Lock()
	bgClosed = false
	bgMu.Unlock()
}
