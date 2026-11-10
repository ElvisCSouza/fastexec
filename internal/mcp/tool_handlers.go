package mcp

// tool_handlers.go: tabela nome → handler das tools nativas. Tool nova = uma linha aqui + o schema em tool_schemas.go;
// o DispatchTool não muda. Tools de módulos sob demanda continuam em tools.GetDynamicHandler.

import (
	"encoding/json"
	"reflect"

	"homelab/fastexec/internal/tools"
)

type toolHandler func(json.RawMessage) (string, error)

// typed adapta um handler de argumentos tipados: decodifica o JSON e chama. Antes de chegar aqui o DispatchTool já
// normalizou as chaves (argnorm.go), então o json.Unmarshal nunca descarta uma chave em silêncio; o que sobra de erro
// de tipo (campo de tipo errado) chega como valor zero e o handler valida, como sempre foi. typed também registra os
// campos do tipo em argSpecs, que é o que o normalizador usa para reconhecer, renomear ou recusar chaves.
func typed[A any](name string, fn func(A) (string, error)) toolHandler {
	if t := reflect.TypeOf((*A)(nil)).Elem(); t.Kind() == reflect.Struct {
		argSpecs[name] = fieldsOf(t)
	}
	return func(raw json.RawMessage) (string, error) {
		var a A
		_ = json.Unmarshal(raw, &a)
		return fn(a)
	}
}

var toolHandlers = map[string]toolHandler{
	"exec":         typed("exec", tools.HandleExec),
	"sql":          typed("sql", tools.HandleSql),
	"file_ops":     typed("file_ops", tools.HandleFileOps),
	"read_file":    typed("read_file", tools.HandleReadFile),
	"code_outline": typed("code_outline", tools.HandleCodeOutline),
	"code_graph":   typed("code_graph", tools.HandleCodeGraph),
	"write_file":   typed("write_file", tools.HandleWriteFile),
	"edit_file":    typed("edit_file", tools.HandleEditFile),
	"get_file":     typed("get_file", tools.HandleGetFile),
	"put_file":     typed("put_file", tools.HandlePutFile),
	"job":          typed("job", tools.HandleJob),
	"ping":         typed("ping", tools.HandlePing),
	"handoff":      typed("handoff", tools.HandleHandoff),
	"tool_manage":  tools.HandleToolManage,
}
