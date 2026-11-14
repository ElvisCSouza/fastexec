package telemetry

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"

	"homelab/fastexec/internal/measure"
)

// Economia por chamada: onde o FastExec devolveu menos texto (ou evitou uma ida ao modelo) do que a
// ferramenta comum devolveria. Os pontos de economia chamam NoteSaving/NoteAvoided; o despacho abre e
// fecha o registro da chamada (BeginCall/EndCall) e grava o resultado na tabela `saving`.
//
// O registro é por goroutine: o handler roda na mesma goroutine do despacho, então nenhuma assinatura muda.

// Categorias (chaves estáveis gravadas no banco).
const (
	CatClean   = "clean"   // saída limpa: barras de progresso, cor, linhas repetidas, preenchimento
	CatCut     = "cut"     // saída enorme cortada, com o resto em arquivo
	CatSlice   = "slice"   // trecho do arquivo em vez do arquivo inteiro
	CatEdit    = "edit"    // troca de trecho em vez de reenviar o arquivo
	CatRetry   = "retry"   // erro corrigido ou explicado antes de falhar (uma ida ao modelo a menos)
	CatTimeout = "timeout" // chamada que seria cortada pelo cliente e devolveu o que tinha
	CatSchema  = "schema"  // lista de ferramentas menor (esquemas sob demanda)
	CatGroup   = "group"   // listagem recursiva reagrupada por diretório (mesmos nomes, menos texto)
	CatDedup   = "dedup"   // saída idêntica à da execução anterior, devolvida como aviso curto
	CatReadCap = "readcap" // leitura inteira cortada no limite, com a instrução de como continuar
)

// AvoidedRoundTripMs é o tempo médio estimado de uma ida ao modelo evitada (suposição, não medição).
const AvoidedRoundTripMs = 15000

// Saving é uma economia registrada.
type Saving struct {
	Cat    string `json:"cat"`
	Bytes  int    `json:"bytes"`  // bytes que o agente deixou de ler/enviar (medido ou proxy documentado)
	Trips  int    `json:"trips"`  // idas ao modelo evitadas
	Ms     int64  `json:"ms"`     // tempo evitado estimado
	Exact  bool   `json:"exact"`  // true: diferença de tamanhos medida; false: estimativa
	Before int    `json:"before"` // tamanho que a ferramenta comum devolveria (0 se não medido)
	After  int    `json:"after"`  // tamanho realmente devolvido
	Note   string `json:"note"`   // o que aconteceu, em uma frase com números (aparece na tela de detalhes)
}

type callLedger struct {
	mu    sync.Mutex
	items []Saving
	cmdMs int64 // tempo do comando dentro da chamada (measure.CmdUnknown se não houve)
}

var ledgers sync.Map // goroutine id -> *callLedger

func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		n, _ := strconv.ParseUint(string(b[:i]), 10, 64)
		return n
	}
	return 0
}

// BeginCall abre o registro da chamada em andamento nesta goroutine.
func BeginCall() { ledgers.Store(goid(), &callLedger{cmdMs: measure.CmdUnknown}) }

// NoteCommandMs soma o tempo que um comando levou dentro desta chamada (exec).
func NoteCommandMs(ms int64) {
	v, ok := ledgers.Load(goid())
	if !ok {
		return
	}
	l := v.(*callLedger)
	l.mu.Lock()
	if l.cmdMs == measure.CmdUnknown {
		l.cmdMs = 0
	}
	l.cmdMs += ms
	l.mu.Unlock()
}

// CommandMs devolve o tempo de comando anotado nesta chamada (measure.CmdUnknown se nenhum). Chamar antes de EndCall.
func CommandMs() int64 {
	v, ok := ledgers.Load(goid())
	if !ok {
		return measure.CmdUnknown
	}
	l := v.(*callLedger)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cmdMs
}

// EndCall fecha o registro e devolve o que foi anotado.
func EndCall() []Saving {
	v, ok := ledgers.LoadAndDelete(goid())
	if !ok {
		return nil
	}
	l := v.(*callLedger)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items
}

func add(s Saving) {
	v, ok := ledgers.Load(goid())
	if !ok {
		return
	}
	l := v.(*callLedger)
	l.mu.Lock()
	l.items = append(l.items, s)
	l.mu.Unlock()
}

// NoteSaving anota uma redução medida: before era o tamanho da saída crua, after o que foi devolvido.
func NoteSaving(cat string, before, after int) { NoteSavingDetail(cat, before, after, "") }

// NoteSavingDetail é o NoteSaving com uma nota (o que foi cortado ou removido, com números).
func NoteSavingDetail(cat string, before, after int, note string) {
	if before > after {
		add(Saving{Cat: cat, Bytes: before - after, Exact: true, Before: before, After: after, Note: note})
	}
}

// NoteAvoided anota uma ida ao modelo evitada. bytes é um proxy: o que a tentativa que falharia
// teria custado (comando reenviado e mensagem de erro); ms é tempo evitado além da ida ao modelo.
func NoteAvoided(cat string, bytes int, ms int64) { NoteAvoidedDetail(cat, bytes, ms, "") }

// NoteAvoidedDetail é o NoteAvoided com uma nota (o que o FastExec corrigiu ou respondeu).
func NoteAvoidedDetail(cat string, bytes int, ms int64, note string) {
	add(Saving{Cat: cat, Bytes: bytes, Trips: 1, Ms: AvoidedRoundTripMs + ms, Note: note})
}

// NoteBytes anota economia medida sem comparação de tamanhos (ex.: partes do esquema não enviadas).
func NoteBytes(cat string, bytes int) {
	if bytes > 0 {
		add(Saving{Cat: cat, Bytes: bytes, Exact: true})
	}
}

// RecordSaving grava uma economia que não pertence a uma chamada de ferramenta (ex.: tools/list).
func RecordSaving(s Saving) {
	if dbInstance == nil {
		Init()
	}
	if dbInstance == nil {
		return
	}
	noteFailure("saving", writeSavings(dbInstance, 0, []Saving{s}))
}
