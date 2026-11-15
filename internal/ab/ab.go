// Package ab decide, para a fase 4, em qual braço uma conversa cai: metade recebe a regra, metade não. É domínio
// puro (nenhum acesso a config, banco ou ambiente) e a decisão é determinística por (conversa, regra), então a
// mesma conversa fica sempre no mesmo braço. Sem conversa identificada, a regra fica ligada (comportamento de hoje).
package ab

import (
	"crypto/sha1"
	"encoding/binary"
)

// Config é a seção "ab" do config.json.
type Config struct {
	Enabled    bool     `json:"enabled,omitempty"`
	Salt       string   `json:"salt,omitempty"`
	Rules      []string `json:"rules,omitempty"`       // regras sob A/B; vazio = nenhuma
	TreatedArm string   `json:"treated_arm,omitempty"` // "0" ou "1" (padrão "1"): braço que MANTÉM a regra ligada
	Percent    int      `json:"percent,omitempty"`     // % no braço tratado (padrão e valor útil: 50)
}

// Braços de uma conversa numa regra.
const (
	Control = 0 // regra desligada
	Treated = 1 // regra ligada
)

func (c Config) percent() int {
	if c.Percent <= 0 || c.Percent >= 100 {
		return 50
	}
	return c.Percent
}

func (c Config) treatedArm() int {
	if c.TreatedArm == "0" {
		return Control
	}
	return Treated
}

func (c Config) covers(rule string) bool {
	for _, r := range c.Rules {
		if r == rule {
			return true
		}
	}
	return false
}

// Arm devolve o braço de (conversation, rule). Sem conversa, tudo fica no controle.
func (c Config) Arm(rule, conversation string) int {
	if conversation == "" {
		return Control
	}
	h := sha1.Sum([]byte(c.Salt + "|" + conversation + "|" + rule))
	if int(binary.BigEndian.Uint32(h[:4])%100) < c.percent() {
		return Treated
	}
	return Control
}

// Off diz se a regra deve ser DESLIGADA nesta conversa: só quando o A/B está ligado, a regra está na lista e a
// conversa caiu no braço não tratado. Sem conversa, a regra fica ligada.
func (c Config) Off(rule, conversation string) bool {
	if !c.Enabled || conversation == "" || !c.covers(rule) {
		return false
	}
	return c.Arm(rule, conversation) != c.treatedArm()
}
