# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `cf02eefad81c` | 2.1.322 (Claude Code)
- Execuções: 225 (224 válidas), pares válidos: 75. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,813 (-18,7%)**, IC95% [0,721; 0,916], n = 70 pares.
Só pares em que **os dois acertaram**: 0,835 (-16,5%), n = 68. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.660 | 13.312 | -20,1% | 3,0→3,0 | 0,5→0,7 | 5/5 vs 5/5 | 0,0→0,0 |
| E2 | falhas | 5 | 32.188 | 10.532 | -67,3% | 5,0→2,0 | 0,6→0,1 | 3/5 vs 4/5 | 1,0→0,0 |
| E3 | falhas | 5 | 29.507 | 17.497 | -40,7% | 5,0→4,0 | 2,8→2,2 | 5/5 vs 5/5 | 2,0→1,0 |
| E4 | falhas | 5 | 10.258 | 7.690 | -25,0% | 2,0→3,0 | 0,1→0,4 | 5/5 vs 5/5 | 0,0→0,0 |
| R1 | codigo | 5 | 11.451 | 19.683 | +71,9% | 3,0→5,0 | 2,5→3,7 | 5/5 vs 5/5 | 0,0→0,0 |
| R2 | codigo | 5 | 16.474 | 11.740 | -28,7% | 3,0→3,0 | 2,5→0,7 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 17.131 | 11.668 | -31,9% | 3,0→3,0 | 3,7→0,4 | 5/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 15.905 | 28.470 | +79,0% | 3,0→6,0 | 0,3→2,4 | 5/5 vs 5/5 | 0,0→1,0 |
| T0 | baseline | 5 | 4.889 | 3.541 | -27,6% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 10.176 | 7.530 | -26,0% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 27.756 | 17.653 | -36,4% | 5,0→5,0 | 1,4→1,1 | 5/5 vs 5/5 | 1,0→1,0 |
| T8 | dados | 5 | 22.850 | 21.791 | -4,6% | 4,0→6,0 | 1,8→2,5 | 5/5 vs 5/5 | 0,0→0,0 |
| T9 | edicao | 5 | 25.595 | 22.362 | -12,6% | 3,0→3,0 | 18,7→17,5 | 5/5 vs 5/5 | 0,0→0,0 |
| X1 | saida | 5 | 18.571 | 12.079 | -35,0% | 3,0→3,0 | 5,1→0,3 | 5/5 vs 5/5 | 0,0→2,0 |
| X2 | saida | 5 | 27.564 | 22.346 | -18,9% | 5,0→6,0 | 0,4→0,6 | 5/5 vs 5/5 | 2,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 20 | 0,966 | -3,4% |
| dados | 10 | 0,843 | -15,7% |
| edicao | 5 | 0,729 | -27,1% |
| falhas | 20 | 0,672 | -32,8% |
| processo | 5 | 0,804 | -19,6% |
| saida | 10 | 0,862 | -13,8% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.886 tokens, FastExec 3.538 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 3,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 4,10 | 4,00 | 0,98 | -2,4% |
| Tempo no modelo (s) | 2,30 | 2,60 | 1,13 | +13,0% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,12 | 0,67 | 0,60 | -40,2% |
| RAM de pico (MB) | 427,00 | 484,00 | 1,13 | +13,3% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,98 | -1,5% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 75 | 75 | 73 (97%) | 0,33 |
| fastexec | 75 | 75 | 74 (99%) | 0,59 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| E1 | 5 | 16.660 | 22.352 | +34,2% | 0% | 5/5 |
| E2 | 4 | 38.032 | 19.596 | -48,5% | 0% | 3/4 |
| E3 | 5 | 29.507 | 37.181 | +26,0% | 0% | 5/5 |
| E4 | 5 | 10.258 | 13.822 | +34,7% | 0% | 5/5 |
| R1 | 5 | 11.451 | 14.710 | +28,5% | 11% | 5/5 |
| R2 | 5 | 16.474 | 21.640 | +31,4% | 46% | 5/5 |
| R3 | 5 | 17.131 | 28.201 | +64,6% | 0% | 5/5 |
| R4 | 5 | 15.905 | 21.159 | +33,0% | 0% | 5/5 |
| T0 | 5 | 4.889 | 6.651 | +36,0% | 0% | 5/5 |
| T10 | 5 | 10.176 | 13.699 | +34,6% | 0% | 5/5 |
| T2 | 5 | 27.756 | 38.405 | +38,4% | 0% | 5/5 |
| T8 | 5 | 22.850 | 29.607 | +29,6% | 0% | 5/5 |
| T9 | 5 | 25.595 | 31.082 | +21,4% | 17% | 5/5 |
| X1 | 5 | 18.571 | 24.304 | +30,9% | 0% | 5/5 |
| X2 | 5 | 27.564 | 36.340 | +31,8% | 0% | 5/5 |

**Média geométrica híbrido/nativo: 1,256 (+25,6%), n = 69 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| R1 | fastexec | 5 | 0,0 | 0,6 | 0,0 | 4,8 | 19.683 | 5/5 |
| R1 | hybrid | 5 | 0,0 | 0,2 | 1,0 | 2,8 | 14.710 | 5/5 |
| R1 | native | 5 | 0,0 | 0,0 | 1,2 | 3,2 | 11.451 | 5/5 |
| R2 | fastexec | 5 | 1,4 | 0,2 | 0,0 | 3,6 | 11.740 | 5/5 |
| R2 | hybrid | 5 | 0,8 | 0,2 | 1,0 | 3,6 | 21.640 | 5/5 |
| R2 | native | 5 | 0,0 | 0,0 | 1,2 | 3,2 | 16.474 | 5/5 |
| R3 | fastexec | 5 | 0,0 | 0,8 | 0,0 | 3,0 | 11.668 | 5/5 |
| R3 | hybrid | 5 | 0,0 | 0,0 | 0,6 | 3,6 | 28.201 | 5/5 |
| R3 | native | 5 | 0,0 | 0,0 | 1,0 | 3,4 | 17.131 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

