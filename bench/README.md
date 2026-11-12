# Benchmark: Claude nativo vs Claude com FastExec

Mede, com **o mesmo modelo e as mesmas tarefas**, quanto o agente gasta usando só as ferramentas nativas do Claude Code
contra usando só o FastExec. O foco é **economia de tokens**; latência, CPU e RAM são extras.

| Braço | Ferramentas | Observação |
|---|---|---|
| `native` | Bash, Read, Glob, Grep, Edit, Write | o equivalente nativo do que o FastExec oferece |
| `fastexec` | só o MCP do FastExec | daemon isolado (porta 9246, dados próprios): não toca na sua telemetria real |

O protocolo e as lições da rodada anterior estão em [`docs/bench/PROTOCOL.md`](../docs/bench/PROTOCOL.md). Este ambiente
o substitui na prática: é versionado, usa só dados sintéticos e um repositório público, e roda sem caminhos fixos.

## Primeira vez

```powershell
cd bench
pwsh -NoProfile -File .\Initialize-Bench.ps1      # compila o medidor, baixa o cobra (ref fixa), congela o FastExec
pwsh -NoProfile -File .\Run-Bench.ps1 -DryRun     # valida as 13 tarefas e os oráculos, sem gastar nada
```

`Initialize-Bench.ps1` **congela** uma cópia do `fastexec.exe` pelo SHA256: um deploy no meio da rodada não troca o binário
medido. Para testar outro build: `.\Initialize-Bench.ps1 -FastExec C:\caminho\fastexec.exe`.

## Rodar

```powershell
# fumaça barata (≈ US$ 0,13): confere que tudo mede
pwsh -NoProfile -Command "& .\Run-Bench.ps1 -Tasks T0,T2 -Reps 1 -Tag smoke -MaxRunUsd 0.3 -MaxTotalUsd 0.6"

# rodada completa: 13 tarefas x 2 braços x 5 repetições
pwsh -NoProfile -Command "& .\Run-Bench.ps1 -Reps 5 -Tag full1"

pwsh -NoProfile -File .\Report.ps1 -Tag full1     # relatório em results\report-full1.md
```

Vários valores em `-Tasks` exigem `-Command "& ..."` (com `-File` a lista não é separada). A rodada é **retomável**: repetir o
mesmo `-Tag` pula o que já foi medido. Tetos de gasto: `-MaxRunUsd` por execução e `-MaxTotalUsd` por rodada (padrões em
`bench.config.json`). Use `-Cache` para medir com cache de prompt ligado (o padrão é desligado, para os tokens serem comparáveis).

As execuções usam a sua conta do Claude Code e contam no seu uso. Tarefas e braços rodam em ordem aleatória.

## Tarefas

| Id | Estrato | O que mede |
|---|---|---|
| T0 | baseline | só responde "ok": o **imposto fixo por turno** (system prompt + schemas) de cada braço |
| T2 | dados | achar um pedido num JSON de uma linha só (700 pedidos) |
| T8 | dados | log de 30 mil linhas: mensagem mais frequente, contagem e primeira ocorrência |
| T9 | edição | trocar uma constante num arquivo CRLF de 400 linhas sem alterar o resto |
| T10 | processo | comando de ~65 s (timeout, espera, retry) |
| E1–E4 | falhas | aspas, JSON em PowerShell, Windows-1252, controle sem armadilha |
| R1–R4 | código | contagem, chamadores (`stripFlags`), localizar função em arquivo grande, tarefa encadeada, tudo no `cobra` v1.8.1 |

Cada tarefa tem **oráculo objetivo** calculado na hora por programa independente das duas ferramentas, e o `-DryRun` prova que
o oráculo aceita o gabarito e rejeita resposta errada. Adicionar tarefa: crie uma entrada `$TaskTable['Xn']` em `tasks\*.ps1`
(`Prepare`, `Gold`, `Score`).

## O que é medido por execução

| Dimensão | Campos |
|---|---|
| **Economia (foco)** | `tokens_total` (entrada + saída), `in_tokens`, `out_tokens`, `first_turn_in` (imposto fixo), `result_kb` (bytes que as ferramentas devolveram ao modelo), `turns`, `tool_calls`, `cost` |
| Latência | `wall_s`, `api_s` (tempo no modelo), `tool_s` (tempo fora do modelo = ferramentas) |
| CPU e RAM | `cpu_s`, `ram_mb` (pico) e `procs`, da **árvore inteira**: claude + proxy MCP + daemon do FastExec, via Job Object (`tools\runjob`) |
| Qualidade | `tool_errors`, `correct` (oráculo), `valid` |

Uma execução do braço `fastexec` só é **válida** se o FastExec foi de fato chamado. O relatório compara por **pares** (mesma tarefa
e repetição), mostra n e IC95% por reamostragem, separa o resultado **só dos pares em que os dois acertaram** e exclui a linha
de base da média de economia.

## Cuidados que o ambiente já toma

- **Contexto limpo**: a pasta de trabalho (`work_dir`, padrão `W:\Data\bench-v2`) fica fora do repo, e o preflight recusa rodar
  se achar um `CLAUDE.md` em algum ancestral. O T0 acusa contaminação: primeiro turno acima de 14 mil tokens gera aviso.
- **Sem telemetria real**: o daemon do braço `fastexec` usa dados próprios em `work_dir\fx-data`.
- **Índice do `code_graph` aquecido fora da medição** nas tarefas de código.
- **Sem dados corporativos**: tudo é sintético ou do `cobra` público. Resultados (`results\*.jsonl`) podem ser publicados.

## Como ler o resultado (armadilhas conhecidas)

- Menos tokens **não** significa menos custo ou tempo: reescrever o histórico invalida cache e pode alongar a trajetória.
  Por isso o relatório mostra turnos, bytes devolvidos, custo e tempo ao lado dos tokens.
- Com poucas repetições o IC95% é largo. 5 repetições por tarefa é o mínimo para tirar conclusão; o piloto de 1 não é conclusão.
- Média geral esconde regressão: olhe a tabela por tarefa e por estrato.
- Conferir o oráculo antes de culpar a ferramenta (já aconteceu: o gabarito do E3 estava errado).
