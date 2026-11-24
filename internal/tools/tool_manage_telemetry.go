package tools

// tool_manage_telemetry.go: o que a telemetria grava de uma chamada tool_manage. Só a ação, o módulo e a tool
// resolvidos, nunca os argumentos brutos (params) nem nomes fora do catálogo: ação fora do conjunto vira "other",
// módulo desconhecido vira "unknown" e tool que não existe vira "unresolved". Não executa nada.

import (
	"encoding/json"
	"strings"
)

// ToolManageTelemetry devolve (ação, módulo, tool) para a telemetria. tool só vem preenchida quando a ação é
// 'call' e a tool existe no catálogo; módulo vem do argumento se for conhecido, senão do módulo da tool resolvida.
func ToolManageTelemetry(rawArgs []byte) (action, module, tool string) {
	var args ToolManageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return "unparsed", "", ""
	}
	action = strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list", "search", "enable", "disable", "call":
	default:
		action = "other"
	}

	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	mod := strings.ToLower(strings.TrimSpace(args.Module))
	if mod != "" {
		if _, ok := globalRegistry.modules[mod]; ok {
			module = mod
		} else {
			module = "unknown"
		}
	}

	if action == "call" {
		name := strings.TrimSpace(args.Tool)
		tool = "unresolved"
		for modName, m := range globalRegistry.modules {
			if _, ok := m.Tools[name]; ok {
				tool = name
				if module == "" {
					module = modName
				}
				break
			}
		}
	}
	return action, module, tool
}
