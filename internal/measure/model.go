// Package measure é o domínio da medição do FastExec: separa economia medida de economia suposta,
// detecta sinais de dano, conta uso por funcionalidade e mede o overhead. Não conhece banco de dados:
// lê tudo por uma porta (Source) que o pacote telemetry implementa sobre SQLite.
package measure

import (
	"strconv"
	"time"
)

// BytesPerToken é a conversão usada em todo o FastExec (mesmo valor de telemetry.BytesPerToken;
// um teste em telemetry garante que os dois não divergem).
const BytesPerToken = 3.5

// CmdUnknown marca uma chamada sem o tempo do comando registrado (anterior à coluna cmd_ms).
const CmdUnknown int64 = -1

// ArmMinSamples é o mínimo de coletas (chamadas) por braço do A/B antes de ler o veredito. Não há piso de
// tempo: a regra é decidida quando os dois braços juntarem coletas bastantes.
const ArmMinSamples = 30

// GuardMargin é o limite de não-inferioridade do guardrail (dano por chamada): a regra só vence se o dano do
// braço COM não piorar além disto (5%) em relação ao SEM. Ver ADR-002/ADR-007.
const GuardMargin = 0.05

// Window é a janela de tempo do relatório, independente de como a fonte a filtra.
type Window struct {
	Today bool // o dia local de hoje
	Hours int  // últimas N horas (0 = todo o histórico); ignorado quando Today
}

// Label descreve a janela em português, para cabeçalho.
func (w Window) Label() string {
	switch {
	case w.Today:
		return "hoje"
	case w.Hours > 0 && w.Hours%24 == 0:
		if w.Hours == 24 {
			return "últimas 24 horas"
		}
		return "últimos " + strconv.Itoa(w.Hours/24) + " dias"
	case w.Hours > 0:
		return "últimas " + strconv.Itoa(w.Hours) + " horas"
	}
	return "todo o histórico"
}

// Call é uma chamada de ferramenta registrada na auditoria.
type Call struct {
	ID              int64
	At              time.Time // quando a chamada aconteceu (para o switchback por janela)
	Channel         string    // client (fallback: session_id): um canal por processo MCP
	Conversation    string    // id de conversa informado pelo cliente ("" quando não há)
	Tool            string
	Summary         string
	ArgsHash        string
	Marks           string // "Hint:ab12,Guard:cd34": o que o FastExec disse ao agente
	Truncated       bool
	OK              bool
	HasConversation bool  // o cliente informou id de conversa
	TokensOut       int64 // tokens devolvidos ao modelo (estimados pela auditoria)
	DurMs           int64 // tempo total da chamada
	CmdMs           int64 // tempo do comando dentro da chamada (CmdUnknown se não medido)
}

// SavingRow é uma linha de economia registrada.
type SavingRow struct {
	Cat   string
	Bytes int64
	Trips int64
}

// Source é a porta de leitura. As chamadas vêm ordenadas por ID (ordem real de execução).
type Source interface {
	Calls(w Window) ([]Call, error)
	Savings(w Window) ([]SavingRow, error)
	// MarkTemplates devolve o texto-modelo de cada aviso conhecido (id -> texto); ids antigos podem faltar.
	MarkTemplates() (map[string]string, error)
}

func tokensOf(bytes int64) int64 { return int64(float64(bytes) / BytesPerToken) }
