# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `a0827e7fcbe5` | 2.1.322 (Claude Code)
- Execuções: 225 (223 válidas), pares válidos: 74. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,787 (-21,3%)**, IC95% [0,709; 0,880], n = 69 pares.
Só pares em que **os dois acertaram**: 0,792 (-20,8%), n = 67. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.654 | 17.924 | +7,6% | 3,0→4,0 | 0,4→0,7 | 5/5 vs 5/5 | 0,0→1,0 |
| E2 | falhas | 5 | 16.228 | 8.715 | -46,3% | 2,0→2,0 | 0,2→0,0 | 3/5 vs 4/5 | 0,0→0,0 |
| E3 | falhas | 4 | 32.844 | 20.042 | -39,0% | 5,5→4,5 | 2,7→1,6 | 5/5 vs 4/5 | 1,5→1,0 |
| E4 | falhas | 5 | 15.392 | 21.649 | +40,7% | 3,0→5,0 | 0,1→0,7 | 5/5 vs 5/5 | 0,0→1,0 |
| R1 | codigo | 5 | 11.257 | 19.913 | +76,9% | 3,0→4,0 | 2,2→4,2 | 5/5 vs 5/5 | 0,0→0,0 |
| R2 | codigo | 5 | 16.650 | 12.450 | -25,2% | 3,0→4,0 | 2,6→1,6 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 17.306 | 11.703 | -32,4% | 3,0→3,0 | 3,0→2,3 | 5/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 15.946 | 17.414 | +9,2% | 3,0→4,0 | 0,3→1,4 | 5/5 vs 5/5 | 0,0→0,0 |
| T0 | baseline | 5 | 4.889 | 3.542 | -27,6% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 10.265 | 7.659 | -25,4% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 29.158 | 12.250 | -58,0% | 5,0→3,0 | 2,1→0,8 | 5/5 vs 5/5 | 1,0→1,0 |
| T8 | dados | 5 | 22.116 | 27.280 | +23,3% | 4,0→6,0 | 0,8→2,0 | 5/5 vs 5/5 | 0,0→1,0 |
| T9 | edicao | 5 | 25.808 | 32.076 | +24,3% | 3,0→4,0 | 18,7→17,7 | 5/5 vs 5/5 | 0,0→0,0 |
| X1 | saida | 5 | 19.129 | 11.741 | -38,6% | 3,0→3,0 | 5,1→0,2 | 5/5 vs 5/5 | 0,0→1,0 |
| X2 | saida | 5 | 27.131 | 21.963 | -19,0% | 5,0→6,0 | 0,4→0,6 | 5/5 vs 5/5 | 0,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 20 | 0,873 | -12,7% |
| dados | 10 | 0,690 | -31,0% |
| edicao | 5 | 0,939 | -6,1% |
| falhas | 19 | 0,810 | -19,0% |
| processo | 5 | 0,748 | -25,2% |
| saida | 10 | 0,650 | -35,0% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.886 tokens, FastExec 3.539 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 2,5.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 4,65 | 3,65 | 0,78 | -21,5% |
| Tempo no modelo (s) | 2,70 | 2,50 | 0,93 | -7,4% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,07 | 0,62 | 0,58 | -42,1% |
| RAM de pico (MB) | 436,00 | 464,00 | 1,06 | +6,4% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,75 | -25,3% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 75 | 75 | 73 (97%) | 0,23 |
| fastexec | 75 | 74 | 73 (97%) | 0,63 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| E1 | 5 | 16.654 | 21.880 | +31,4% | 0% | 5/5 |
| E2 | 5 | 16.228 | 16.780 | +3,4% | 14% | 5/5 |
| E3 | 4 | 36.060 | 40.420 | +12,1% | 0% | 4/4 |
| E4 | 5 | 15.392 | 13.684 | -11,1% | 0% | 5/5 |
| R1 | 5 | 11.257 | 14.768 | +31,2% | 0% | 5/5 |
| R2 | 5 | 16.650 | 22.032 | +32,3% | 31% | 5/5 |
| R3 | 5 | 17.306 | 29.775 | +72,1% | 0% | 5/5 |
| R4 | 5 | 15.946 | 21.152 | +32,6% | 0% | 5/5 |
| T0 | 5 | 4.889 | 6.651 | +36,0% | 0% | 5/5 |
| T10 | 5 | 10.265 | 13.750 | +34,0% | 0% | 5/5 |
| T2 | 5 | 29.158 | 40.399 | +38,6% | 0% | 5/5 |
| T8 | 5 | 22.116 | 39.896 | +80,4% | 0% | 5/5 |
| T9 | 5 | 25.808 | 43.469 | +68,4% | 8% | 5/5 |
| X1 | 5 | 19.129 | 24.081 | +25,9% | 0% | 5/5 |
| X2 | 5 | 27.131 | 35.950 | +32,5% | 0% | 5/5 |

**Média geométrica híbrido/nativo: 1,299 (+29,9%), n = 69 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| R1 | fastexec | 5 | 0,0 | 0,4 | 0,0 | 4,6 | 19.913 | 5/5 |
| R1 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 2,8 | 14.768 | 5/5 |
| R1 | native | 5 | 0,0 | 0,0 | 1,6 | 3,0 | 11.257 | 5/5 |
| R2 | fastexec | 5 | 1,0 | 0,4 | 0,0 | 3,6 | 12.450 | 5/5 |
| R2 | hybrid | 5 | 0,6 | 0,0 | 1,0 | 3,6 | 22.032 | 5/5 |
| R2 | native | 5 | 0,0 | 0,0 | 1,0 | 3,0 | 16.650 | 5/5 |
| R3 | fastexec | 5 | 0,0 | 0,6 | 0,0 | 2,8 | 11.703 | 5/5 |
| R3 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 3,6 | 29.775 | 5/5 |
| R3 | native | 5 | 0,0 | 0,0 | 0,8 | 3,4 | 17.306 | 5/5 |
| T10 | fastexec | 5 | 0,0 | 0,2 | 0,0 | 2,4 | 7.659 | 5/5 |
| T10 | hybrid | 5 | 0,0 | 0,0 | 0,0 | 2,0 | 13.750 | 5/5 |
| T10 | native | 5 | 0,0 | 0,0 | 0,0 | 2,2 | 10.265 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

