package mcp

// tool_schemas.go: schemas das tools nativas (o que o LLM vê no tools/list). Só dados: o handler de cada uma está
// em tool_handlers.go e os goldens em testdata/golden travam o conteúdo.
//
// POLÍTICA DE TAMANHO (ver docs/tools.md, "Tool descriptions"): este texto é imposto fixo em TODO turno e o teste
// tools_list_size_test.go trava o orçamento. Aqui mora só o que muda a decisão do modelo: o que a tool faz, quando
// usá-la no lugar de outra, e o parâmetro cujo nome não basta. Exemplos, explicação longa e aviso ficam na
// documentação (docs/tools.md) e voltam na resposta só quando são relevantes, pelos mecanismos de sempre:
// "[FastExec FastHint]" (internal/tools/search_guard.go), "[FastExec Auto-Remediation]" e tool_manage.
// Nome de tool e de parâmetro nunca muda por causa de tamanho, e parâmetro cujo nome já diz tudo vai sem
// 'description' (omitempty em PropertyDef).

var serverTools = []ToolSchema{
	{
		Name:        "exec",
		Description: "Run a command on a host and return its output ({exec_default}). 'command' is a shell line (cmd.exe on Windows 'local'); 'argv' runs with no shell. Use 'job' for anything over {job_over}.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "{hosts_cd}"},
				"command":    {Type: "string", Description: "Shell line"},
				"argv":       {Type: "array", Description: "No shell: [program, arg1, ...]", Items: &PropertyDef{Type: "string"}},
				"cwd":        {Type: "string", Description: "Working directory (relative: from the host cwd)"},
				"input":      {Type: "string", Description: "Sent to the process stdin"},
				"timeout_ms": {Type: "integer", Description: "{exec_timeout_arg}"},
			},
		},
	},
	{
		Name:        "read_file",
		Description: "Read a file on a host: whole, or a slice ('offset'/'limit' bytes, 'start_line'/'end_line', 'ranges', 'pattern', 'symbol'). One call returns at most 32 KB; the footer carries the call that continues.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "'local' (default) or a configured host"},
				"path":       {Type: "string"},
				"offset":     {Type: "integer", Description: "Bytes"},
				"limit":      {Type: "integer", Description: "Max bytes"},
				"start_line": {Type: "integer", Description: "First line"},
				"end_line":   {Type: "integer", Description: "Last line"},
				"ranges":     {Type: "string", Description: "Line ranges, e.g. '10-40,90-120'"},
				"pattern":    {Type: "string", Description: "Regex (case-insensitive); repeated log lines come back as counts"},
				"context":    {Type: "integer", Description: "Lines around a match"},
				"symbol":     {Type: "string", Description: "Read only this declaration/body via AST"},
				"numbered":   {Type: "boolean"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "code_outline",
		Description: "Declarations of a source file as 'L<line>: <signature>'; with a directory as 'path' plus 'symbol', every declaration whose name contains it, repo-wide, in one call - instead of recursive grep.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "'local' (default) or a remote host; directories only on 'local'"},
				"path":   {Type: "string", Description: "File or directory"},
				"symbol": {Type: "string", Description: "Name or part of it; omit for the whole outline"},
				"max":    {Type: "integer", Description: "Max results"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "code_graph",
		Description: "Code graph of a local repo (auto-refreshed): 'callers' (where a symbol is used), 'def' (where declared), 'impact' (files that use what a file or symbol defines; check before refactoring), 'index' (rebuild). Exact (LSP) for C#/Go/TS, else by name; interface 'callers' lists implementers.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action":    {Type: "string", Description: "'callers', 'def', 'impact' or 'index'"},
				"symbol":    {Type: "string", Description: "Symbol name ('pkg.Name' accepted)"},
				"target":    {Type: "string", Description: "File for 'impact'"},
				"repo_path": {Type: "string", Description: "Repo root or a path inside it"},
				"max":       {Type: "integer"},
				"depth":     {Type: "integer", Description: "'callers': 1 direct (default), up to 4 = whole chain"},
			},
			Required: []string{"action", "repo_path"},
		},
	},
	{
		Name:        "edit_file",
		Description: "Change an existing file by exact text replacement: 'target' -> 'replacement', or several at once with 'edits' (in order, all or nothing). LF/CRLF matched automatically. Use this, not write_file.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":        {Type: "string", Description: "'local' (default) or a configured host"},
				"path":        {Type: "string"},
				"target":      {Type: "string", Description: "Exact text to replace"},
				"replacement": {Type: "string", Description: "New text ('' deletes)"},
				"symbol":      {Type: "string", Description: "Replace this symbol via AST (no 'target')"},
				"edits":       {Type: "array", Description: "[{target, replacement}, ...]", Items: &PropertyDef{Type: "object"}},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "write_file",
		Description: "Create a file on a host. An existing non-empty file is refused unless 'overwrite' is true; to change part of a file use edit_file.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":      {Type: "string", Description: "'local' (default) or a configured host"},
				"path":      {Type: "string"},
				"content":   {Type: "string"},
				"atomic":    {Type: "boolean", Description: "Temp file + rename"},
				"overwrite": {Type: "boolean", Description: "Replace an existing non-empty file"},
			},
			Required: []string{"path", "content"},
		},
	},
	{
		Name:        "job",
		Description: "Background jobs that outlive the call (dev servers, watchers, anything over {job_over}). 'wait' blocks up to {await_s} and returns only the log lines new since the last wait. For a command that ends, start with await=true: it returns the output if it finishes within {await_s}.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":    {Type: "string", Description: "{hosts}"},
				"action":  {Type: "string", Description: "'start', 'wait', 'status', 'list', 'tail' or 'stop'"},
				"command": {Type: "string", Description: "Shell line for 'start'"},
				"argv":    {Type: "array", Description: "No-shell alternative: [program, arg1, ...]", Items: &PropertyDef{Type: "string"}},
				"cwd":     {Type: "string", Description: "Working directory"},
				"job_id":  {Type: "string", Description: "Job id"},
				"lines":   {Type: "integer", Description: "Lines for 'tail'"},
				"await":   {Type: "boolean", Description: "'start' only: wait up to {await_s} and return the output"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "tool_manage",
		Description: "On-demand tool modules: 'search' by keyword, 'list', 'enable'/'disable' a module (updates the tool list), or 'call' a module tool without loading it. Modules: files, handoff, sql, hosts, docker.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action": {Type: "string", Description: "'search', 'list', 'enable', 'disable' or 'call'"},
				"module": {Type: "string", Description: "Module name"},
				"query":  {Type: "string", Description: "Search keyword"},
				"tool":   {Type: "string"},
				"params": {Type: "string", Description: "JSON arguments for the call"},
			},
			Required: []string{"action"},
		},
	},
}
