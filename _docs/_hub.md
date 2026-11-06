# FastExec — MCP Server & CLI Multihost Unificado

Ponte nativa em Go entre o PC-Elvis (Windows) e a infraestrutura do HomeLab (NoteServer, AX6000, AX3000, Local). Substitui o Desktop-Commander e wrappers PowerShell intermediários.

## Arquitetura
```
[LLM (Claude / Antigravity)]
       │ (JSON-RPC 2.0 via stdio)
       ▼
fastexec.exe (W:\ReposHomeLab\fastexec\bin\fastexec.exe)
       ├── host: "local"      ──> Win32 CreateProcess (sem powershell.exe)
       ├── host: "noteserver" ──> TCP :9142 (Daemon Zig server-exec, ~4ms)
       ├── host: "ax6000"     ──> TCP :9142 (Daemon Zig server-exec aarch64)
       └── host: "ax3000"     ──> TCP :9142 (Daemon Zig server-exec armv7l)
```

## Ferramentas Expostas ao LLM (8 Core Tools + Módulos Dinâmicos Sob Demanda)

Para economizar ~45% de overhead estático de tokens no system prompt (~490 tokens vs ~900 tokens de catálogos inflados), o FastExec expõe por padrão apenas as **8 ferramentas Core essenciais**:

| Tool Core | Parâmetros | Descrição |
|---|---|---|
| `exec` | `host`, `command` **ou** `argv`, `cwd`, `input`, `timeout_ms` | Execução síncrona orientada a eventos (`timeout_ms` padrão 45 s, teto 50 s). Inclui auto-remediação de `cat` e `sleep`, resolução `pwsh` e nudges educativos no rodapé |
| `read_file` | `host`, `path`, `offset`, `limit`, `start_line`, `end_line` | Leitura determinística de arquivo local ou remoto sem subprocessos (suporta paginação por bytes e por linhas) |
| `code_outline` | `host`, `path`, `symbol`, `max` | Declarações (funções, tipos, classes, métodos, rotas HTTP) como `L<linha>: <assinatura>`, por scanners de linha em Go puro (`internal/tools/code_nav.go`): Go, TS/JS, Python, shell, PowerShell. Arquivo: outline (filtrado por `symbol`, substring sem caixa). Diretório (só host `local`): varredura paralela pulando diretórios de ruído e ocultos, `*.min.js`, linhas > 400 colunas e arquivos > 2 MB; saída `caminho:L<n>: ...`, teto `max` (200). Medido neste repo: achar `HandleExec` = 74 bytes em 2,5 ms (grep -rn: 3.249 bytes); outline do `exec.go` = 1.256 bytes (arquivo: 25.805) |
| `write_file` | `host`, `path`, `content`, `atomic` | Escrita atômica ou padrão em arquivo. Retorna confirmação compacta `ok N bytes` |
| `edit_file` | `host`, `path`, `target`, `replacement` | Edição cirúrgica em arquivo existente. Exige correspondência única de `target`, evitando transferir arquivos inteiros |
| `file_ops` | `host`, `action`, `path`, `target` | Operações atômicas no sistema de arquivos (`list`, `copy`, `move`, `delete`, `mkdir`) sem spawn de shell |
| `job` | `host`, `action`, `command` ou `argv`, `cwd`, `job_id`, `lines` | Background jobs assíncronos (`start`, `status`, `tail`, `wait`, `stop`, `list`): systemd-run nos hosts Linux, Job Object no `local` |
| `tool_manage` | `action`, `module`, `query`, `tool`, `params` | Catálogo dinâmico e execução sob demanda (Tool RAG / MCP `listChanged`). Ações: `search`, `list`, `enable`, `disable`, `call` |

### Módulos Especializados Sob Demanda (Zero-Token Inicial)
Ferramentas de uso pontual ou avançado foram modularizadas e são acessadas via `tool_manage`:
- **`sql`**: DuckDB analítico e SQLite via JSON puro (`tool_manage(action='call', tool='sql', params='{"query": "..."}')` ou `enable`).
- **`homelab`**: Ferramentas de infraestrutura e transferência binária: `ping`, `get_file`, `put_file`.
- **`docker`**: Inspeções e logs de containers: `docker_ps`, `docker_logs`.
*Nota de compatibilidade:* Chamadas diretas legadas do cliente para `sql`, `ping`, `get_file` e `put_file` continuam sendo atendidas de forma transparente no dispatcher sem falhas.

