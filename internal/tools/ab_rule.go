package tools

import "homelab/fastexec/internal/config"

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
