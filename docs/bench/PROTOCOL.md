# Benchmark FastExec vs. sem FastExec: protocolo

Versão revisada após a rodada completa de nov/2026 (substitui o pré-registro original).

## 1. Pergunta

Em tarefas reais, o agente que usa FastExec gasta menos tokens/custo, termina mais rápido, usa menos recursos e **erra menos** do que o agente com as ferramentas nativas? Falhas são métrica de primeira classe, não ruído.

Restrição de projeto: o FastExec nunca pode ser a causa de mais tokens. Correções têm de beneficiar uso real (nada de ajuste ao benchmark, nada de troca de custo por ganho).

## 2. Braços

| Braço | Ferramentas | Binário |
|---|---|---|
| A | nativas: Bash, Read, Glob, Grep (T9 acrescenta Edit, Write) | n/a |
| B0 | só FastExec via MCP (linha de base) | `fx-base.exe` (2286d79) |
| B | só FastExec via MCP (versão após micro-tools sob demanda) | `fx-new.exe` (2fec822) |
| B2 | só FastExec via MCP (working tree: encoding, homônimos, PowerShell inline) | `fx-new2.exe` (2a3ce81) |

Braços B usam daemon isolado (`FASTEXEC_DAEMON_ADDR=127.0.0.1:9246`, `FASTEXEC_DATA_DIR=W:\Data\bench\fx-data`). Binários compilados com `go build -overlay <private>/overlay/overlay.json -ldflags="-H=windowsgui -s -w"`.

## 3. Isolamento e controle

- Contexto limpo por execução (sessão nova, sem memória, sem CLAUDE.md).
- Modelo fixo `claude-sonnet-5-5`, **cache de prompt desligado**.
- Custo fixo por turno ≈ 7,2k tokens (system + schemas); cada turno extra ≈ +7–8k. Por isso o número de turnos domina o resultado.
- Tetos de gasto: `-MaxRunUsd` por execução e `-MaxTotalUsd` por rodada. Nunca rodar rodada paga sem ter certeza do que se mede; um cenário por vez, ciclo corrige → testa.
- Host de acesso remoto: SSH só habilitado temporariamente (via FastExec) e `ALLOW_SSH_HOMELAB` só para o braço A.

## 4. Cenários e oráculos

Cada cenário tem gabarito objetivo e pontuação automática (precisão P, recall R, ok).

| Id | Foco | Observação |
|---|---|---|
| T2, T5, T6 | busca/leitura em código e dados reais | fixtures locais |
| T7 | callers de método homônimo (code_graph) | oráculo por tipo declarante |
| T8 | log com consulta PowerShell inline com `\"` | falha de aspas via cmd.exe |
| T9 | edição de arquivos | A usa Edit/Write |
| T10 | tarefa de dados | |
| T11 | host remoto (noteserver) | n=1, inconclusivo |
| E1–E4 | falhas: aspas, shell, formatação, encoding | E3: Windows-1252, gabarito 40 |

Uma execução só é válida se (braços B) houve chamadas FastExec > 0.

## 5. Métricas

Economia: tokens totais e custo (US$). Desempenho: RAM e CPU do processo. Tempo: wall clock e nº de turnos. Erros: erros de ferramenta por execução, sucesso na primeira tentativa, acurácia do oráculo.

Relatar sempre por estrato (cenários de 2 turnos, código/dados, falhas) e com n explícito; média geral esconde regressões.

## 6. Resultados resumidos (rodada nov/2026)

- B vs B0: cerca de 7% menos tokens em cenários de 2 turnos.
- B vs A: 37–61% menos tokens em cenários de código/dados.
- A vencia só em T8 (14,9k vs 21,8k) e E3 (antes do fix).
- Após correções: E3 B2 = 14,1k tokens, 2 turnos, 0 erros; T7 B2 precisão 1,0; T8 B2 = 13,8k e 13,9k tokens, 2 turnos, 0 erros (≈ A).
- T11 (remoto): B uma volta de auto-verificação pior; n=1, variância do modelo.

## 7. Lições

1. Conferir o oráculo antes de culpar a ferramenta (E3 tinha gabarito errado: 40, não 2).
2. A telemetria real tem viés de sobrevivência: classes de erro que o usuário contornou deixam de aparecer; por isso existem os cenários E1–E4.
3. Instruções do servidor para chamadas paralelas não mudaram o comportamento do modelo e custavam tokens por turno; foram removidas.
4. Toda descrição de ferramenta custa tokens em todo turno; cada linha adicionada precisa de ganho medido.
5. Caminhos relativos em PowerShell/.NET resolvem para `C:\Windows\system32`; usar caminhos absolutos nos scripts do harness.

## 8. Como rodar

```powershell
pwsh -NoProfile -Command "& W:\Data\bench\bench-run.ps1 -Scenarios T8 -Reps 2 -Arms B2 -MaxRunUsd 0.5 -MaxTotalUsd 0.5 -Tag t8-b2"
```

Vários cenários exigem `-Command "& arquivo -Scenarios a,b"` (com `-File` o array não é separado). Resultados em `results2.jsonl`; transcrições em `runs\<tag>\<cenário>-<braço>-r<n>\stream.jsonl`. `-DryRun` valida sem gastar.

Dados dos fixtures contêm código corporativo: não commitar nem publicar.