### Auto-Remediação Pedagógica e Guardas no `exec`
1. **Auto-Remediação de `cat` / `type` / `Get-Content`**: Execuções isoladas de leitura são interceptadas no ar e convertidas automaticamente para a micro-tool `read_file` (zero subprocessos, sem shell, instantâneo). O conteúdo do arquivo é devolvido acompanhado do aviso pedagógico no topo:
   `[FastExec Auto-Remediation] O comando 'cat ...' foi convertido automaticamente para a tool nativa 'read_file' (zero-process). Na próxima, invoque read_file diretamente.`
2. **Auto-Remediação de `sleep` / `timeout`**: Comandos de pausa artificial são resolvidos via timer nativo em Go (sem spawn de processos inúteis) e retornam:
   `[FastExec] Pausa de Ns concluída. (Dica: para sincronização de serviços, prefira checar a porta/log do alvo).`
3. **Resolução Automática de PowerShell 7 (`pwsh`)**: Chamadas para `powershell` são promovidas para `pwsh` automaticamente, resolvendo caminhos fora da sandbox UWP e garantindo saída em UTF-8 nativo sem BOM, com fallback gracioso para 5.1 se o 7 não estiver instalado.
4. **Hints Contextuais Inteligentes**:
   - Falhas de cmdlets do PowerShell invocados no `cmd.exe` recebem orientações automáticas de uso do `pwsh` ou `file_ops`.
   - Erros comuns de SQL (ex: funções de agregação no GROUP BY) recebem dicas de reescrita sem interromper bruscamente o raciocínio da LLM.

## Convenções de Rede e CWD Padrão
- `noteserver`: `192.168.31.142:9142` (Daemon Zig). Diretório de trabalho padrão automático: `/home/elvis/workspace` (elimina a necessidade de injetar `cd ~/workspace &&` nos comandos).
- `ax6000`: `192.168.31.1:9142` (Daemon Zig aarch64)
- `ax3000`: `192.168.31.34:9142` (Daemon Zig armv7l)
- `local`: `PC-Elvis` (Daemon local residente em `127.0.0.1:9146` com auto-bootstrap transparente ou fallback direto)

## Retorno da tool `exec` (`internal/tools/outclean.go`, `formatExecResult`)

Aplicado a stdout e stderr, sempre, antes de montar a resposta através de esteira determinística consciente do conteúdo (*Content-Aware Minification*):
1. Resolve sobrescritas de Carriage Return (`\r` sem `\n`), mantendo apenas o estado final de barras de progresso (ex: `curl`, `docker pull`, `pip`, `npm`).
2. Remove sequências ANSI (CSI e OSC).
3. **Detecção e Minificação de JSON**: se a saída for JSON válido (`json.Valid`), compacta diretamente via `json.Compact` (eliminando 100% de quebras de linha e recuos desnecessários, economia de 30% a 50% de tokens).
4. Expulsa lixo de terminal conhecido via tabela extensível (`noisePrefixes`): `hint: ` do Git, avisos de update/depreciação de gerenciadores de pacotes (`npm notice`, `npm warn deprecated`, `[notice] A new release of pip`) e avisos de versão de PowerShell.
5. **Preservação de Código vs Compactação de Tabelas**:
   - Código, diffs, YAML e scripts (`def `, `import `, `class `, `diff --git`, etc.): têm sua indentação e espaços preservados **100% intactos**.
   - Tabelas de CLI de sistema (`ps`, `docker ps`, `df -h`, `netstat`, `kubectl`): vãos de 3 ou mais espaços entre colunas viram 2 espaços (`\s{3,} ->   `), cortando de 20% a 40% dos tokens de whitespace sem perder a estrutura tabular.
