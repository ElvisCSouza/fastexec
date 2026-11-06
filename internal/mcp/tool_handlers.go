package mcp

// tool_handlers.go: tabela nome → handler das tools nativas. Tool nova = uma linha aqui + o schema em tool_schemas.go;
// o DispatchTool não muda. Tools de módulos sob demanda continuam em tools.GetDynamicHandler.

import (
	"encoding/json"

	"homelab/fastexec/internal/tools"
)

type toolHandler func(json.RawMessage) (string, error)

// typed adapta um handler de argumentos tipados: decodifica o JSON (erro de decodificação é ignorado, como sempre
// foi: campo ausente ou de tipo errado chega como valor zero e o handler valida) e chama.
func typed[A any](fn func(A) (string, error)) toolHandler {
	return func(raw json.RawMessage) (string, error) {
		var a A
		_ = json.Unmarshal(raw, &a)
		return fn(a)
	}
}

var toolHandlers = map[string]toolHandler{
	"exec":         typed(tools.HandleExec),
	"sql":          typed(tools.HandleSql),
	"file_ops":     typed(tools.HandleFileOps),
	"read_file":    typed(tools.HandleReadFile),
	"code_outline": typed(tools.HandleCodeOutline),
	"code_graph":   typed(tools.HandleCodeGraph),
	"write_file":   typed(tools.HandleWriteFile),
	"edit_file":    typed(tools.HandleEditFile),
	"get_file":     typed(tools.HandleGetFile),
	"put_file":     typed(tools.HandlePutFile),
	"job":          typed(tools.HandleJob),
	"ping":         typed(tools.HandlePing),
	"handoff":      typed(tools.HandleHandoff),
	"tool_manage":  tools.HandleToolManage,
}
