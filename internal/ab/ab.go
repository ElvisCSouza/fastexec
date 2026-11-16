// Package ab decide, para a fase 4, em qual braço uma conversa cai: metade recebe a regra, metade não. É domínio
// puro (nenhum acesso a config, banco ou ambiente) e a decisão é determinística por (conversa, regra), então a
// mesma conversa fica sempre no mesmo braço. Sem conversa identificada, a regra fica ligada (comportamento de hoje).
package ab

import (
	"crypto/sha1"
	"encoding/binary"
	"time"
)

// Config é a seção "ab" do config.json.
type Config struct {
	Enabled    bool     `json:"enabled,omitempty"`
	Salt       string   `json:"salt,omitempty"`
	Rules      []string `json:"rules,omitempty"`       // regras sob A/B; vazio = nenhuma
	TreatedArm string   `json:"treated_arm,omitempty"` // "0" ou "1" (padrão "1"): braço que MANTÉM a regra ligada
	Percent    int      `json:"percent,omitempty"`     // % no braço tratado (padrão e valor útil: 50)
	// Mode "" = por conversa (padrão); "switchback" = por janela de tempo (todas as chamadas da janela no mesmo
	// braço). Switchback serve à realidade de 1 usuário: usa cada chamada como amostra em vez de cada conversa.
	Mode      string `json:"mode,omitempty"`
	WindowMin int    `json:"window_min,omitempty"` // minutos por janela no switchback (padrão 60)
}

// Switchback reporta se o A/B alterna por janela de tempo.
func (c Config) Switchback() bool { return c.Mode == "switchback" }

// Window dá o índice da janela de tempo (switchback) de t.
func (c Config) Window(t time.Time) int { return c.window(t) }

// window devolve o índice da janela de tempo (em minutos) de t.
func (c Config) window(t time.Time) int {
	w := c.WindowMin
	if w <= 0 {
		w = 60
	}
	return int(t.Unix() / (int64(w) * 60))
}

// rulePhase dá 0 ou 1 fixo por regra, para o switchback não alternar todas as regras na mesma janela.
func (c Config) rulePhase(rule string) int {
	h := sha1.Sum([]byte(c.Salt + "|phase|" + rule))
	return int(h[0] % 2)
}

// OffAt decide a regra na hora t: por conversa (padrão) ou por janela de tempo (switchback).
func (c Config) OffAt(rule, conversation string, t time.Time) bool {
	if !c.Enabled || !c.covers(rule) {
		return false
	}
	if c.Switchback() {
		// Fase por regra: regras não flipam todas juntas (decorrelaciona tempo e regra).
		return (c.window(t)+c.rulePhase(rule))%2 == 1
	}
	if conversation == "" {
		return false
	}
	return c.Arm(rule, conversation) != c.treatedArm()
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

// Off decide a regra agora (OffAt com o relógio atual). Mantém a assinatura por conversa.
func (c Config) Off(rule, conversation string) bool {
	if !c.Enabled {
		return false
	}
	if c.Switchback() {
		return c.OffAt(rule, conversation, time.Now())
	}
	if conversation == "" || !c.covers(rule) {
		return false
	}
	return c.Arm(rule, conversation) != c.treatedArm()
}