6. Linhas consecutivas idênticas viram `<linha> ×N` (CRLF preservado).
7. Linha com mais de 400 caracteres vira `<400 chars>…(+N chars)`.
8. Exit 1 com stdout e stderr vazios responde `sem resultado (exit 1)` com `isError=false` e é gravado como sucesso (`success=1`, `no_result=1`). Exit 1 com alguma saída e demais exits não-zero mantêm `[exit N] ...` com `isError=true` e `success=0`.
9. Teto: saída limpa acima de 64 KB devolve os primeiros 16 KB e a linha final `[truncado: <total> bytes; só os primeiros 16 KB foram devolvidos]`. Até 64 KB nada é cortado.

Saída limpa (sem ANSI, sem lixo de terminal, sem repetição, sem linha longa, até 64 KB) é preservada fielmente.

10. Timeout (host `local`): resposta `[timeout após X.Xs; árvore de processos encerrada]`, seguida de `Saída parcial capturada até o corte:` e do que foi emitido, com `isError=true`. O agente sabe com certeza que o comando foi interrompido.

## Ambiente "Interface Zero / Humano Zero" (Execução Headless na Origem)
Todas as execuções locais e remotas injetam o set canônico de variáveis headless:
`CI=1`, `DEBIAN_FRONTEND=noninteractive`, `PAGER=cat`, `GIT_PAGER=cat`, `GIT_TERMINAL_PROMPT=0`, `TERM=dumb`, `NO_COLOR=1`, `POWERSHELL_UPDATECHECK=Off`, `DOTNET_CLI_TELEMETRY_OPTOUT=1`, `NPM_CONFIG_UPDATE_NOTIFIER=false`, `PIP_NO_INPUT=1`, `PIP_DISABLE_PIP_VERSION_CHECK=1`.
Isso impede que ferramentas de terminal abram paginadores (`less`/`more`), peçam confirmações interativas `[y/n]`, mostrem spinners de progresso ou façam checagens de rede de nova versão na origem.

Telemetria (`internal/telemetry`): tabela `audit` ganhou `truncated` e `no_result` (migração `setupAuditSchema`, `ALTER TABLE ... ADD COLUMN` só se faltar). `RenderStats` mostra por ferramenta P50/P90/P99/máximo de `bytes_out`, contagem de truncados e de "sem resultado", e os 5 maiores retornos do período (comando limitado a 60 caracteres).

**`stats <período>` respeita a janela** (`internal/telemetry/stats.go`: `ParsePeriod`, `periodWhere`, `periodDescription`): aceita `Nh` (horas) e `Nd` (dias), sem diferenciar maiúsculas (`stats 24h`, `stats 7d`). O cabeçalho mostra o período (ex.: `Últimos 7 dias`). Sem argumento, o relatório segue como histórico total. `--today` continua valendo e tem precedência. Valores inválidos (`0h`, `7x`) são ignorados e o relatório volta ao histórico total. Teste: `internal/telemetry/stats_period_test.go`.

## Execução no host `local` (`internal/transport/local.go`)

Contrato síncrono e determinístico. Cada linha da tabela é uma regra com o motivo.

