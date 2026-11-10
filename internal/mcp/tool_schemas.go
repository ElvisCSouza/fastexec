package mcp

// tool_schemas.go: schemas das tools nativas (o que o LLM vê no tools/list). Só dados: o handler de cada uma está
// em tool_handlers.go e os goldens em testdata/golden travam o conteúdo.

var serverTools = []ToolSchema{
	{
		Name:        "exec",
		Description: "Run a command on a host and return its output as soon as the process exits (default limit 45 s). Two modes: 'command' (a shell line; on Windows host 'local' it runs in cmd.exe) or 'argv' (no shell: each item reaches the program exactly as given; use it for code and text with metacharacters). Long code goes through 'input' (stdin): argv ['node','-'], ['python','-'] or ['pwsh','-NoProfile','-Command','-'] with the script in 'input'. Prefer read_file to read files, file_ops to manage them, code_outline/code_graph to locate code, and the 'job' tool for anything over ~30 s. On timeout the partial output is returned and the process tree is killed (on Windows 'local' it keeps running as a background job instead); stdin without 'input' is an immediate EOF.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "Target: {hosts_cd} or 'ssh:<host>'"},
				"command":    {Type: "string", Description: "Shell line (artificial sleep/timeout is blocked). Give command OR argv"},
				"argv":       {Type: "array", Description: "No-shell alternative: [program, arg1, ...]; no metacharacter is interpreted. Give argv OR command", Items: &PropertyDef{Type: "string"}},
				"cwd":        {Type: "string", Description: "Working directory (relative paths resolve from the host default, e.g. 'app/config')"},
				"input":      {Type: "string", Description: "Content sent to the process stdin"},
				"timeout_ms": {Type: "integer", Description: "Limit in ms (default 45000, max 50000 to stay under the client's 60 s cut); longer work goes to 'job'"},
			},
		},
	},
	{
		Name:        "read_file",
		Description: "Read a local or remote file, whole or a slice (byte offset/limit start_line/end_line, ranges 'a-b,c-d', or pattern+context). Reads are capped at 32 KB: the footer gives the call that continues; use code_outline first. Use it instead of cat, type or head through exec.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "'local' (default) or a configured host"},
				"path":       {Type: "string", Description: "File path"},
				"offset":     {Type: "integer", Description: "Byte offset"},
				"limit":      {Type: "integer", Description: "Max bytes to read"},
				"start_line": {Type: "integer", Description: "First line (1-based)"},
				"end_line":   {Type: "integer", Description: "Last line (inclusive)"},
				"ranges":     {Type: "string", Description: "Several line ranges in one call, e.g. '10-40,90-120' (numbered)"},
				"pattern":    {Type: "string", Description: "Regex (case-insensitive): return matching lines with context, numbered"},
				"context":    {Type: "integer", Description: "Lines around each pattern match (default 2)"},
				"symbol":     {Type: "string", Description: "Read only the declaration and body of this symbol via AST (e.g. 'FunctionName' or 'ClassName') without reading the rest of the file"},
				"numbered":   {Type: "boolean", Description: "Prefix lines with their number"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "code_outline",
		Description: "List a source file's declarations (funcs, types, classes, methods, routes) as 'L<line>: <signature>', or find where a symbol is declared across a directory (skips .git, node_modules, dist, vendor...). Use before read_file or grep to locate code. Go, C/C++, TS/JS, Python, shell, PowerShell.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "'local' (default) or a remote host; directories only on 'local'"},
				"path":   {Type: "string", Description: "File or directory"},
				"symbol": {Type: "string", Description: "Name or part of it (case-insensitive); omit for the full outline"},
				"max":    {Type: "integer", Description: "Max results (default 200)"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "code_graph",
		Description: "Code graph of a local repository (incremental index, built on first use, refreshed on every call): 'callers' = where a symbol is called or referenced; 'def' = where it is declared (line range, signature); 'impact' = files that use what a file or symbol defines (check before refactoring). Go via go/ast; C/C++, TS/JS, Python, shell by line scanners; matched by name, no type resolution.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action":    {Type: "string", Description: "'callers', 'def', 'impact' or 'index' (full rebuild)"},
				"symbol":    {Type: "string", Description: "Symbol name (pkg.Name accepted) for callers/def/impact"},
				"target":    {Type: "string", Description: "File (relative to repo or absolute) for impact"},
				"repo_path": {Type: "string", Description: "Repository root or any path inside it (host 'local')"},
				"max":       {Type: "integer", Description: "Max lines (default 200)"},
			},
			Required: []string{"action", "repo_path"},
		},
	},
	{
		Name:        "edit_file",
		Description: "Change an existing file (local or remote) by exact text replacement: 'target' -> 'replacement', or several changes in one call with 'edits' (applied in order, all or nothing). Line endings (LF/CRLF) are matched automatically. Prefer this over write_file for any change to an existing file.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":        {Type: "string", Description: "'local' (default) or a configured host"},
				"path":        {Type: "string", Description: "File to change"},
				"target":      {Type: "string", Description: "Exact text to replace (first occurrence)"},
				"replacement": {Type: "string", Description: "New text (empty deletes the target)"},
				"symbol":      {Type: "string", Description: "Symbol name to replace (replaces its entire declaration/body via AST without needing target text)"},
				"edits":       {Type: "array", Description: "Several changes: [{target, replacement}, ...]", Items: &PropertyDef{Type: "object", Description: "{target, replacement}"}},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "write_file",
		Description: "Create a file (local or remote). An existing non-empty file is refused unless overwrite is true; to change parts of a file use edit_file.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":      {Type: "string", Description: "'local' (default) or a configured host"},
				"path":      {Type: "string", Description: "Destination file"},
				"content":   {Type: "string", Description: "File content"},
				"atomic":    {Type: "boolean", Description: "Write to a temp file and rename it into place"},
				"overwrite": {Type: "boolean", Description: "Replace an existing non-empty file entirely"},
			},
			Required: []string{"path", "content"},
		},
	},
	{
		Name:        "file_ops",
		Description: "File system operations without a shell: copy, move, delete, mkdir, list. Use it instead of ls, dir, rm or mkdir through exec.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "'local' (default) or a configured host"},
				"action": {Type: "string", Description: "'copy', 'move', 'delete', 'mkdir' or 'list'"},
				"path":   {Type: "string", Description: "Source file or directory"},
				"target": {Type: "string", Description: "Destination (required for copy and move)"},
			},
			Required: []string{"action", "path"},
		},
	},
	{
		Name:        "job",
		Description: "Background jobs that outlive the call (dev servers, watchers, tunnels, anything over ~30 s): systemd-run on Linux hosts, a named Job Object with a log file on Windows 'local' (max 12 active, 12 h lifetime; 'stop' kills the whole tree). 'wait' blocks up to 45 s per call and returns only the log lines that are new since the previous wait.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":    {Type: "string", Description: "{hosts}"},
				"action":  {Type: "string", Description: "'start', 'wait', 'status', 'list', 'tail' or 'stop'"},
				"command": {Type: "string", Description: "Shell line for 'start' (command OR argv)"},
				"argv":    {Type: "array", Description: "No-shell alternative for 'start': [program, arg1, ...]", Items: &PropertyDef{Type: "string"}},
				"cwd":     {Type: "string", Description: "Working directory for 'start'"},
				"job_id":  {Type: "string", Description: "Job id for wait/status/tail/stop"},
				"lines":   {Type: "integer", Description: "Lines for 'tail' (default 30)"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "tool_manage",
		Description: "On-demand tool modules ('sql', 'hosts', 'docker'), kept out of the schema to save tokens: 'search' tools by keyword, 'list' modules, 'enable'/'disable' a module (sends list_changed), or 'call' a module tool directly without loading it.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action": {Type: "string", Description: "'search', 'list', 'enable', 'disable' or 'call'"},
				"module": {Type: "string", Description: "Module name, e.g. 'sql'"},
				"query":  {Type: "string", Description: "Keyword for 'search'"},
				"tool":   {Type: "string", Description: "Tool name for 'call'"},
				"params": {Type: "string", Description: "JSON arguments for 'call'"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "handoff",
		Description: "Session handoff and long-term memory built from real telemetry (no LLM): 'latest' returns the last project briefing (~300 tokens) for a cold start; 'save' records the session (files touched, failures to avoid, next steps) in Markdown; 'search' finds past decisions; 'list' lists handoffs.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action":  {Type: "string", Description: "'latest' (default), 'save', 'search' or 'list'"},
				"topic":   {Type: "string", Description: "Session topic for 'save', e.g. 'auth-refactor'"},
				"notes":   {Type: "string", Description: "Session summary for 'save'"},
				"pending": {Type: "string", Description: "Pending items or next steps for 'save'"},
				"query":   {Type: "string", Description: "Search term for 'search'"},
				"limit":   {Type: "integer", Description: "Max results for search/list (default 5)"},
			},
		},
	},
}
