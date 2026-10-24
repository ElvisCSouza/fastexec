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
| `exec` | `host`, `command` **ou** `argv`, `cwd`, `input`, `timeout_ms` | Execução síncrona. `command` é linha de shell; `argv` roda sem shell (ver seção do host `local`) |
| `read_file` | `host`, `path`, `offset`, `limit` | Leitura determinística de arquivo local ou remoto |
| `write_file` | `host`, `path`, `content`, `atomic` | Escrita atômica ou padrão em arquivo. Retorna `ok N bytes` |
| `get_file` | `host`, `remote_path`, `local_path` | Download determinístico via socket binário direto. Retorna `ok N bytes` |
| `put_file` | `host`, `local_path`, `remote_path` | Upload determinístico chunked via socket binário. Retorna `ok N bytes` |
| `sql` | `host`, `db_path`, `query` | Execução direta de SQLite com saída tabular ou JSON |
| `job` | `host`, `action`, `cmd`, `id`, `lines` | Gerenciamento de background jobs assíncronos (`start`, `status`, `tail`, `stop`, `list`) |
| `ping` | `host` | Diagnóstico de latência e saúde de conexão por nó |

## Convenções de Rede
- `noteserver`: `192.168.31.142:9142`
- `ax6000`: `192.168.31.1:9142`
- `ax3000`: `192.168.31.34:9142`
- `local`: `PC-Elvis` (Win32 direto)

## Retorno da tool `exec` (`internal/tools/outclean.go`, `formatExecResult`)

Aplicado a stdout e stderr, sempre, antes de montar a resposta:
1. Remove sequências ANSI (CSI e OSC).
2. Linhas consecutivas idênticas viram `<linha> ×N` (CRLF preservado).
3. Linha com mais de 400 caracteres vira `<400 chars>…(+N chars)`.
4. Exit 1 com stdout e stderr vazios responde `sem resultado (exit 1)` com `isError=false` e é gravado como sucesso (`success=1`, `no_result=1`). Exit 1 com alguma saída e demais exits não-zero mantêm `[exit N] ...` com `isError=true` e `success=0`.
5. Teto: saída limpa acima de 256 KB devolve os primeiros 16 KB e a linha final `[truncado: <total> bytes; só os primeiros 16 KB foram devolvidos]`. Até 256 KB nada é cortado.

Saída limpa (sem ANSI, sem repetição, sem linha longa, até 256 KB) é idêntica byte a byte à saída bruta.

6. Timeout (host `local`): resposta `[timeout após X.Xs; árvore de processos encerrada]`, seguida de `Saída parcial capturada até o corte:` e do que foi emitido, com `isError=true`. O agente sabe com certeza que o comando foi interrompido.

Telemetria (`internal/telemetry`): tabela `audit` ganhou `truncated` e `no_result` (migração `setupAuditSchema`, `ALTER TABLE ... ADD COLUMN` só se faltar). `RenderStats` mostra por ferramenta P50/P90/P99/máximo de `bytes_out`, contagem de truncados e de "sem resultado", e os 5 maiores retornos do período (comando limitado a 60 caracteres).

## Execução no host `local` (`internal/transport/local.go`)

Contrato síncrono e determinístico. Cada linha da tabela é uma regra com o motivo.

| Regra | Como | Por quê |
|---|---|---|
| Timeout encerra a árvore inteira | cada comando entra num Windows Job Object (`KILL_ON_JOB_CLOSE`, `job_windows.go`); no estouro de `timeout_ms` o job é terminado e `cmd.WaitDelay` (150 ms) solta pipes presos | antes só o `cmd.exe` morria; um `node` neto segurava o pipe e a chamada ficava até o filho acabar, sem aviso de timeout |
| Nada sobrevive à chamada | ao fim da chamada o handle do job é fechado e o que ainda estiver vivo na árvore morre | sem processo órfão consumindo CPU ou segurando porta/arquivo. **Consequência:** processo iniciado em segundo plano (`start`, `Start-Process`, servidor) morre quando a chamada termina; não há breakaway |
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
