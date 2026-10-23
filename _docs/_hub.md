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
| `exec` | `host`, `cmd`, `timeout_ms` | Execução síncrona sem escape de aspas ou pipes |
| `read_file` | `host`, `path`, `offset`, `limit` | Leitura determinística de arquivo local ou remoto |
| `write_file` | `host`, `path`, `content`, `atomic` | Escrita atômica ou padrão em arquivo |
| `get_file` | `host`, `remote_path`, `local_path` | Download determinístico via socket binário direto |
| `put_file` | `host`, `local_path`, `remote_path` | Upload determinístico chunked via socket binário |
| `sql` | `host`, `db_path`, `query` | Execução direta de SQLite com saída tabular ou JSON |
| `job` | `host`, `action`, `cmd`, `id`, `lines` | Gerenciamento de background jobs assíncronos (`start`, `status`, `tail`, `stop`, `list`) |
| `ping` | `host` | Diagnóstico de latência e saúde de conexão por nó |

## Convenções de Rede
- `noteserver`: `192.168.31.142:9142`
- `ax6000`: `192.168.31.1:9142`
- `ax3000`: `192.168.31.34:9142`
- `local`: `PC-Elvis` (Win32 direto)

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
