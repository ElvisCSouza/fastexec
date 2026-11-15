package tools

import (
	"os"

	"homelab/fastexec/internal/config"
)

// Ponte da fase 4 (A/B): cada regra pergunta aqui se está desligada nesta conversa. Sem a seção "ab" no
// config.json (ou com enabled=false), devolve false e a regra segue como hoje. O escape por ambiente
// (FASTEXEC_NO_*) continua no ponto de cada regra, somado a esta consulta.
func abOff(rule, id string) bool {
	c := config.Get()
	if c == nil || c.AB == nil {
		return false
	}
	return c.AB.Off(rule, id)
}

// readSliceOff: regra "slice" — desliga o "graph first" (arquivo de código grande devolve o outline em vez do texto).
func readSliceOff(id string) bool {
	return os.Getenv("FASTEXEC_NO_READ_SLICE") != "" || abOff("slice", id)
}

// timeoutPartialOff: regra "timeout" — no estouro do prazo devolve erro seco em vez da saída parcial.
func timeoutPartialOff(id string) bool {
	return os.Getenv("FASTEXEC_NO_TIMEOUT_PARTIAL") != "" || abOff("timeout", id)
}

// autofixOn: regra "retry" — auto-remediação ligada (cat/type, sleep, cd, listagem e resposta pelo grafo).
func autofixOn(id string) bool {
	return os.Getenv("FASTEXEC_NO_AUTOFIX") == "" && !abOff("retry", id)
}
