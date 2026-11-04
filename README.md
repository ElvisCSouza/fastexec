# FastExec — Runtime de Execução, Otimização e Mediação para LLMs & Agentes

> **Não é "apenas um shell remoto".**  
> Dizer que o FastExec existe apenas para executar comandos remotos é confundir a camada de transporte com o propósito do sistema. O FastExec é um **Runtime de Execução, Engenharia de Contexto e Governança de Processos** projetado especificamente para agentes autônomos e LLMs (Claude Desktop, Antigravity, OpenClaw, Workers de Background).

---

## 1. Por que o FastExec Existe? (A Dor Real)

Modelos de linguagem conectados a terminais comuns sofrem de gargalos graves que geram desperdício financeiro, lentidão e falhas em loop:

1. **Queima Desenfreada de Tokens (*Token Burn*):**
   * Banners de boas-vindas, mensagens de update (`npm notice`, `pip release`, `powershell update`), sequências ANSI de cores, animações e barras de progresso com Carriage Return (`\r`) poluem a janela de contexto.
   * Tabelas de terminal (ex: `docker ps`, `netstat`, `ps aux`) contêm centenas de espaços em branco vazios entre colunas.
   * dumps acidentais de comandos como `cat arquivo_grande.json` ou `SELECT *` estouram a janela de contexto do modelo em uma única rodada.
2. **Corte Súbito de Sessão por Timeout (60 segundos):**
   * Clientes de LLM (como o Claude Desktop) encerram violentamente qualquer chamada MCP que passe de 60 segundos com erro fatal de timeout. Ferramentas padrão que deixam builds longos rodarem soltos quebram o fluxo do usuário.
3. **Processos Órfãos e Travas de Sistema (*Process Leaks*):**
   * Quando uma LLM cancela ou aborta um comando, subprocessos netos (compiladores, servidores Node, instâncias de teste) continuam rodando em segundo plano no Windows/Linux, segurando portas TCP e consumindo 100% de CPU.
4. **Maus Hábitos e Alucinações de Sintaxe:**
   * Agentes insistem em usar `cat` ou `type` para ler arquivos inteiros, inserem `sleep 10` esperando serviços subirem, ou erram sintaxes de SQL/PowerShell, gerando ciclos infinitos de tentativa e erro.
5. **Latência de Inicialização (Cold Start):**
   * Instanciar shells pesados (PowerShell 5.1 leva ~200ms) ou abrir túneis SSH interativos a cada micro-comando adiciona segundos de atraso cumulativo no dia a dia.

---

## 2. Os 5 Pilares Arquiteturais do FastExec

### Pilar 1: Engenharia de Contexto & Minificação Consciente (*Content-Aware*)
O FastExec atua como um filtro cirúrgico bidirecional para tokens:
* **Entrada (System Prompt):** Em vez de expor dezenas de ferramentas no catálogo estático consumindo ~900 tokens por turno, expõe apenas **6 Core Tools** compactas (~480 tokens) e oferece módulos sob demanda via **Tool RAG** (`tool_manage`).
* **Saída (Content-Aware Minification - `outclean.go`):**
  * **JSON Inteligente:** Detecta JSON válido e o minifica inline com `json.Compact` (redução de 30% a 50% de tokens).
  * **Compactação Tabular com Preservação de Código:** Diferencia código-fonte/YAML/diffs (cuja indentação é preservada 100% intacta) de saídas tabulares de CLI (`docker ps`, `df -h`, `netstat`), onde vãos de 3 ou mais espaços viram 2 espaços (`\s{3,} ->   `), eliminando tokens de whitespace sem perder a estrutura visual.
  * **Eliminação de Ruído de CLI:** Remove sequências ANSI, unifica barras de progresso `\r` no estado final, condensa linhas repetidas (`<linha> ×N`) e descarta avisos conhecidos de gerenciadores de pacotes.
  * **Capping Seguro:** Saídas com mais de 64 KB são truncadas deterministicamente para os primeiros 16 KB com aviso claro no rodapé, impedindo o estouro da janela de contexto.

### Pilar 2: Auto-Remediação Pedagógica em Runtime
Em vez de apenas rejeitar o comando ou executá-lo de forma ineficiente, o FastExec intercepta maus hábitos da LLM, resolve a operação da forma correta e devolve um aviso instrutivo (*nudge*):
* **Auto-Remediação de `cat` / `type` / `Get-Content`:** Execuções isoladas de leitura são interceptadas no ar e convertidas para a micro-tool nativa `read_file` (zero subprocessos, sem criação de shell, com suporte a paginação). O retorno inclui:
  `[FastExec Auto-Remediation] O comando 'cat ...' foi convertido automaticamente para a tool nativa 'read_file' (zero-process). Na próxima, invoque read_file diretamente.`
