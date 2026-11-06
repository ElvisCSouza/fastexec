package mcp

import (
	"encoding/json"
	"strings"

	"homelab/fastexec/internal/config"
	"homelab/fastexec/internal/tools"
)

// Placeholders nas descrições de 'host': viram a lista de alvos da config (padrão primeiro), sem custo de token
// além dos nomes; quem não tem config vê só "'local' (default)". {hosts_cd} acrescenta o aviso contra
// 'cd <cwd padrão> &&' (tool exec).
const (
	hostsPlaceholder   = "{hosts}"
	hostsCdPlaceholder = "{hosts_cd}"
)

func jsonInner(s string) string {
	b, _ := json.Marshal(s)
	return strings.Trim(string(b), `"`)
}

// toolsList monta o array 'tools' do tools/list: nativas + módulos ativos, com {hosts} preenchido.
func toolsList() json.RawMessage {
	all := make([]any, 0, len(serverTools))
	for _, st := range serverTools {
		all = append(all, st)
	}
	all = append(all, tools.GetActiveDynamicSchemas()...)
	raw, _ := json.Marshal(all)
	cfg := config.Get()
	r := strings.NewReplacer(
		hostsCdPlaceholder, jsonInner(cfg.HostChoices(true)),
		hostsPlaceholder, jsonInner(cfg.HostChoices(false)),
	)
	return json.RawMessage(r.Replace(string(raw)))
}
