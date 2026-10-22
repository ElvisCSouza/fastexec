# Commander — MCP Server & CLI Multihost Unificado

Ponte nativa em Go entre o PC-Elvis (Windows) e a infraestrutura do HomeLab (NoteServer, AX6000, AX3000, Local). Substitui o Desktop-Commander e wrappers PowerShell intermediários.

## Arquitetura
```
[LLM (Claude / Antigravity)]
       │ (JSON-RPC 2.0 via stdio)
       ▼
commander.exe (W:\ReposHomeLab\commander\bin\commander.exe)
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
| `sql` | `host`, `db_path`, `query` | Execução direta de SQLite com saída tabular |
| `job` | `host`, `action`, `cmd`, `id`, `lines` | Gerenciamento de background jobs assíncronos (`start`, `status`, `tail`, `stop`, `list`) |
| `ping` | `host` | Diagnóstico de latência e saúde de conexão por nó |

## Convenções de Rede
- `noteserver`: `192.168.31.142:9142`
- `ax6000`: `192.168.31.1:9142`
- `ax3000`: `192.168.31.34:9142`
- `local`: `PC-Elvis` (Win32 direto)
