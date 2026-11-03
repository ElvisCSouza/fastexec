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

## Ferramentas Expostas ao LLM
| Tool | Parâmetros | Descrição |
|---|---|---|
| `exec` | `host`, `command` **ou** `argv`, `cwd`, `input`, `timeout_ms` | Execução síncrona (`timeout_ms` padrão 200 s, teto 210 s). `command` é linha de shell; `argv` roda sem shell (ver seção do host `local`) |
| `read_file` | `host`, `path`, `offset`, `limit` | Leitura determinística de arquivo local ou remoto |
| `write_file` | `host`, `path`, `content`, `atomic` | Escrita atômica ou padrão em arquivo. Retorna `ok N bytes` |
| `get_file` | `host`, `remote_path`, `local_path` | Download determinístico via socket binário direto. Retorna `ok N bytes` |
| `put_file` | `host`, `local_path`, `remote_path` | Upload determinístico chunked via socket binário. Retorna `ok N bytes` |
| `sql` | `host`, `db_path`, `query` | Execução direta de SQLite com saída tabular ou JSON |
| `job` | `host`, `action`, `command` ou `argv`, `cwd`, `job_id`, `lines` | Background jobs assíncronos (`start`, `status`, `tail`, `wait`, `stop`, `list`): systemd-run nos hosts Linux, Job Object no `local` (seção abaixo) |
| `ping` | `host` | Diagnóstico de latência e saúde de conexão por nó |
| `tool_manage` | `action`, `module`, `query`, `tool`, `params` | Catálogo dinâmico e execução sob demanda (Tool RAG / `listChanged`). Evita inchar o system prompt com dezenas de ferramentas. Ações: `search`, `list`, `enable`, `disable`, `call` |

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

## Execução no host `local` (`internal/transport/local.go`)

Contrato síncrono e determinístico. Cada linha da tabela é uma regra com o motivo.

| Regra | Como | Por quê |
|---|---|---|
| Timeout encerra a árvore inteira | cada comando entra num Windows Job Object (`KILL_ON_JOB_CLOSE`, `job_windows.go`); no estouro de `timeout_ms` o job é terminado e `cmd.WaitDelay` (150 ms) solta pipes presos | antes só o `cmd.exe` morria; um `node` neto segurava o pipe e a chamada ficava até o filho acabar, sem aviso de timeout |
| Nada sobrevive à chamada | ao fim da chamada o handle do job é fechado e o que ainda estiver vivo na árvore morre | sem processo órfão consumindo CPU ou segurando porta/arquivo. **Consequência:** processo iniciado em segundo plano (`start`, `Start-Process`, servidor) morre quando a chamada termina; não há breakaway. Para isso existe a tool `job` |
| `argv` sem shell | `argv: ["node","-e","...","a > b && %TEMP%"]` vira `exec.Command` direto; `command` e `argv` são exclusivos | `cmd.exe /s /c` interpreta `> < \| & %VAR%` e aspas antes do programa ver o argumento (arquivos-lixo, comando quebrado) |
| Código longo por `input` | `argv ["node","-"]`, `["python","-"]` ou `["powershell","-NoProfile","-Command","-"]` com o script em `input` (stdin) | evita qualquer escape; não há parâmetro `script` nem `interpreter` (tool sprawl) |
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
