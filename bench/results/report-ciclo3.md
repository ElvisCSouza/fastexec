# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `1ee7db7ef50f` | 2.1.322 (Claude Code)
- Execuções: 78 (78 válidas), pares válidos: 39. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,809 (-19,1%)**, IC95% [0,685; 0,967], n = 39 pares.
Só pares em que **os dois acertaram**: 0,810 (-19,0%), n = 35. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C3 | contexto | 3 | 37.040 | 13.724 | -62,9% | 8,0→4,0 | 4,3→2,1 | 3/3 vs 3/3 | 0,0→0,0 |
| C4 | contexto | 3 | 13.165 | 22.298 | +69,4% | 2,0→5,0 | 7,3→7,8 | 2/3 vs 3/3 | 0,0→0,0 |
| M1 | edicao-multi | 3 | 38.983 | 28.692 | -26,4% | 13,0→6,0 | 8,3→4,1 | 0/3 vs 0/3 | 0,0→0,0 |
| M2 | edicao-multi | 3 | 43.592 | 38.766 | -11,1% | 18,0→17,0 | 5,8→5,7 | 3/3 vs 3/3 | 0,0→1,0 |
| M3 | edicao-multi | 3 | 24.900 | 32.839 | +31,9% | 12,0→12,0 | 2,5→4,1 | 3/3 vs 3/3 | 0,0→1,0 |
| M4 | edicao-multi | 3 | 49.969 | 39.956 | -20,0% | 15,0→15,0 | 3,1→2,8 | 3/3 vs 3/3 | 0,0→0,0 |
| M5 | edicao-multi | 3 | 66.030 | 56.375 | -14,6% | 12,0→15,0 | 5,5→5,4 | 3/3 vs 3/3 | 1,0→0,0 |
| M6 | edicao-multi | 3 | 22.906 | 18.277 | -20,2% | 8,0→10,0 | 0,9→1,1 | 3/3 vs 3/3 | 0,0→0,0 |
| P3 | mcp | 3 | 10.444 | 7.764 | -25,7% | 2,0→2,0 | 0,3→0,2 | 3/3 vs 3/3 | 0,0→0,0 |
| P4 | mcp | 3 | 76.691 | 38.701 | -49,5% | 11,0→8,0 | 1,5→1,4 | 3/3 vs 3/3 | 0,0→1,0 |
| W5 | terminal | 3 | 28.985 | 12.550 | -56,7% | 7,0→5,0 | 0,6→0,0 | 3/3 vs 3/3 | 1,0→0,0 |
| W6 | terminal | 3 | 22.662 | 19.461 | -14,1% | 4,0→5,0 | 0,6→1,7 | 3/3 vs 3/3 | 0,0→0,0 |
| W7 | terminal | 3 | 39.565 | 26.873 | -32,1% | 10,0→14,0 | 2,7→2,7 | 3/3 vs 3/3 | 0,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 6 | 0,920 | -8,0% |
| edicao-multi | 18 | 0,899 | -10,1% |
| mcp | 6 | 0,705 | -29,5% |
| terminal | 9 | 0,659 | -34,1% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 7,0, FastExec 7,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 7,10 | 6,80 | 0,96 | -4,2% |
| Tempo no modelo (s) | 4,10 | 4,20 | 1,02 | +2,4% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,95 | 1,41 | 0,72 | -27,7% |
| RAM de pico (MB) | 490,00 | 531,00 | 1,08 | +8,4% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,72 | -27,5% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 39 | 39 | 35 (90%) | 0,18 |
| fastexec | 39 | 39 | 36 (92%) | 0,51 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C4 | fastexec | 3 | 1,3 | 0,3 | 0,0 | 4,7 | 22.298 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,3 | 2,3 | 13.165 | 2/3 |
| M1 | fastexec | 3 | 0,3 | 0,7 | 0,0 | 7,0 | 28.692 | 0/3 |
| M1 | native | 3 | 0,0 | 0,0 | 1,7 | 14,3 | 38.983 | 0/3 |
| M2 | fastexec | 3 | 1,0 | 0,7 | 0,0 | 15,3 | 38.766 | 3/3 |
| M2 | native | 3 | 0,0 | 0,0 | 0,7 | 16,0 | 43.592 | 3/3 |
| M3 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 12,7 | 32.839 | 3/3 |
| M3 | native | 3 | 0,0 | 0,0 | 0,3 | 12,7 | 24.900 | 3/3 |
| M4 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 15,3 | 39.956 | 3/3 |
| M4 | native | 3 | 0,0 | 0,0 | 0,3 | 16,3 | 49.969 | 3/3 |
| M5 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 12,7 | 56.375 | 3/3 |
| M5 | native | 3 | 0,0 | 0,0 | 0,3 | 14,0 | 66.030 | 3/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

