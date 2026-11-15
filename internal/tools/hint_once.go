package tools

import "sync"

// hint_once.go: dica pedagógica só na primeira vez por identidade (conversa, senão canal). Repetir a mesma dica
// custa tokens e não muda o comportamento (sinal repeated_hint). id vazio (testes, sem identidade) nunca suprime.
var (
	hintOnceMu   sync.Mutex
	hintOnceSeen = map[string]bool{}
)

func hintOnce(id, kind string) bool {
	if id == "" {
		return true
	}
	hintOnceMu.Lock()
	defer hintOnceMu.Unlock()
	if len(hintOnceSeen) > 5000 {
		hintOnceSeen = map[string]bool{}
	}
	k := id + "|" + kind
	if hintOnceSeen[k] {
		return false
	}
	hintOnceSeen[k] = true
	return true
}
