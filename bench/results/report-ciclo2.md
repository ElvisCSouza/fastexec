# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `4d34954b6d33` | 2.1.322 (Claude Code)
- Execuções: 108 (108 válidas), pares válidos: 54. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,732 (-26,8%)**, IC95% [0,639; 0,841], n = 54 pares.
Só pares em que **os dois acertaram**: 0,744 (-25,6%), n = 50. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C3 | contexto | 3 | 18.623 | 13.499 | -27,5% | 5,0→4,0 | 3,1→1,9 | 3/3 vs 3/3 | 0,0→0,0 |
| C4 | contexto | 3 | 22.450 | 13.904 | -38,1% | 4,0→3,0 | 8,1→3,2 | 3/3 vs 3/3 | 0,0→0,0 |
| M4 | edicao-multi | 3 | 42.263 | 79.104 | +87,2% | 14,0→19,0 | 2,9→5,0 | 3/3 vs 3/3 | 0,0→0,0 |
| M6 | edicao-multi | 3 | 17.237 | 22.900 | +32,9% | 7,0→9,0 | 0,8→1,4 | 3/3 vs 3/3 | 0,0→0,0 |
| P1 | mcp | 3 | 18.349 | 12.747 | -30,5% | 14,0→3,0 | 1,9→2,1 | 3/3 vs 3/3 | 0,0→0,0 |
| P2 | mcp | 3 | 28.418 | 21.384 | -24,8% | 6,0→5,0 | 0,6→0,3 | 3/3 vs 3/3 | 1,0→0,0 |
| P3 | mcp | 3 | 10.450 | 12.057 | +15,4% | 2,0→3,0 | 0,2→0,3 | 3/3 vs 2/3 | 0,0→0,0 |
| P4 | mcp | 3 | 84.277 | 43.146 | -48,8% | 12,0→8,0 | 1,9→1,1 | 3/3 vs 1/3 | 0,0→0,0 |
| P5 | mcp | 3 | 10.708 | 8.037 | -24,9% | 2,0→2,0 | 0,3→0,9 | 3/3 vs 3/3 | 0,0→0,0 |
| P6 | mcp | 3 | 41.105 | 19.840 | -51,7% | 7,0→5,0 | 0,7→1,0 | 3/3 vs 3/3 | 1,0→0,0 |
| W1 | terminal | 3 | 16.805 | 9.212 | -45,2% | 3,0→2,0 | 2,1→1,4 | 2/3 vs 3/3 | 0,0→0,0 |
| W2 | terminal | 3 | 24.084 | 18.232 | -24,3% | 5,0→4,0 | 1,9→2,1 | 3/3 vs 3/3 | 0,0→0,0 |
| W3 | terminal | 3 | 46.636 | 28.893 | -38,0% | 7,0→6,0 | 2,2→4,5 | 3/3 vs 3/3 | 0,0→0,0 |
| W4 | terminal | 3 | 28.643 | 14.345 | -49,9% | 4,0→3,0 | 8,7→3,1 | 3/3 vs 3/3 | 0,0→0,0 |
| W5 | terminal | 3 | 35.210 | 12.161 | -65,5% | 7,0→5,0 | 0,7→0,0 | 3/3 vs 3/3 | 1,0→0,0 |
| W6 | terminal | 3 | 17.735 | 17.832 | +0,5% | 5,0→4,0 | 1,1→1,5 | 3/3 vs 3/3 | 0,0→0,0 |
| W7 | terminal | 3 | 44.800 | 27.449 | -38,7% | 7,0→13,0 | 2,6→2,5 | 3/3 vs 3/3 | 1,0→1,0 |
| W8 | terminal | 3 | 53.388 | 28.745 | -46,2% | 8,0→6,0 | 1,4→1,1 | 3/3 vs 3/3 | 2,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 6 | 0,665 | -33,5% |
| edicao-multi | 6 | 1,340 | +34,0% |
| mcp | 18 | 0,695 | -30,5% |
| terminal | 24 | 0,671 | -32,9% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 5,0, FastExec 4,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 6,85 | 6,15 | 0,90 | -10,2% |
| Tempo no modelo (s) | 4,15 | 3,85 | 0,93 | -7,2% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,60 | 1,10 | 0,68 | -31,6% |
| RAM de pico (MB) | 471,00 | 519,50 | 1,10 | +10,3% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,68 | -31,8% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 54 | 54 | 53 (98%) | 0,37 |
| fastexec | 54 | 54 | 51 (94%) | 0,22 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C3 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 4,3 | 13.499 | 3/3 |
| C3 | native | 3 | 0,0 | 0,0 | 0,0 | 5,0 | 18.623 | 3/3 |
| C4 | fastexec | 3 | 2,0 | 0,0 | 0,0 | 3,7 | 13.904 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,7 | 3,7 | 22.450 | 3/3 |
| M4 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 20,0 | 79.104 | 3/3 |
| M4 | native | 3 | 0,0 | 0,0 | 0,3 | 14,0 | 42.263 | 3/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

