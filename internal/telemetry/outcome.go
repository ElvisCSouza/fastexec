package telemetry

// outcome.go: desfecho explícito de cada chamada, gravado em colunas próprias (outcome_schema=1). Separa falha de
// infraestrutura (a resposta não chegou, panic, timeout), uso/validação (argumentos, guard, tool inexistente),
// código de saída não-zero do comando e o que não se sabe classificar. Não muda execução, retry, nem o texto
// devolvido ao modelo: só descreve o que já aconteceu. Linhas antigas ficam com outcome_schema=0 (desconhecido).

import (
	"regexp"
	"strconv"
	"strings"
)

// Valores de error_type (enum fechado; vazio = sem erro).
const (
	ErrInvalidParams = "invalid_params" // argumentos recusados antes da execução (uso)
	ErrGuardRejected = "guard_rejected" // bloqueio de guarda ([FastExec Guard]) (uso)
	ErrNotFound      = "not_found"      // tool inexistente (uso)
	ErrCommandFailed = "command_failed" // o comando rodou e saiu com código ≠ 0 (resultado do comando)
	ErrTimeout       = "timeout"        // prazo estourado; o efeito pode ter acontecido (infraestrutura)
	ErrTransportLost = "transport_lost" // resposta do daemon perdida depois do envio (infraestrutura)
	ErrToolInternal  = "tool_internal"  // panic dentro do handler (infraestrutura)
	ErrUnknown       = "unknown"        // erro sem classe conhecida: não é promovido a outra categoria
)

// Outcome é o desfecho de uma chamada. Valores -1 = não se aplica ou desconhecido (também o que as linhas
// antigas recebem por padrão).
type Outcome struct {
	TransportOK     int    // 1: o handler respondeu; 0: a resposta se perdeu no transporte; -1 desconhecido
	ToolOK          int    // 1: sucesso; 0: a ferramenta falhou; -1 desconhecido
	CommandExitCode int    // código de saída quando um comando rodou; -1 = não se aplica
	ErrorType       string // um dos Err*; "" sem erro
	EffectUnknown   int    // 1: a ação pode ter ocorrido mesmo com erro; 0: não; -1 desconhecido
}

// UnknownOutcome é o valor de linhas sem desfecho (legado): todos os campos desconhecidos.
var UnknownOutcome = Outcome{TransportOK: -1, ToolOK: -1, CommandExitCode: -1, EffectUnknown: -1}

var reExitLead = regexp.MustCompile(`^\[exit (-?\d+)\]`)
var reExitMsg = regexp.MustCompile(`command failed with exit code (-?\d+)`)

// SuccessOutcome: a ferramenta respondeu com sucesso. cmdRan indica se um comando foi executado (exit 0).
func SuccessOutcome(cmdRan bool) Outcome {
	o := Outcome{TransportOK: 1, ToolOK: 1, CommandExitCode: -1, EffectUnknown: 0}
	if cmdRan {
		o.CommandExitCode = 0
	}
	return o
}

// FailureOutcome classifica uma falha do handler. O handler foi alcançado (transport_ok=1). outText é a resposta
// devolvida ao modelo; err é o erro do Go. Nunca promove um erro desconhecido a infraestrutura.
func FailureOutcome(err error, outText string) Outcome {
	o := Outcome{TransportOK: 1, ToolOK: 0, CommandExitCode: -1, EffectUnknown: 0}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	switch {
	case strings.Contains(outText, "[FATAL ERROR INTERCEPTED]"):
		o.ErrorType, o.EffectUnknown = ErrToolInternal, 1
	case strings.Contains(msg, "[FastExec Guard]") || strings.Contains(outText, "[FastExec Guard]"):
		o.ErrorType = ErrGuardRejected
	case strings.Contains(msg, "timeout after") || strings.Contains(outText, "timeout after"):
		o.ErrorType, o.EffectUnknown = ErrTimeout, 1
	case reExitLead.MatchString(strings.TrimSpace(outText)) || reExitMsg.MatchString(msg):
		o.ErrorType = ErrCommandFailed
		if m := reExitLead.FindStringSubmatch(strings.TrimSpace(outText)); m != nil {
			o.CommandExitCode = atoiOr(m[1], 1)
		} else if m := reExitMsg.FindStringSubmatch(msg); m != nil {
			o.CommandExitCode = atoiOr(m[1], 1)
		}
	case strings.Contains(msg, "invalid parameters") || strings.Contains(msg, "is required") || strings.Contains(msg, "parameter '"):
		o.ErrorType = ErrInvalidParams
	default:
		o.ErrorType = ErrUnknown
	}
	return o
}

// PanicOutcome: o handler entrou em panic. Pode ter feito parte do efeito.
func PanicOutcome() Outcome {
	return Outcome{TransportOK: 1, ToolOK: 0, CommandExitCode: -1, ErrorType: ErrToolInternal, EffectUnknown: 1}
}

// LostOutcome: a chamada foi enviada ao daemon e a resposta não voltou. Pode ter rodado (ver docs/telemetry.md).
func LostOutcome() Outcome {
	return Outcome{TransportOK: 0, ToolOK: -1, CommandExitCode: -1, ErrorType: ErrTransportLost, EffectUnknown: 1}
}

// NotFoundOutcome: nenhuma tool com esse nome.
func NotFoundOutcome() Outcome {
	return Outcome{TransportOK: 1, ToolOK: 0, CommandExitCode: -1, ErrorType: ErrNotFound, EffectUnknown: 0}
}

// InvalidOutcome: argumentos recusados antes de qualquer execução.
func InvalidOutcome() Outcome {
	return Outcome{TransportOK: 1, ToolOK: 0, CommandExitCode: -1, ErrorType: ErrInvalidParams, EffectUnknown: 0}
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
