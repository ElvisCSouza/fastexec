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
pwsh -NoProfile -File .\Run-Bench.ps1 -DryRun     # valida as 30 tarefas e os oráculos, sem gastar nada
```

`Initialize-Bench.ps1` **congela** uma cópia do `fastexec.exe` pelo SHA256: um deploy no meio da rodada não troca o binário
medido. Para testar outro build: `.\Initialize-Bench.ps1 -FastExec C:\caminho\fastexec.exe`.

## Rodar

```powershell
# fumaça barata (≈ US$ 0,13): confere que tudo mede
pwsh -NoProfile -Command "& .\Run-Bench.ps1 -Tasks T0,T2 -Reps 1 -Tag smoke -MaxRunUsd 0.3 -MaxTotalUsd 0.6"

# rodada completa: 30 tarefas x 2 braços x 5 repetições
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
| X1–X2 | saída | saída gigante com erro no fim (20 mil linhas); falha transitória que só passa na 3ª tentativa |
| R1–R4 | código | contagem, chamadores (`stripFlags`), localizar função em arquivo grande, tarefa encadeada, tudo no `cobra` v1.8.1 |
| G1–G3 | grafo | chamadores no `hugo` v0.139.5 (gabarito por `go/ast`, em `tools\callers`) |
| M1–M2 | edição-multi | Go: renomear função em 8 arquivos; passar `context.Context` por 6 chamadas em 4 arquivos (compila e testa) |
| M3–M4 | edição-multi | Python: parâmetro keyword-only e usos marcados; JS: `var`→`const`/`let` e `require`→`import` |
| M5–M6 | edição-multi | 12 arquivos CRLF trocando 3 constantes byte a byte; `API_TIMEOUT` em JSON/YAML/.env preservando formato |
| C1–C2 | contexto | **code2test**: quais testes alcançam uma função exportada (direto ou 1 nível indireto) |
| C3 | contexto | **trace2code**: dada a pilha de um panic dentro do `cobra`, o arquivo:linha onde ele nasceu |
| C4–C5 | contexto | **edit2ripple**: arquivos não-teste que precisam mudar junto com a assinatura de uma função |
| C6 | contexto | **comment2context**: símbolos exportados do próprio arquivo citados num comentário `TODO`/`Deprecated` |

Cada tarefa tem **oráculo objetivo** calculado na hora por programa independente das duas ferramentas, e o `-DryRun` prova que
o oráculo aceita o gabarito e rejeita resposta errada. Adicionar tarefa: crie uma entrada `$TaskTable['Xn']` em `tasks\*.ps1`
(`Prepare`, `Gold`, `Score`).

A suíte **`edicao-multi`** (`tasks\edicao.ps1`, tarefas M1–M6) é auto-contida: cada `Prepare` gera um projeto sintético num
diretório temporário, sem rede. Onde o host tem a ferramenta da linguagem, o oráculo a executa de verdade (`go build`/`go
test`/`go vet`, `node --check`, `pytest`); onde não tem, cai para o oráculo estrutural e avisa `[edicao-multi] … oráculo só
estrutural`. Detalhes em [`docs/bench/spec-edicao.md`](../docs/bench/spec-edicao.md).

A suíte **`contexto`** (`tasks\contexto.ps1`, tarefas C1–C6) é só leitura e cobre os quatro tipos de recuperação de contexto do
Agent Retrieval Bench (code2test, trace2code, edit2ripple, comment2context) nos fixtures `cobra` e `hugo` de ref fixa. O
gabarito sai de programa independente em `tools\contexto` (`go/ast`, sem heurística textual) e o alvo de cada tarefa é
escolhido por regra determinista documentada no topo do arquivo, então o gabarito é estável entre execuções. A resposta é
sempre uma lista (um item por linha) pontuada por conjunto (precisão e recall; `correct` exige os dois em 1,0). Detalhes em
[`docs/bench/spec-contexto.md`](../docs/bench/spec-contexto.md).

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
