package mcp

// tool_manage_telemetry.go: ponte entre a chamada tool_manage e a telemetria. A linha grava ação, módulo e tool
// resolvidos (via tools.ToolManageTelemetry) e nunca o hash nem os argumentos brutos (params pode conter comandos).

import (
	"strings"

	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/tools"
)

// summarizeCall: o resumo padrão, trocado pelo de tool_manage quando for o caso.
func summarizeCall(name string, rawArgs []byte) (string, string) {
	host, summary := telemetry.SummarizeCall(name, rawArgs)
	if name == "tool_manage" {
		action, module, tool := tools.ToolManageTelemetry(rawArgs)
		summary = telemetry.ToolManageSummary(action, module, tool)
	}
	return host, summary
}

// argsHashFor: hash dos argumentos, exceto em tool_manage (sem identificador de alta cardinalidade nem conteúdo).
func argsHashFor(name string, rawArgs []byte) string {
	if name == "tool_manage" {
		return ""
	}
	return telemetry.ArgsHash(rawArgs)
}

// toolOutcome: classificação do erro. Para tool_manage, "não encontrado" (módulo ou tool inexistente) é uso, não
// falha desconhecida.
func toolOutcome(name string, err error, outText string) telemetry.Outcome {
	if name == "tool_manage" && strings.Contains(err.Error(), "not found") {
		return telemetry.NotFoundOutcome()
	}
	return telemetry.FailureOutcome(err, outText)
}