| Regra | Como | Por quê |
|---|---|---|
| Timeout encerra a árvore inteira | cada comando entra num Windows Job Object (`KILL_ON_JOB_CLOSE`, `job_windows.go`); no estouro de `timeout_ms` o job é terminado e `cmd.WaitDelay` (150 ms) solta pipes presos | antes só o `cmd.exe` morria; um `node` neto segurava o pipe e a chamada ficava até o filho acabar, sem aviso de timeout |
| Nada sobrevive à chamada | ao fim da chamada o handle do job é fechado e o que ainda estiver vivo na árvore morre | sem processo órfão consumindo CPU ou segurando porta/arquivo. **Consequência:** processo iniciado em segundo plano (`start`, `Start-Process`, servidor) morre quando a chamada termina; não há breakaway. Para isso existe a tool `job`. Antes do close, `job.alive()` conta (uma consulta, sem espera) os processos ainda vivos no job, ignorando o `conhost.exe`; se houver, a resposta ganha `[warning] N child process(es) outlived the command...` sugerindo `job` (`ExecResult.Leftover`) |
| `argv` sem shell | `argv: ["node","-e","...","a > b && %TEMP%"]` vira `exec.Command` direto; `command` e `argv` são exclusivos | `cmd.exe /s /c` interpreta `> < \| & %VAR%` e aspas antes do programa ver o argumento (arquivos-lixo, comando quebrado) |
| Código longo por `input` | `argv ["node","-"]`, `["python","-"]` ou `["powershell","-NoProfile","-Command","-"]` com o script em `input` (stdin). Para `pwsh|powershell ... -Command -` o `input` sempre termina com linha em branco (`pwsh_stdin.go`): sem ela o PowerShell 7 e 5.1 descartam o bloco multilinha final (try/catch, foreach) com exit 0 e saída vazia | evita qualquer escape; não há parâmetro `script` nem `interpreter` (tool sprawl) |
| Dica de cmdlet só no local | a dica "runs commands through cmd.exe" só sai quando `ExecResult.Local` (o comando rodou no próprio host) | em host remoto a dica citava o host local e a sintaxe errada |
| Guard de sleep em argv | só quando `argv[0]` é `sleep`, `timeout` ou um shell (`cmd`, `bash`, `sh`, `zsh`, `powershell`, `pwsh`) | num argv para `node`/`python` o texto é código, não linha de shell: sem falso positivo |
| stdin sem `input` é EOF | `cmd.Stdin` recebe leitor vazio; `GIT_TERMINAL_PROMPT=0` no ambiente | prompt interativo falha na hora em vez de travar a chamada |
| Captura limitada | `capBuffer` guarda os primeiros 3 MiB e os últimos 1 MiB de cada stream e marca o meio descartado | um loop de saída infinita não aloca gigabytes antes de o `outclean.go` agir; o fim (onde costuma estar o erro) é preservado |
| Encodings mistos | `decodeMixed` (`encoding_windows.go`): por linha, UTF-8 válido fica; inválido é lido na code page OEM (`GetOEMCP`, padrão 850) | `node`/`git` emitem UTF-8 e `cmd`/`findstr` emitem OEM no mesmo host; sem isso "não" vira `n\ufffdo` e o hint do PowerShell não dispara. Não usa `chcp`. Torna o modo UTF-8 do Windows desnecessário para o FastExec |
| `cwd` validado | `os.Stat` antes de iniciar; erro `cwd inexistente ou não é diretório` | erro claro em vez do erro críptico do kernel |

Limite conhecido: o processo é posto no job logo após `CreateProcess`; um filho criado nessa janela de microssegundos escaparia. O `cmd.exe` leva mais que isso para inicializar, então na prática a árvore entra inteira.

Hosts remotos (`noteserver`, `ax6000`, `ax3000`): inalterados, exceto `argv`, que vira linha de comando com cada argumento entre aspas simples POSIX.

Testes (`go test ./...`): `internal/transport/local_test.go` (timeout com limpeza da árvore, órfão no fim normal, encodings, argv literal, stdin EOF, cwd, limite de captura) e `internal/tools/exec_argv_test.go` (guard, formato do timeout, hint do PowerShell). Exigem `node` no PATH.

Ativar um binário novo: `go build -o bin\fastexec.new.exe .\cmd\fastexec`; o `fastexec.exe` em uso fica bloqueado pelo Claude Desktop, então fechar o Desktop, trocar o arquivo e reabrir.

## Tool `job` no host `local` (`internal/transport/localjob_windows.go`, `internal/tools/job_local.go`)

Para o que precisa ficar no ar além da chamada (dev server, watcher, túnel) e para qualquer comando acima de ~3 min. Mesma assinatura da tool: `host: "local"`, `action`, `command` ou `argv`, `cwd`, `job_id`, `lines`.

| Ação | O que faz |
|---|---|
| `start` | inicia em background e devolve na hora o `id` e o caminho do log; `job_id` opcional escolhe o id (letras, números, `_`, `-`) |
| `status` | `RUNNING`, `EXITED`, `STOPPED` ou `EXPIRED`, processos ativos, tempo no ar, último log, tamanho do log |
| `tail` | últimas `lines` linhas do log (padrão 30, máximo 500) com a limpeza do `outclean.go` |
| `wait` | espera o job terminar por até 200 s; passou disso, responde "ainda em execução" com o fim do log |
| `stop` | encerra a árvore inteira pelo nome do Job Object |
| `list` | jobs conhecidos, do mais novo ao mais velho (20 linhas no máximo) |

