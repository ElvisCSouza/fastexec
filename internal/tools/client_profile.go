package tools

import (
	"strings"
	"time"
)

// Perfil de tempo por cliente MCP. O Claude Desktop corta a chamada de tool aos 60 s: o exec fica em 45 s (teto 50 s) e o
// que passa disso vai para a tool job (3+ turnos). O Claude Code espera o MCP quase sem limite (MCP_TOOL_TIMEOUT, ~27 h) e o
// próprio Bash dele tem 2 min de padrão e 10 min de teto: com o mesmo prazo, um comando de 1-2 minutos é uma chamada só.
// O cliente vem de clientInfo.name do initialize (o proxy o injeta como _caller "claude-code#<pid>").
const (
	longDefaultTimeout = 120 * time.Second
	longMaxTimeout     = 600 * time.Second
	longAwaitMax       = 100 * time.Second
)

func longTimeoutClient(caller string) bool { return strings.HasPrefix(caller, "claude-code") }

// execTimeouts devolve o prazo padrão e o teto do exec para o cliente.
func execTimeouts(caller string, def time.Duration) (time.Duration, time.Duration) {
	if longTimeoutClient(caller) {
		return longDefaultTimeout, longMaxTimeout
	}
	return def, maxClientTimeout
}

// awaitMax: quanto o 'job start' com await espera o job terminar.
func awaitMax(caller string) time.Duration {
	if longTimeoutClient(caller) {
		return longAwaitMax
	}
	return startAwaitMax
}

// ClientTimeoutTexts são os trechos do tools/list que citam prazos, para o cliente em questão (clientName = clientInfo.name).
type ClientTimeoutTexts struct{ ExecDefault, ExecTimeoutArg, JobOver, AwaitSeconds string }

func TimeoutTextsFor(clientName string) ClientTimeoutTexts {
	if longTimeoutClient(clientName) {
		return ClientTimeoutTexts{
			ExecDefault:    "default limit 2 min",
			ExecTimeoutArg: "Limit in ms (default 120000, max 600000); beyond that use 'job'",
			JobOver:        "~2 min",
			AwaitSeconds:   "100 s",
		}
	}
	return ClientTimeoutTexts{
		ExecDefault:    "default limit 45 s",
		ExecTimeoutArg: "Limit in ms (default 45000, max 50000); longer work goes to 'job'",
		JobOver:        "~30 s",
		AwaitSeconds:   "40 s",
	}
}