* **Auto-Remediação de `sleep` / `timeout`:** Intercepta comandos de pausa artificial e os executa no timer nativo em Go, avisando o modelo:
  `[FastExec] Pausa de Ns concluída. (Dica: para sincronização de serviços, prefira checar a porta/log do alvo).`
* **Hints Contextuais de SQL e Shell:** Erros comuns como `aggregate functions are not allowed in GROUP BY` ou cmdlets PowerShell rodando em `cmd.exe` recebem orientações amigáveis no rodapé, permitindo à LLM auto-corrigir o plano sem abortar a tarefa.

### Pilar 3: Governança de Processos & Proteção Contra Timeouts
* **Windows Job Objects (`KILL_ON_JOB_CLOSE`):** Cada comando no Windows roda amarrado a um Job Object de kernel. No timeout ou no término da chamada, a árvore inteira (filhos, netos, ferramentas auxiliares) é eliminada atomicamente. Zero processos órfãos.
* **Teto Rígido de 45s (com Margem de Segurança):** O `timeout_ms` padrão é 45 s (teto 50 s). Se o comando estourar o tempo, o FastExec interrompe a árvore de processos aos 45s e devolve a saída parcial capturada até ali com a instrução de usar a tool `job`. Isso garante 15 segundos de folga para o limite de 60s do Claude Desktop, evitando que a conversa morra.
* **Ambiente Headless Canônico:** Injeta na origem `CI=1`, `DEBIAN_FRONTEND=noninteractive`, `PAGER=cat`, `GIT_PAGER=cat`, `GIT_TERMINAL_PROMPT=0`, `TERM=dumb`, `NO_COLOR=1`, `POWERSHELL_UPDATECHECK=Off`, etc. Nenhuma ferramenta abre paginadores interativos (`less`/`more`) nem trava aguardando `[y/n]`.

### Pilar 4: Transporte Multihost de Baixa Latência (~1 ms a 4 ms)
* **Local (PC-Elvis / Windows):** Daemon residente em `127.0.0.1:9146`. Elimina o overhead de inicialização do executável em chamadas repetidas e suporta múltiplas LLMs (Claude, Antigravity, OpenClaw) simultaneamente sem piscar janelas na tela.
* **Remoto (NoteServer, Roteadores AX6000, AX3000):** Comunica-se via socket TCP direto com daemons ultraleves compilados em **Zig** (`server-exec` na porta `:9142`), alcançando latências de ~2ms a 4ms, sem a lentidão e overhead do SSH tradicional.
* **Dispositivos Dedicados (Firesticks, TVs, Android):** Ponte nativa via daemon Zig e ADB persistente.

### Pilar 5: Padronização de Shell & Encoding Moderno
* **Preferência Automática por `pwsh` (PowerShell 7):** O FastExec resolve o caminho do PowerShell 7 mais estável, eliminando o clássico bug de arquivos gravados em UTF-16LE com BOM do PowerShell 5.1.
* **Fallback Transparente:** Se o `pwsh` não estiver instalado, faz fallback gracioso para o `powershell.exe` (5.1) sem quebrar chamadas legadas.
* **Expansão de Til (`~`):** Expande caminhos relativos ao diretório home do usuário tanto no Linux (`/home/elvis`) quanto no Windows (`C:\Users\ElvisSouza`), eliminando erros de `__FILE_NOT_FOUND__`.

---

## 3. Catálogo de Ferramentas

### As 6 Core Tools (Sempre Ativas)

| Ferramenta | Parâmetros Principais | Propósito |
|---|---|---|
| **`exec`** | `host`, `command` ou `argv`, `cwd`, `input`, `timeout_ms` | Execução síncrona de comandos (máx 45s). Suporta modo `argv` + `input` para envio de scripts longos por stdin sem criar arquivos temporários nem escapar aspas. |
| **`read_file`** | `host`, `path`, `offset`, `limit`, `start_line`, `end_line` | Leitura determinística em memória (local ou remota) sem spawn de processos. Suporta paginação por bytes ou linhas. |
| **`write_file`** | `host`, `path`, `content`, `atomic` | Escrita atômica ou direta de arquivos. Retorna apenas confirmação de tamanho (`ok N bytes`), poupando tokens. |
| **`edit_file`** | `host`, `path`, `target`, `replacement` | Edição cirúrgica em arquivo existente. Exige correspondência única do bloco `target`, evitando reescritas de arquivos inteiros. |
| **`file_ops`** | `host`, `action` (`list`, `copy`, `move`, `delete`, `mkdir`), `path`, `target` | Operações estruturadas no sistema de arquivos em formato JSON puro, sem invocar comandos de shell como `rm`, `mkdir` ou `dir`. |
| **`job`** | `host`, `action` (`start`, `status`, `tail`, `wait`, `stop`, `list`), `command`, `job_id` | Gerenciamento de processos assíncronos de longa duração (builds, daemons, watchers) via systemd-run (Linux) ou Named Job Objects (Windows). |

