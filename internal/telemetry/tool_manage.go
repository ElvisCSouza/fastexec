package telemetry

// tool_manage.go: resumo de uma chamada tool_manage para a coluna command_summary. Recebe valores já fechados pelo
// chamador (ver tools.ToolManageTelemetry): nunca grava params, query nem nomes livres.

// ToolManageSummary monta "action=<a> module=<m> tool=<t>", omitindo o que não se aplica.
func ToolManageSummary(action, module, tool string) string {
	s := "action=" + action
	if module != "" {
		s += " module=" + module
	}
	if tool != "" {
		s += " tool=" + tool
	}
	return s
}