| Regra | Como | Por quê |
|---|---|---|
| Job Object nomeado, handle guardado pelo dono | `Local\FastExec_Job_<id>`, criado com `KILL_ON_JOB_CLOSE`; o FastExec que iniciou o job guarda o handle enquanto o job viver | **Medido:** o nome de um job object só resolve enquanto alguém mantém um handle aberto. Fechar o último handle remove o nome mesmo com o processo vivo, e o processo vira órfão inalcançável. Efeito: o job sobrevive às chamadas, **não a um restart do FastExec** (que o encerra, sem órfão). Outro FastExec vê e para o job pelo nome enquanto o dono estiver vivo |
| Parada por nome do job, nunca por PID | `OpenJobObjectW` + `TerminateJobObject` | o Windows recicla PID na hora; matar por PID derruba programa de terceiros |
| `status` sem lista de PIDs | contabilidade do job (`ActiveProcesses`) | mesma informação e sem tocar em PID |
| Sem breakaway | o job aninha dentro de qualquer job do host (Windows 8+, inclusive o do Claude Desktop/MSIX) | `CREATE_BREAKAWAY_FROM_JOB` falharia com acesso negado |
| Log direto em arquivo | stdout e stderr do processo vão para `<id>.log` (append), sem pipe nem buffer no FastExec | zero memória presa; o log sobrevive ao job |
| Estado em disco | `<id>.json` (id, comando ou argv, cwd, pid informativo, created_at, nome do job, caminho do log, estado), escrita atômica | outro FastExec e o `list` leem o mesmo estado |
| Pasta | `FASTEXEC_JOBS_DIR` se definida; senão `W:\Data\jobs` se a unidade W: existir; senão `%LOCALAPPDATA%\fastexec\jobs` | W: é o Dev Drive do PC-Elvis |
| Higiene | no máximo 5 jobs ativos (o 6º falha dizendo qual parar); vida máxima de 12 h desde a criação (encerrado ao consultar); arquivos de jobs finalizados são apagados após 7 dias | nada fica no ar para sempre |
| Comando que falha na hora | o `start` não falha: o job aparece `EXITED` e o `tail` mostra a mensagem | o agente vê o erro real |

Timeout do `exec`: `DefaultTimeout` é 45 s e `timeout_ms` acima de 50 s é limitado a 50 s (`MaxClientTimeoutMs`), porque o Claude Desktop corta chamadas de dispositivos em 60 s; o padrão deixa 15 s de margem e encerra a árvore de processos sem quebrar a sessão do chat. Quem estoura recebe `[timeout após X.Xs; árvore de processos encerrada]`, a saída parcial e a orientação de usar `job`. O `wait` também espera no máximo 35 s (`jobWaitMax`), no local e nos hosts Linux: se o job segue ativo no prazo, a resposta é `[job X ainda em execução após 35s; chame 'wait' de novo ou use 'tail']` seguida do fim do log. Isso possibilita polling contínuo sem nunca estourar o limite de 60 s do Claude Desktop.

Testes: `internal/transport/localjob_test.go` (ciclo start, status, tail, stop sem órfão; job que sobrevive a um `exec`; limite de 5; vida máxima; término espontâneo; comando que falha na hora; id e cwd) e `internal/tools/job_local_test.go`, `internal/tools/exec_timeout_test.go`.

## Módulo `internal/env`: Montagem de Ambiente Windows

**Responsabilidade**: Garantir que todos os processos filhos do fastexec (exec, job, worker.ps1, headless, builds) vejam as mesmas variáveis de ambiente de um terminal aberto pelo Explorer (Windows Registry: Machine + User).

