# FastExec — Diretrizes para Claude Desktop

## Identidade e Propósito
O FastExec **NÃO** é apenas uma ferramenta de shell remoto. Ele é o **Runtime de Execução, Engenharia de Contexto e Governança de Processos** de todo o HomeLab e das LLMs (Claude Desktop, Antigravity, OpenClaw, Workers).

Seus objetivos fundamentais:
1. **Economia Brutal de Tokens:** Minificação inteligente de saídas (`outclean`), JSON compactado, redução de espaços em branco em tabelas CLI e Tool RAG dinâmico para reduzir o system prompt em 45%.
2. **Prevenção de Timeouts de 60s:** Execuções síncronas são limitadas a 45s com encerramento atômico de processos (Windows Job Objects / systemd-run), garantindo que a sessão do chat nunca caia.
3. **Auto-Remediação e Nudges:** Interceptação automática de `cat`/`type` (convertidos para `read_file` em 0ms) e `sleep` (convertidos para timer Go com feedback pedagógico).
4. **Transporte de Ultrabaixa Latência (~1-4ms):** Daemon local Windows em `127.0.0.1:9146` e daemons Zig nativos nos hosts remotos via TCP `:9142`.

## Regras de Execução para Agentes
- **Leitura de arquivos:** Use **SEMPRE** a micro-tool `read_file` em vez de chamar `cat`, `type` ou scripts via `exec`.
- **Edição cirúrgica:** Use `edit_file` com blocos drop-in exatos (evita reescrever arquivos inteiros).
- **Operações de filesystem:** Use `file_ops` (`list`, `copy`, `move`, `delete`, `mkdir`) em vez de comandos de shell.
- **Tarefas longas (>30s):** Inicie com `job` (action: `start`). Não prenda a chamada síncrona esperando builds ou deploys.
- **PowerShell:** Use `argv: ["pwsh", "-NoProfile", "-Command", "-"]` com o script em `input` (stdin). O FastExec resolve o PowerShell 7 nativo em UTF-8 limpo sem BOM automaticamente.
- **Fonte única de regras:** `~/workspace/LLM.md` no `noteserver` (leia via `fastexec:read_file` no host `noteserver`).
