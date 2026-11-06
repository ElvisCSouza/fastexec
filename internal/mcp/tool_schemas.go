package mcp

// tool_schemas.go: schemas das tools nativas (o que o LLM vê no tools/list). Só dados: o handler de cada uma está
// em tool_handlers.go e os goldens em testdata/golden travam o conteúdo.

var serverTools = []ToolSchema{
	{
		Name:        "exec",
		Description: "Executa comando síncrono rápido no nó alvo (orientado a eventos, máx 45s). CWD PADRÃO NO NOTESERVER É ~/workspace (NUNCA prefixe comandos com 'cd ~/workspace &&' ou 'cd ~/workspace;'). Para outro diretório prefira o parâmetro 'cwd'. Para ler arquivos prefira 'read_file'; para gerenciar arquivos/diretórios prefira 'file_ops'; para tarefas longas (> 30s) use a tool 'job' (action='start'). Dois modos: 'command' (linha de shell; no Windows 'local' roda em cmd.exe) ou 'argv' (sem shell: cada item chega ao programa exatamente como enviado; use para código e texto com metacaracteres). Código longo vai por 'input' (stdin): argv ['node','-'] ou ['python','-'] com script em 'input'. Para PowerShell use argv ['pwsh','-NoProfile','-Command','-'] com script em 'input'. No estouro de timeout_ms a árvore de processos é encerrada e a resposta traz a saída parcial. stdin sem 'input' é EOF imediato.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "Alvo: 'noteserver' (default, cwd ~/workspace), 'ax6000', 'ax3000', 'local', 'vm-ia-core' ou prefixo 'ssh:<host>'"},
				"command":    {Type: "string", Description: "Linha de shell a executar (PROIBIDO usar sleep/timeout artificial; NUNCA prefixe com 'cd ~/workspace &&' pois o cwd padrão do noteserver JÁ É ~/workspace). Informe command OU argv"},
				"argv":       {Type: "array", Description: "Alternativa sem shell: [executável, arg1, arg2...]. Nenhum metacaractere é interpretado. Informe argv OU command", Items: &PropertyDef{Type: "string"}},
				"cwd":        {Type: "string", Description: "Diretório de trabalho para execução (opcional, padrão no noteserver: ~/workspace; para rodar em subpastas passe ex: 'homeassistant/config')"},
				"input":      {Type: "string", Description: "Conteúdo enviado diretamente para o stdin do processo (opcional, evita criar arquivos temporários)"},
				"timeout_ms": {Type: "integer", Description: "Teto de segurança em ms (padrão: 45000; máximo: 50000, valores maiores são limitados para respeitar o corte de 60s do Claude Desktop). Ao estourar, a árvore de processos é encerrada. Comando que precisa de mais tempo: tool 'job'"},
			},
		},
	},
	{
		Name:        "read_file",
		Description: "Lê conteúdo de arquivo local ou remoto. Use SEMPRE para inspecionar arquivos ou trechos (offset/limit) em vez de 'cat', 'type' ou 'head' via exec.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":       {Type: "string", Description: "Alvo: 'local' (default, host atual), 'noteserver', 'ax6000', 'ax3000'"},
				"path":       {Type: "string", Description: "Caminho do arquivo"},
				"offset":     {Type: "integer", Description: "Offset em bytes (opcional)"},
				"limit":      {Type: "integer", Description: "Limite de bytes a ler (opcional)"},
				"start_line": {Type: "integer", Description: "Linha inicial a ler (1-based, opcional)"},
				"end_line":   {Type: "integer", Description: "Linha final a ler (inclusive, opcional)"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "code_outline",
		Description: "List a source file's declarations (funcs, types, classes, methods, routes) as 'L<line>: <signature>', or find where a symbol is declared across a directory (skips .git, node_modules, dist, vendor...). Use before read_file or grep to locate code. Go, TS/JS, Python, shell, PowerShell.",
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
		Description: "Code graph of a local repository (incremental index, built on first use, refreshed on every call): 'callers' = where a symbol is called or referenced; 'def' = where it is declared (line range, signature); 'impact' = files that use what a file or symbol defines (check before refactoring). Go via go/ast; TS/JS, Python, shell by line scanners; matched by name, no type resolution.",
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
		Description: "Operações atômicas no sistema de arquivos sem shell: copy, move, delete, mkdir, list. Use para listar diretórios ou gerenciar arquivos em vez de 'ls', 'dir', 'rm' ou 'mkdir' via exec.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":   {Type: "string", Description: "Alvo: 'local' (default) ou 'noteserver'"},
				"action": {Type: "string", Description: "Ação a executar: 'copy', 'move', 'delete', 'mkdir', 'list'"},
				"path":   {Type: "string", Description: "Caminho do arquivo ou diretório de origem"},
				"target": {Type: "string", Description: "Caminho de destino (obrigatório para 'copy' e 'move')"},
			},
			Required: []string{"action", "path"},
		},
	},
	{
		Name:        "job",
		Description: "Gerencia background jobs assíncronos: no NoteServer via systemd-run; no host 'local' (Windows) em Job Object nomeado, com log em arquivo, sobrevivendo ao fim da chamada (dev server, watcher, túnel). Use para qualquer comando que precise de mais de ~30s ou ficar no ar; o 'exec' é síncrono e encerra tudo ao terminar. No local: máx. 5 jobs ativos e vida máxima de 12 h; 'stop' encerra a árvore inteira. Use action='wait' para aguardar conclusão por evento (espera até 35s por chamada com retorno fluido de progresso e log parcial caso ainda esteja em execução, permitindo polling contínuo sem timeout).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"host":    {Type: "string", Description: "Alvo: 'noteserver' (default) ou 'local' (Windows)"},
				"action":  {Type: "string", Description: "Ação: 'start', 'wait' (aguarda conclusão por evento), 'status', 'list', 'tail', 'stop'"},
				"command": {Type: "string", Description: "Linha de shell para ação 'start' (informe command OU argv)"},
				"argv":    {Type: "array", Description: "Alternativa sem shell para 'start': [executável, arg1, ...]", Items: &PropertyDef{Type: "string"}},
				"cwd":     {Type: "string", Description: "Diretório de trabalho para 'start' (opcional)"},
				"job_id":  {Type: "string", Description: "Identificador da tarefa para wait/status/tail/stop"},
				"lines":   {Type: "integer", Description: "Número de linhas para ação 'tail' (padrão: 30)"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "tool_manage",
		Description: "Pesquisa, ativa e executa módulos de ferramentas especializadas sob demanda no FastExec (Tool RAG / Dynamic Loading, economizando tokens no system prompt). Módulos disponíveis: 'sql', 'homelab', 'docker'. Ações: 'search' (pesquisa ferramentas por palavra-chave), 'list' (mostra módulos disponíveis), 'enable' (carrega módulo no MCP com list_changed), 'disable' (descarrega módulo), 'call' (executa ferramenta diretamente sem carregá-la no schema).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action": {Type: "string", Description: "Ação: 'search', 'list', 'enable', 'disable', 'call'"},
				"module": {Type: "string", Description: "Nome do módulo (ex: 'sql', 'homelab', 'docker')"},
				"query":  {Type: "string", Description: "Termo de busca para a ação 'search'"},
				"tool":   {Type: "string", Description: "Nome da ferramenta para a ação 'call'"},
				"params": {Type: "string", Description: "JSON serializado com argumentos para a ação 'call'"},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "handoff",
		Description: "Gerencia a memória de longo prazo e handoff entre sessões e agentes (Zero-LLM overhead, baseado em telemetria real). Ações: 'latest' (retorna o último briefing do projeto em ~300 tokens para cold-start imediato), 'save' (consolida a sessão atual com arquivos modificados, falhas a evitar e próximos passos em Markdown), 'search' (pesquisa histórico de decisões em Markdown), 'list' (lista handoffs anteriores).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"action":  {Type: "string", Description: "Ação: 'latest' (default), 'save', 'search', 'list'"},
				"topic":   {Type: "string", Description: "Tema ou módulo da sessão para ação 'save' (ex: 'popcorn-cache', 'matter-hub')"},
				"notes":   {Type: "string", Description: "Resumo ou notas da sessão para 'save' (opcional)"},
				"pending": {Type: "string", Description: "Itens pendentes ou próximos passos para 'save' (opcional)"},
				"query":   {Type: "string", Description: "Termo de busca para a ação 'search'"},
				"limit":   {Type: "integer", Description: "Limite de resultados para 'search' ou 'list' (padrão: 5)"},
			},
		},
	},
}