**Regra**:
1. Começa com variáveis herdadas (de `os.Environ()`)
2. Sobrepõe todas as variáveis da seção **Machine** do registry (exceto `Path` e variáveis só-do-sistema como `USERNAME=SYSTEM`)
3. Sobrepõe todas as variáveis da seção **User** do registry (exceto `Path`)
4. Path especial (case-insensitive): `Machine:Path` + `User:Path` + partes do herdado não duplicadas

**Variáveis de cache canônicas** (verificadas por `env-assert.ps1`):
- `GRADLE_USER_HOME`, `GOPATH`, `GOMODCACHE`, `GOCACHE`, `GOLANGCI_LINT_CACHE`
- `npm_config_cache`, `XDG_CACHE_HOME`, `NUGET_PACKAGES`, `PIP_CACHE_DIR`, `CARGO_HOME`
- `JAVA_HOME`, `ANDROID_HOME`

**Implementação**:
- `registry.go`: Lê registry via PowerShell → `GetEnvironmentVariables('Machine')` + `GetEnvironmentVariables('User')`
- `env.go`: Máquina de montagem (`BuildEnv`) + slice (`ToSlice`) + ponto de entrada (`MountForCurrentProcess`)
- Falha ao ler registry: loga stderr, retorna `os.Environ()` (nunca falha)

**Integração**:
- `internal/transport/local.go`: `ExecLocalWithInput` chama `env.MountForCurrentProcess()` antes de criar processo via `cmd.Cmd.Env`

**Hosts remotos**: Inalterado — apenas `local` monta a partir do registry

**Validação** (deve passar após build):
```powershell
# Pré-requisito: variáveis de cache definidas no registro (User ou Machine)
# Teste 1: variáveis de cache presentes
& W:\ReposHomeLab\.agent\env-assert.ps1
# deve sair com exit 0

# Teste 2: binário de validação enxerga valores do registro
& %TEMP%\fastexec-val.exe exec "go env GOCACHE"
# deve retornar o valor de GOCACHE do registro (User ou Machine), não vazio

# Teste 3: worker.ps1 run valida ambiente
cd W:\ReposHomeLab
& .\worker.ps1 run | head -n 20
# deve sair com 0 (passou em env-assert.ps1)
```

## Protocolo SX e pool de conexões (06/nov/2026)

- **Daemon (`ops/tools/server-exec/main.zig`, noteserver):** além do `SE` (linha de shell, formato legado inalterado), entende `SX` (magic 0x5358): pedido de 16 bytes (magic, flags, cwd_len, argc, body_len, stdin_len) + cwd + body + stdin; resposta de 16 bytes (magic, flags, exit, stdout_len, stderr_len) + stdout + stderr. Flag 1 = `argv` direto (strings terminadas em NUL, sem shell); sem a flag, `bash -c`. O servidor aplica o ambiente não interativo (CI, TERM=dumb, PAGER=cat, ...). E/S com buffer, conexão ociosa fecha em 10 min, e o filho é morto se o cliente fecha o socket. `SERVER_EXEC_PORT` muda a porta (testes).
- **Cliente (`internal/transport`):** `daemon_pool.go` reaproveita conexões (goroutine vigia por conexão ociosa; comando enviado nunca é reenviado) e fala SX nos daemons que suportam (sonda `true` por host, em cache; daemon antigo cai no SE). `DispatchArgv` usa SX direto; `DispatchWithInput` usa `SimpleArgv` (função pura: só "programa args", sem metacaractere nem comando interno do bash) para ir sem shell e, no resto, SX com `bash -c`, `cwd` e `stdin` como campos.
- **Chaves de desligar:** `FASTEXEC_NO_DIRECT=1` (tudo pelo bash) e `FASTEXEC_NO_SX=1` (volta ao SE puro).
- **Medido PC -> noteserver:** `ls -d /` 7,2 ms -> 3,2 ms; comando dominado pelo programa (git) não muda. Medição: `FASTEXEC_BENCH=1 go test -run MedicaoViaRede ./internal/transport/`; no servidor: `ops/tools/server-exec/bench/`.
- **Reversão:** cliente: `bin\fastexec-old.exe` (renomear de volta para `fastexec.exe`); servidor: `ops/backups/server-exec-pre-v2/server-exec`. Roteadores e Firesticks seguem com o daemon antigo (sem SX), por isso o cliente os trata pelo SE.
- **Limite do SE em daemon Windows:** o caminho SE monta linha POSIX (`export CI=1 ...;`, `cd "..." &&`, `printf | base64 -d |` para `input`). Daemon Windows sem SX (build anterior a 06/nov) executa isso no PowerShell: erro de `export` em todo comando, `input` e `cwd` quebrados e o resto da linha roda mesmo assim. Correção: atualizar o daemon Windows para o build com SX (`server-exec-windows.exe`).