### Módulos Especializados Sob Demanda (Tool RAG via `tool_manage`)
Para tarefas específicas, o agente ativa ferramentas adicionais sem onerar o prompt inicial:
* **`sql`**: Execução analítica de DuckDB e SQLite com extração em linha única e prevenção de dumps tabulares.
* **`homelab`**: Ferramentas de infraestrutura de rede e transferência binária: `ping`, `get_file`, `put_file`.
* **`docker`**: Comandos estruturados de inspeção de containers: `docker_ps`, `docker_logs`.

---

## 4. Estrutura do Repositório

```
fastexec/
├── bin/                    # Binários compilados (fastexec.exe)
├── cmd/
│   └── fastexec/           # Ponto de entrada CLI e Daemon (main.go, attach_windows.go)
├── internal/
│   ├── analytics/          # Detecção de sensibilidade e regras de sandbox
│   ├── daemon/             # Listener TCP residente local (127.0.0.1:9146)
│   ├── env/                # Montagem e unificação de variáveis de ambiente Windows
│   ├── logger/             # Sistema de logs com rotação e níveis
│   ├── mcp/                # Protocolo JSON-RPC 2.0 e registro de ferramentas MCP
│   ├── telemetry/          # Auditoria e métricas SQLite WAL (bytes_in, bytes_out, tokens)
│   ├── tools/              # Implementação das Core Tools (exec, files, job, sql, etc.)
│   │   ├── exec.go         # Validação, guards e auto-remediação de comandos
│   │   ├── outclean.go     # Esteira Content-Aware de limpeza e minificação
│   │   └── ...
│   └── transport/          # Camada de despacho multihost (local, TCP Zig, SSH)
│       ├── local.go        # Execução Win32 com Job Objects e ambiente limpo
│       ├── pwsh_windows.go # Resolução robusta de PowerShell 7 vs 5.1
│       └── ...
├── _docs/                  # Documentação técnica e runbooks de arquitetura
├── AGENTS.md               # Contexto para agentes autônomos
├── CLAUDE.md               # Instruções específicas para Claude Desktop
└── README.md               # Este documento
```

---

## 5. Guia Rápido de Desenvolvimento

### Pré-requisitos
* Go 1.22+
* PowerShell 7 (`pwsh`)
* Node.js (necessário para a suíte de testes de árvore de processos)

### Comandos Essenciais

```powershell
# Executar todos os testes unitários
go test ./internal/transport/... ./internal/tools/...

# Compilar novo binário de produção (modo GUI sem console flicker)
go build -ldflags="-H=windowsgui -s -w" -o bin/fastexec.exe ./cmd/fastexec

# Verificar status do daemon local
go run ./cmd/fastexec daemon-status

# Parar ou reiniciar o daemon
go run ./cmd/fastexec daemon-stop
Start-Process "bin/fastexec.exe" -ArgumentList "daemon","run" -WindowStyle Hidden

# Consultar telemetria de tokens e auditoria (último 1 dia)
bin/fastexec stats 1d
```

---

## 6. Resumo Executivo para LLMs e Agentes

> Ao usar ou avaliar o FastExec, tenha em mente:
> 1. **Não é só shell:** O FastExec é seu ambiente de execução otimizado. Ele foi desenhado para economizar seus tokens, proteger sua sessão de timeouts de 60s e evitar que comandos travem o chat.
> 2. **Prefira ferramentas especializadas:** Se quiser ler um arquivo, use `read_file` (não use `cat`). Se quiser editar, use `edit_file`. Se a tarefa for longa (>30s), dispare em background com `job`.
> 3. **Segurança de Timeout:** O FastExec monitora a execução e corta processos aos 45s para que o Claude Desktop nunca corte você aos 60s. Confie no retorno e respeite as orientações nos rodapés.
