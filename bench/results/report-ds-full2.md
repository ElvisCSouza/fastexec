# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `41336f2ed137` | 2.1.322 (Claude Code)
- Execuções: 225 (223 válidas), pares válidos: 73. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 1,006 (+0,6%)**, IC95% [0,875; 1,173], n = 68 pares.
Só pares em que **os dois acertaram**: 1,000 (0,0%), n = 64. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.249 | 36.611 | +125,3% | 3,0→7,0 | 0,5→0,5 | 5/5 vs 5/5 | 0,0→3,0 |
| E2 | falhas | 5 | 13.190 | 22.347 | +69,4% | 2,0→4,0 | 0,3→0,1 | 2/5 vs 3/5 | 0,0→2,0 |
| E3 | falhas | 5 | 35.377 | 15.954 | -54,9% | 6,0→5,0 | 2,2→2,4 | 5/5 vs 5/5 | 1,0→1,0 |
| E4 | falhas | 5 | 10.169 | 24.692 | +142,8% | 2,0→5,0 | 0,1→1,0 | 5/5 vs 5/5 | 0,0→3,0 |
| R1 | codigo | 4 | 16.880 | 9.854 | -41,6% | 3,5→2,0 | 2,1→2,1 | 5/5 vs 4/5 | 0,0→0,0 |
| R2 | codigo | 5 | 16.590 | 14.371 | -13,4% | 3,0→4,0 | 2,5→1,1 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 17.063 | 13.889 | -18,6% | 3,0→3,0 | 4,5→0,6 | 5/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 15.896 | 21.293 | +34,0% | 3,0→5,0 | 0,2→1,6 | 5/5 vs 5/5 | 0,0→0,0 |
| T0 | baseline | 5 | 4.891 | 4.246 | -13,2% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 10.166 | 8.984 | -11,6% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 36.284 | 18.941 | -47,8% | 6,0→4,0 | 1,8→0,7 | 5/5 vs 5/5 | 1,0→1,0 |
| T8 | dados | 5 | 21.774 | 22.663 | +4,1% | 5,0→6,0 | 0,8→3,4 | 5/5 vs 5/5 | 0,0→1,0 |
| T9 | edicao | 4 | 25.730 | 29.720 | +15,5% | 3,0→3,5 | 18,7→17,8 | 5/5 vs 5/5 | 0,0→0,0 |
| X1 | saida | 5 | 18.669 | 9.148 | -51,0% | 3,0→3,0 | 3,5→0,2 | 5/5 vs 5/5 | 0,0→1,0 |
| X2 | saida | 5 | 21.347 | 24.715 | +15,8% | 4,0→6,0 | 0,4→0,4 | 5/5 vs 5/5 | 0,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 19 | 0,892 | -10,8% |
| dados | 10 | 0,748 | -25,2% |
| edicao | 4 | 1,214 | +21,4% |
| falhas | 20 | 1,344 | +34,4% |
| processo | 5 | 0,959 | -4,1% |
| saida | 10 | 0,902 | -9,8% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.888 tokens, FastExec 4.243 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 3,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 4,00 | 4,30 | 1,07 | +7,5% |
| Tempo no modelo (s) | 2,30 | 3,00 | 1,30 | +30,4% |
| Tempo fora do modelo: ferramentas (s) | 1,00 | 1,00 | 1,00 | 0,0% |
| CPU (s) | 1,00 | 0,67 | 0,67 | -33,0% |
| RAM de pico (MB) | 435,00 | 455,00 | 1,05 | +4,6% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 1,02 | +1,9% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 75 | 74 | 72 (96%) | 0,27 |
| fastexec | 75 | 74 | 72 (96%) | 1,12 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| E1 | 5 | 16.249 | 24.140 | +48,6% | 0% | 5/5 |
| E2 | 5 | 13.190 | 24.462 | +85,5% | 50% | 4/5 |
| E3 | 5 | 35.377 | 34.288 | -3,1% | 35% | 5/5 |
| E4 | 5 | 10.169 | 15.164 | +49,1% | 0% | 5/5 |
| R1 | 5 | 17.139 | 15.428 | -10,0% | 0% | 5/5 |
| R2 | 5 | 16.590 | 24.163 | +45,6% | 67% | 5/5 |
| R3 | 5 | 17.063 | 32.935 | +93,0% | 0% | 5/5 |
| R4 | 5 | 15.896 | 23.315 | +46,7% | 0% | 5/5 |
| T0 | 5 | 4.891 | 7.356 | +50,4% | 0% | 5/5 |
| T10 | 5 | 10.166 | 15.121 | +48,7% | 0% | 5/5 |
| T2 | 5 | 36.284 | 49.163 | +35,5% | 0% | 5/5 |
| T8 | 5 | 21.774 | 32.584 | +49,6% | 0% | 5/5 |
| T9 | 4 | 25.730 | 33.058 | +28,5% | 18% | 5/5 |
| X1 | 5 | 18.669 | 25.972 | +39,1% | 17% | 5/5 |
| X2 | 5 | 21.347 | 31.805 | +49,0% | 0% | 5/5 |

**Média geométrica híbrido/nativo: 1,403 (+40,3%), n = 69 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| R1 | fastexec | 4 | 0,0 | 1,0 | 0,0 | 2,5 | 9.854 | 4/4 |
| R1 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 2,6 | 15.428 | 5/5 |
| R1 | native | 5 | 0,0 | 0,0 | 1,2 | 3,4 | 17.139 | 5/5 |
| R2 | fastexec | 5 | 1,2 | 0,6 | 0,0 | 4,0 | 14.371 | 5/5 |
| R2 | hybrid | 5 | 1,0 | 0,2 | 0,6 | 3,4 | 24.163 | 5/5 |
| R2 | native | 5 | 0,0 | 0,0 | 1,2 | 3,0 | 16.590 | 5/5 |
| R3 | fastexec | 5 | 0,2 | 0,6 | 0,0 | 3,2 | 13.889 | 5/5 |
| R3 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 4,2 | 32.935 | 5/5 |
| R3 | native | 5 | 0,0 | 0,0 | 1,0 | 3,2 | 17.063 | 5/5 |
| R4 | fastexec | 5 | 0,0 | 0,2 | 0,0 | 4,8 | 21.293 | 5/5 |
| R4 | hybrid | 5 | 0,0 | 0,0 | 0,0 | 3,0 | 23.315 | 5/5 |
| R4 | native | 5 | 0,0 | 0,0 | 0,0 | 3,2 | 15.896 | 5/5 |
| X2 | fastexec | 5 | 0,0 | 0,2 | 0,0 | 5,6 | 24.715 | 5/5 |
| X2 | hybrid | 5 | 0,0 | 0,0 | 0,0 | 4,4 | 31.805 | 5/5 |
| X2 | native | 5 | 0,0 | 0,0 | 0,0 | 4,2 | 21.347 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