## Guard de busca no `exec` (`internal/tools/search_guard.go`)

- **Exclusões automáticas:** `grep`/`egrep`/`fgrep` recursivo (`-r`, `-R`, `--recursive`) sobre `.`/`./` ou sem caminho, e sem `--exclude-dir` próprio, recebe `--exclude-dir=` para `.git`, `node_modules`, `dist`, `build`, `vendor`, `.next`, `.agent`, `tmp`, `cache`, `__pycache__` e `--exclude=` para `*.min.js`, `*.min.css`, `*.map`, `package-lock.json`, `pnpm-lock.yaml`, logo depois do programa (`command` e `argv`). Uma flag por item, nunca `{a,b}`: a expansão de chaves só existe no bash (quebraria no `cmd.exe` e no argv do SX). `rg` não é tocado (já respeita `.gitignore`). Sem resultado com exclusão aplicada → nota dizendo o que foi pulado.
- **Corte de saída:** pipeline com busca (`grep`, `rg`, `findstr`, `git grep`) sem `head`/`tail`/`wc`/`Select-Object` depois mantém as 40 primeiras linhas e anexa `[FastExec Guard] Search returned N lines...` sugerindo `--include`, subpasta, `-l` ou `code_outline`.
- **Desligar:** `FASTEXEC_NO_SEARCH_GUARD=1`. Testes: `internal/tools/code_nav_test.go`.

## Testes e Goldens (`internal/golden`)

Goldens travam o que o LLM e o CLI veem. Cada um é um arquivo versionado em `testdata/golden/` do pacote que o gera; `golden.Check` compara a saída e, com `-update`, regrava o arquivo.

| Golden | Pacote | O que trava |
|---|---|---|
| `tools_list_{core,docker,sql,homelab,all}.json` | `internal/mcp` | `tools/list` no fio: `wire_bytes` (tamanho da linha JSON-RPC com `\n`) e o array de tools (nomes, descrições, schemas) do núcleo e de cada módulo sob demanda |
| `exec_format.txt` | `internal/tools` | `formatExecResult` com entradas fixas: sucesso, exit 1 sem saída, erro com exit N, dica de cmdlet, timeout com e sem saída parcial, truncamento |
| `host_resolution.txt` | `internal/transport` | nome → alvo de cada host conhecido, host padrão via `FASTEXEC_DEFAULT_HOST`, erro de host desconhecido |
| `cli_usage.txt` | `cmd/fastexec` | ajuda, comando desconhecido e erros de argumento do CLI; só caminhos que saem antes de rede, banco ou comando real |

**Regenerar** (só quando a mudança for intencional; revise o diff dos goldens antes de commitar):

```
go test ./internal/mcp ./internal/tools ./internal/transport ./cmd/fastexec -run Golden -update
```

Regras:
- Offline, independentes do host, do registro e de `W:\`: env pinado nos testes de host; o CLI roda com `HOME`/`USERPROFILE` num diretório temporário.
- O CLI é testado re-executando o próprio binário de teste (`TestMain`, marcador `__fastexec_cli_golden__`), porque `main()` chama `os.Exit`.
- `mcp.Server.serve(io.Reader)` recebe a entrada; `Run()` só liga o stdin e o stdout. O teste monta o `Server{}` direto, sem `telemetry.Init()`.
- A ordem do `tools/list` é determinística: módulos por nome e tools por nome dentro de cada módulo.
- `json.Marshal` escreve `&` como `\u0026` nas descrições; o golden registra a saída real do fio.
- Tamanho atual do `tools/list` do núcleo: **8605 bytes** (`tools_list_core.json`, `wire_bytes`).
