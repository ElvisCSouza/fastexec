# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `572e464f2ada` | 2.1.322 (Claude Code)
- Execuções: 398 (370 válidas), pares válidos: 180. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,725 (-27,5%)**, IC95% [0,673; 0,782], n = 177 pares.
Só pares em que **os dois acertaram**: 0,721 (-27,9%), n = 159. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C1 | contexto | 4 | 43.080 | 29.306 | -32,0% | 6,5→7,5 | 19,8→6,2 | 4/4 vs 3/4 | 0,0→0,0 |
| C3 | contexto | 3 | 17.332 | 13.137 | -24,2% | 3,0→3,0 | 1,8→1,9 | 3/3 vs 3/3 | 0,0→0,0 |
| C4 | contexto | 3 | 13.200 | 22.651 | +71,6% | 2,0→5,0 | 7,3→7,6 | 3/3 vs 3/3 | 0,0→0,0 |
| C6 | contexto | 3 | 18.988 | 17.540 | -7,6% | 4,0→4,0 | 6,5→7,0 | 0/3 vs 1/3 | 0,0→0,0 |
| CS1 | csharp | 3 | 10.858 | 13.151 | +21,1% | 2,0→4,0 | 1,0→1,8 | 3/3 vs 3/3 | 0,0→0,0 |
| CS2 | csharp | 3 | 34.362 | 31.316 | -8,9% | 6,0→5,0 | 11,3→9,9 | 3/3 vs 3/3 | 0,0→0,0 |
| CS3 | csharp | 3 | 117.698 | 84.251 | -28,4% | 14,0→13,0 | 16,1→32,6 | 3/3 vs 3/3 | 0,0→0,0 |
| CS4 | csharp | 3 | 112.249 | 100.976 | -10,0% | 14,0→12,0 | 14,1→26,6 | 3/3 vs 3/3 | 0,0→0,0 |
| CS5 | csharp | 3 | 18.845 | 12.913 | -31,5% | 4,0→4,0 | 4,9→2,0 | 3/3 vs 3/3 | 0,0→0,0 |
| CS6 | csharp | 2 | 53.116 | 150.738 | +183,8% | 8,5→18,0 | 8,8→19,9 | 3/3 vs 3/3 | 0,0→2,5 |
| CS7 | csharp | 3 | 32.523 | 26.534 | -18,4% | 6,0→6,0 | 6,4→10,8 | 3/3 vs 3/3 | 0,0→0,0 |
| CS8 | csharp | 1 | 175.270 | 66.254 | -62,2% | 19,0→11,0 | 12,7→16,5 | 3/3 vs 3/3 | 1,0→0,0 |
| D1 | docs | 3 | 26.598 | 14.662 | -44,9% | 6,0→7,0 | 6,5→5,7 | 3/3 vs 3/3 | 0,0→0,0 |
| D2 | docs | 3 | 37.309 | 28.093 | -24,7% | 9,0→7,0 | 8,8→6,7 | 3/3 vs 3/3 | 0,0→0,0 |
| D3 | docs | 3 | 10.212 | 7.593 | -25,6% | 2,0→2,0 | 0,1→0,3 | 3/3 vs 3/3 | 0,0→0,0 |
| D4 | docs | 3 | 39.791 | 22.240 | -44,1% | 7,0→8,0 | 13,3→9,2 | 3/3 vs 3/3 | 0,0→0,0 |
| D5 | docs | 3 | 36.432 | 22.738 | -37,6% | 7,0→8,0 | 7,5→9,3 | 3/3 vs 3/3 | 0,0→0,0 |
| D6 | docs | 3 | 30.789 | 13.631 | -55,7% | 6,0→5,0 | 4,4→3,6 | 1/3 vs 3/3 | 0,0→0,0 |
| E1 | falhas | 3 | 17.007 | 17.532 | +3,1% | 3,0→4,0 | 0,5→1,0 | 3/3 vs 3/3 | 0,0→0,0 |
| E2 | falhas | 3 | 15.750 | 9.813 | -37,7% | 2,0→2,0 | 0,1→0,0 | 1/3 vs 3/3 | 0,0→0,0 |
| E3 | falhas | 3 | 30.308 | 17.839 | -41,1% | 5,0→4,0 | 3,6→1,0 | 3/3 vs 3/3 | 1,0→1,0 |
| E4 | falhas | 3 | 10.168 | 7.693 | -24,3% | 2,0→2,0 | 0,1→0,6 | 3/3 vs 3/3 | 0,0→0,0 |
| G1 | grafo | 3 | 41.468 | 12.839 | -69,0% | 6,0→4,0 | 16,3→1,5 | 3/3 vs 3/3 | 0,0→0,0 |
| G2 | grafo | 3 | 67.849 | 38.938 | -42,6% | 7,0→8,0 | 22,3→8,4 | 3/3 vs 3/3 | 0,0→0,0 |
| G3 | grafo | 3 | 30.509 | 8.738 | -71,4% | 4,0→3,0 | 6,4→1,4 | 3/3 vs 3/3 | 0,0→0,0 |
| M1 | edicao-multi | 3 | 41.121 | 37.005 | -10,0% | 21,0→11,0 | 8,6→6,5 | 3/3 vs 3/3 | 0,0→0,0 |
| M2 | edicao-multi | 3 | 45.310 | 29.157 | -35,6% | 17,0→14,0 | 5,8→5,6 | 3/3 vs 3/3 | 0,0→0,0 |
| M3 | edicao-multi | 3 | 24.952 | 26.299 | +5,4% | 12,0→13,0 | 2,5→3,7 | 3/3 vs 3/3 | 0,0→0,0 |
| M4 | edicao-multi | 3 | 81.886 | 48.590 | -40,7% | 15,0→15,0 | 4,2→4,7 | 3/3 vs 3/3 | 0,0→0,0 |
| M5 | edicao-multi | 3 | 70.618 | 51.871 | -26,5% | 10,0→14,0 | 7,5→5,3 | 3/3 vs 3/3 | 1,0→1,0 |
| M6 | edicao-multi | 3 | 17.139 | 13.169 | -23,2% | 7,0→7,0 | 0,8→1,3 | 3/3 vs 3/3 | 0,0→0,0 |
| P1 | mcp | 3 | 17.649 | 18.542 | +5,1% | 4,0→5,0 | 2,1→2,1 | 3/3 vs 3/3 | 0,0→0,0 |
| P2 | mcp | 3 | 28.216 | 26.533 | -6,0% | 5,0→7,0 | 0,7→0,6 | 3/3 vs 3/3 | 0,0→0,0 |
| P3 | mcp | 3 | 10.474 | 7.804 | -25,5% | 2,0→2,0 | 0,3→0,2 | 3/3 vs 3/3 | 0,0→0,0 |
| P4 | mcp | 3 | 92.804 | 41.785 | -55,0% | 13,0→10,0 | 1,9→1,1 | 3/3 vs 2/3 | 1,0→2,0 |
| P5 | mcp | 3 | 10.694 | 8.072 | -24,5% | 2,0→2,0 | 0,5→0,6 | 3/3 vs 3/3 | 0,0→0,0 |
| P6 | mcp | 3 | 43.476 | 42.015 | -3,4% | 7,0→8,0 | 1,1→1,6 | 3/3 vs 3/3 | 2,0→1,0 |
| R1 | codigo | 3 | 11.381 | 9.291 | -18,4% | 3,0→3,0 | 2,2→2,1 | 3/3 vs 3/3 | 0,0→0,0 |
| R2 | codigo | 3 | 16.632 | 17.226 | +3,6% | 3,0→4,0 | 2,5→2,9 | 3/3 vs 3/3 | 0,0→0,0 |
| R3 | codigo | 3 | 16.688 | 8.224 | -50,7% | 3,0→3,0 | 3,7→2,0 | 3/3 vs 3/3 | 0,0→0,0 |
| R4 | codigo | 3 | 16.093 | 12.405 | -22,9% | 3,0→3,0 | 0,6→0,8 | 3/3 vs 3/3 | 0,0→0,0 |
| SL1 | swelite | 1 | 121.401 | 116.716 | -3,9% | 12,0→13,0 | 12,5→19,6 | 2/3 vs 3/3 | 0,0→0,0 |
| SL2 | swelite | 3 | 78.421 | 51.053 | -34,9% | 12,0→9,0 | 12,4→10,7 | 3/3 vs 3/3 | 1,0→0,0 |
| SL3 | swelite | 3 | 46.685 | 59.492 | +27,4% | 6,0→10,0 | 9,3→9,4 | 0/3 vs 0/3 | 0,0→0,0 |
| SL4 | swelite | 2 | 132.318 | 86.809 | -34,4% | 12,5→13,0 | 17,8→9,2 | 2/3 vs 3/3 | 0,0→1,0 |
| SL5 | swelite | 1 | 111.193 | 89.543 | -19,5% | 10,0→12,0 | 22,2→22,5 | 0/3 vs 0/3 | 0,0→1,0 |
| SL7 | swelite | 1 | 155.272 | 146.285 | -5,8% | 17,0→16,0 | 21,5→25,5 | 0/3 vs 0/3 | 1,0→0,0 |
| SL8 | swelite | 3 | 55.301 | 62.121 | +12,3% | 8,0→10,0 | 13,7→12,8 | 0/3 vs 0/3 | 0,0→0,0 |
| T0 | baseline | 3 | 4.887 | 3.562 | -27,1% | 1,0→1,0 | 0,0→0,0 | 3/3 vs 3/3 | 0,0→0,0 |
| T10 | processo | 3 | 10.353 | 7.503 | -27,5% | 2,0→2,0 | 0,0→0,0 | 3/3 vs 3/3 | 0,0→0,0 |
| T2 | dados | 3 | 40.838 | 12.057 | -70,5% | 7,0→3,0 | 1,7→1,1 | 3/3 vs 3/3 | 1,0→0,0 |
| T8 | dados | 3 | 28.397 | 17.338 | -38,9% | 5,0→5,0 | 0,7→3,0 | 3/3 vs 3/3 | 0,0→0,0 |
| T9 | edicao | 3 | 25.624 | 36.606 | +42,9% | 3,0→5,0 | 18,7→12,4 | 3/3 vs 3/3 | 0,0→0,0 |
| W1 | terminal | 3 | 12.176 | 8.388 | -31,1% | 2,0→2,0 | 1,1→1,2 | 3/3 vs 3/3 | 0,0→0,0 |
| W2 | terminal | 3 | 25.085 | 13.827 | -44,9% | 5,0→4,0 | 2,0→2,0 | 3/3 vs 3/3 | 0,0→0,0 |
| W3 | terminal | 3 | 38.077 | 30.344 | -20,3% | 6,0→6,0 | 3,1→4,9 | 3/3 vs 3/3 | 0,0→0,0 |
| W4 | terminal | 3 | 21.893 | 19.577 | -10,6% | 3,0→4,0 | 10,3→3,5 | 3/3 vs 3/3 | 0,0→0,0 |
| W5 | terminal | 3 | 29.554 | 12.229 | -58,6% | 7,0→5,0 | 0,6→0,0 | 3/3 vs 3/3 | 1,0→0,0 |
| W6 | terminal | 3 | 24.727 | 30.060 | +21,6% | 4,0→6,0 | 0,9→1,6 | 3/3 vs 3/3 | 0,0→1,0 |
| W7 | terminal | 3 | 40.282 | 26.910 | -33,2% | 7,0→13,0 | 2,4→2,8 | 3/3 vs 3/3 | 0,0→1,0 |
| W8 | terminal | 3 | 37.655 | 24.960 | -33,7% | 6,0→6,0 | 1,3→1,2 | 3/3 vs 2/3 | 1,0→1,0 |
| X1 | saida | 3 | 18.582 | 11.760 | -36,7% | 3,0→2,0 | 5,1→10,4 | 3/3 vs 3/3 | 0,0→1,0 |
| X2 | saida | 3 | 27.054 | 21.230 | -21,5% | 5,0→5,0 | 0,4→0,4 | 3/3 vs 3/3 | 0,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 12 | 0,740 | -26,0% |
| contexto | 13 | 0,860 | -14,0% |
| csharp | 21 | 0,893 | -10,7% |
| dados | 6 | 0,430 | -57,0% |
| docs | 18 | 0,609 | -39,1% |
| edicao | 3 | 0,944 | -5,6% |
| edicao-multi | 18 | 0,780 | -22,0% |
| falhas | 12 | 0,708 | -29,2% |
| grafo | 9 | 0,390 | -61,0% |
| mcp | 18 | 0,770 | -23,0% |
| processo | 3 | 0,733 | -26,7% |
| saida | 6 | 0,898 | -10,2% |
| swelite | 14 | 0,917 | -8,3% |
| terminal | 24 | 0,654 | -34,6% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.884 tokens, FastExec 3.559 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 5,0, FastExec 4,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 6,95 | 5,10 | 0,73 | -26,6% |
| Tempo no modelo (s) | 4,20 | 3,70 | 0,88 | -11,9% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,47 | 1,21 | 0,83 | -17,3% |
| RAM de pico (MB) | 461,00 | 520,00 | 1,13 | +12,8% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,68 | -32,0% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 199 | 183 | 169 (85%) | 0,20 |
| fastexec | 199 | 187 | 173 (87%) | 0,37 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C1 | fastexec | 4 | 2,0 | 0,8 | 0,0 | 7,2 | 29.306 | 3/4 |
| C1 | native | 4 | 0,0 | 0,0 | 1,0 | 6,2 | 43.080 | 4/4 |
| C4 | fastexec | 3 | 1,7 | 0,0 | 0,0 | 5,0 | 22.651 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,0 | 2,7 | 13.200 | 3/3 |
| C5 | fastexec | 3 | 4,3 | 0,3 | 0,0 | 17,0 | 137.848 | 0/3 |
| C6 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 3,7 | 17.540 | 1/3 |
| C6 | native | 3 | 0,0 | 0,0 | 1,7 | 3,7 | 18.988 | 0/3 |
| CS1 | fastexec | 3 | 0,3 | 0,7 | 0,0 | 4,0 | 13.151 | 3/3 |
| CS1 | native | 3 | 0,0 | 0,0 | 1,0 | 2,0 | 10.858 | 3/3 |
| CS3 | fastexec | 3 | 1,3 | 0,7 | 0,0 | 13,3 | 84.251 | 3/3 |
| CS3 | native | 3 | 0,0 | 0,0 | 1,0 | 14,3 | 117.698 | 3/3 |
| CS4 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 12,3 | 100.976 | 3/3 |
| CS4 | native | 3 | 0,0 | 0,0 | 1,7 | 13,7 | 112.249 | 3/3 |
| CS5 | fastexec | 3 | 0,3 | 0,7 | 0,0 | 4,0 | 12.913 | 3/3 |
| CS5 | native | 3 | 0,0 | 0,0 | 2,3 | 4,0 | 18.845 | 3/3 |
| CS6 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 15,3 | 140.307 | 3/3 |
| CS6 | native | 2 | 0,0 | 0,0 | 1,5 | 8,5 | 53.116 | 2/2 |
| CS7 | fastexec | 3 | 0,0 | 1,3 | 0,0 | 6,3 | 26.534 | 3/3 |
| CS7 | native | 3 | 0,0 | 0,0 | 2,3 | 6,3 | 32.523 | 3/3 |
| CS8 | fastexec | 2 | 0,5 | 1,5 | 0,0 | 13,5 | 98.550 | 2/2 |
| CS8 | native | 2 | 0,0 | 0,0 | 3,0 | 17,0 | 157.176 | 2/2 |
| D1 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 6,3 | 14.662 | 3/3 |
| D1 | native | 3 | 0,0 | 0,0 | 1,7 | 7,0 | 26.598 | 3/3 |
| D6 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 5,7 | 13.631 | 3/3 |
| D6 | native | 3 | 0,0 | 0,0 | 0,0 | 6,0 | 30.789 | 1/3 |
| G1 | fastexec | 3 | 1,0 | 3,7 | 0,0 | 8,3 | 12.839 | 3/3 |
| G1 | native | 3 | 0,0 | 0,0 | 1,7 | 6,0 | 41.468 | 3/3 |
| G2 | fastexec | 3 | 1,7 | 0,7 | 0,0 | 7,3 | 38.938 | 3/3 |
| G2 | native | 3 | 0,0 | 0,0 | 2,0 | 6,7 | 67.849 | 3/3 |
| G3 | fastexec | 3 | 1,0 | 0,7 | 0,0 | 2,7 | 8.738 | 3/3 |
| G3 | native | 3 | 0,0 | 0,0 | 1,3 | 4,7 | 30.509 | 3/3 |
| M1 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 10,7 | 37.005 | 3/3 |
| M1 | native | 3 | 0,0 | 0,0 | 1,7 | 16,7 | 41.121 | 3/3 |
| M2 | fastexec | 3 | 0,7 | 0,0 | 0,0 | 15,0 | 29.157 | 3/3 |
| M2 | native | 3 | 0,0 | 0,0 | 1,0 | 17,3 | 45.310 | 3/3 |
| M3 | fastexec | 3 | 0,7 | 0,3 | 0,0 | 12,3 | 26.299 | 3/3 |
| M3 | native | 3 | 0,0 | 0,0 | 0,0 | 11,3 | 24.952 | 3/3 |
| M4 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 17,3 | 48.590 | 3/3 |
| M4 | native | 3 | 0,0 | 0,0 | 0,0 | 16,3 | 81.886 | 3/3 |
| R1 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 3,0 | 9.291 | 3/3 |
| R1 | native | 3 | 0,0 | 0,0 | 1,3 | 2,7 | 11.381 | 3/3 |
| R2 | fastexec | 3 | 1,0 | 0,3 | 0,0 | 3,7 | 17.226 | 3/3 |
| R2 | native | 3 | 0,0 | 0,0 | 1,0 | 3,0 | 16.632 | 3/3 |
| R3 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 3,0 | 8.224 | 3/3 |
| R3 | native | 3 | 0,0 | 0,0 | 0,7 | 3,0 | 16.688 | 3/3 |
| SL2 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 9,0 | 51.053 | 3/3 |
| SL2 | native | 3 | 0,0 | 0,0 | 0,0 | 12,7 | 78.421 | 3/3 |
| SL3 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 9,7 | 59.492 | 0/3 |
| SL3 | native | 3 | 0,0 | 0,0 | 0,0 | 6,3 | 46.685 | 0/3 |
| SL4 | fastexec | 3 | 0,3 | 0,7 | 0,0 | 13,7 | 88.278 | 3/3 |
| SL4 | native | 2 | 0,0 | 0,0 | 1,0 | 12,5 | 132.318 | 2/2 |
| SL5 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 12,0 | 89.543 | 0/1 |
| SL5 | native | 2 | 0,0 | 0,0 | 0,5 | 11,5 | 123.642 | 0/2 |
| SL7 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 16,0 | 146.285 | 0/1 |
| SL7 | native | 2 | 0,0 | 0,0 | 3,0 | 16,0 | 130.464 | 0/2 |
| SL8 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 10,0 | 62.121 | 0/3 |
| SL8 | native | 3 | 0,0 | 0,0 | 0,0 | 7,7 | 55.301 | 0/3 |
| T9 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 5,0 | 36.606 | 3/3 |
| T9 | native | 3 | 0,0 | 0,0 | 0,0 | 4,7 | 25.624 | 3/3 |
| X2 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 5,7 | 21.230 | 3/3 |
| X2 | native | 3 | 0,0 | 0,0 | 0,0 | 4,7 | 27.054 | 3/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

