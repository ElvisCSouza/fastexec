# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `15017c424c90` | 2.1.322 (Claude Code)
- Execuções: 204 (185 válidas), pares válidos: 88. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,840 (-16,0%)**, IC95% [0,757; 0,932], n = 88 pares.
Só pares em que **os dois acertaram**: 0,825 (-17,5%), n = 73. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C1 | contexto | 3 | 51.210 | 43.172 | -15,7% | 7,0→9,0 | 18,2→15,8 | 3/3 vs 3/3 | 0,0→0,0 |
| C3 | contexto | 3 | 17.872 | 20.440 | +14,4% | 5,0→6,0 | 2,4→3,7 | 3/3 vs 3/3 | 0,0→0,0 |
| C4 | contexto | 3 | 13.066 | 14.507 | +11,0% | 2,0→4,0 | 7,3→3,6 | 3/3 vs 3/3 | 0,0→0,0 |
| C5 | contexto | 1 | 137.497 | 62.002 | -54,9% | 14,0→12,0 | 20,4→11,3 | 0/3 vs 0/3 | 0,0→0,0 |
| C6 | contexto | 3 | 19.494 | 10.390 | -46,7% | 4,0→3,0 | 7,7→6,4 | 1/3 vs 1/3 | 0,0→0,0 |
| M1 | edicao-multi | 3 | 42.913 | 40.413 | -5,8% | 21,0→8,0 | 8,4→4,8 | 0/3 vs 0/3 | 0,0→1,0 |
| M2 | edicao-multi | 3 | 44.395 | 35.085 | -21,0% | 17,0→16,0 | 5,8→6,0 | 3/3 vs 3/3 | 0,0→1,0 |
| M3 | edicao-multi | 3 | 57.151 | 21.208 | -62,9% | 15,0→13,0 | 3,6→3,9 | 3/3 vs 3/3 | 0,0→0,0 |
| M4 | edicao-multi | 3 | 58.197 | 68.156 | +17,1% | 16,0→20,0 | 4,3→3,2 | 3/3 vs 3/3 | 0,0→1,0 |
| M5 | edicao-multi | 3 | 54.501 | 52.520 | -3,6% | 10,0→12,0 | 6,1→3,8 | 3/3 vs 3/3 | 0,0→1,0 |
| M6 | edicao-multi | 3 | 17.070 | 18.450 | +8,1% | 7,0→10,0 | 0,8→1,1 | 3/3 vs 3/3 | 0,0→0,0 |
| P1 | mcp | 3 | 18.305 | 18.874 | +3,1% | 14,0→4,0 | 1,9→2,2 | 3/3 vs 3/3 | 0,0→0,0 |
| P2 | mcp | 3 | 23.664 | 28.330 | +19,7% | 5,0→6,0 | 0,7→1,7 | 3/3 vs 3/3 | 0,0→0,0 |
| P3 | mcp | 3 | 10.451 | 12.831 | +22,8% | 2,0→3,0 | 0,2→0,4 | 3/3 vs 2/3 | 0,0→0,0 |
| P4 | mcp | 3 | 57.786 | 79.557 | +37,7% | 9,0→14,0 | 1,2→1,9 | 3/3 vs 3/3 | 0,0→3,0 |
| P5 | mcp | 3 | 10.619 | 12.894 | +21,4% | 2,0→3,0 | 0,3→1,2 | 3/3 vs 3/3 | 0,0→0,0 |
| P6 | mcp | 3 | 43.604 | 25.111 | -42,4% | 7,0→6,0 | 1,0→1,3 | 3/3 vs 3/3 | 1,0→1,0 |
| SL1 | swelite | 2 | 166.627 | 81.720 | -51,0% | 13,0→11,0 | 21,4→14,8 | 3/3 vs 3/3 | 0,0→0,5 |
| SL2 | swelite | 3 | 66.926 | 57.761 | -13,7% | 12,0→10,0 | 11,2→11,7 | 3/3 vs 3/3 | 0,0→1,0 |
| SL3 | swelite | 3 | 38.903 | 40.074 | +3,0% | 7,0→7,0 | 9,4→7,4 | 0/3 vs 0/3 | 0,0→0,0 |
| SL4 | swelite | 3 | 145.820 | 70.762 | -51,5% | 17,0→10,0 | 14,7→13,9 | 3/3 vs 3/3 | 1,0→0,0 |
| SL5 | swelite | 1 | 91.584 | 144.155 | +57,4% | 10,0→14,0 | 20,2→28,3 | 0/3 vs 0/3 | 0,0→0,0 |
| SL8 | swelite | 3 | 55.215 | 58.189 | +5,4% | 8,0→10,0 | 13,7→12,0 | 0/3 vs 0/3 | 0,0→0,0 |
| W1 | terminal | 3 | 16.761 | 8.459 | -49,5% | 3,0→3,0 | 1,0→1,4 | 3/3 vs 3/3 | 0,0→0,0 |
| W2 | terminal | 3 | 24.196 | 19.375 | -19,9% | 5,0→5,0 | 2,6→3,0 | 3/3 vs 3/3 | 0,0→0,0 |
| W3 | terminal | 3 | 50.737 | 30.707 | -39,5% | 7,0→7,0 | 3,8→5,1 | 3/3 vs 3/3 | 0,0→0,0 |
| W4 | terminal | 3 | 22.041 | 14.622 | -33,7% | 3,0→3,0 | 7,6→3,2 | 3/3 vs 3/3 | 0,0→0,0 |
| W5 | terminal | 3 | 36.516 | 12.407 | -66,0% | 8,0→5,0 | 0,7→0,0 | 3/3 vs 3/3 | 0,0→0,0 |
| W6 | terminal | 3 | 22.175 | 19.077 | -14,0% | 4,0→5,0 | 1,4→1,5 | 3/3 vs 3/3 | 0,0→1,0 |
| W7 | terminal | 3 | 31.707 | 26.321 | -17,0% | 7,0→13,0 | 2,2→2,6 | 3/3 vs 3/3 | 0,0→1,0 |
| W8 | terminal | 3 | 28.899 | 33.371 | +15,5% | 5,0→7,0 | 0,8→0,9 | 3/3 vs 3/3 | 1,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 13 | 0,781 | -21,9% |
| edicao-multi | 18 | 0,882 | -11,8% |
| mcp | 18 | 1,023 | +2,3% |
| swelite | 15 | 0,824 | -17,6% |
| terminal | 24 | 0,736 | -26,4% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 6,0, FastExec 6,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 8,00 | 8,85 | 1,11 | +10,6% |
| Tempo no modelo (s) | 4,90 | 5,40 | 1,10 | +10,2% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,83 | 1,58 | 0,86 | -14,2% |
| RAM de pico (MB) | 487,50 | 540,50 | 1,11 | +10,9% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,78 | -22,1% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 102 | 94 | 76 (75%) | 0,19 |
| fastexec | 102 | 91 | 75 (74%) | 0,46 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C1 | fastexec | 3 | 2,0 | 0,3 | 0,0 | 7,7 | 43.172 | 3/3 |
| C1 | native | 3 | 0,0 | 0,0 | 1,7 | 6,7 | 51.210 | 3/3 |
| C2 | fastexec | 1 | 20,0 | 0,0 | 0,0 | 31,0 | 144.649 | 0/1 |
| C4 | fastexec | 3 | 1,7 | 0,3 | 0,0 | 4,3 | 14.507 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,0 | 2,3 | 13.066 | 3/3 |
| C5 | fastexec | 3 | 2,7 | 0,3 | 0,0 | 13,0 | 74.844 | 0/3 |
| C5 | native | 1 | 0,0 | 0,0 | 4,0 | 14,0 | 137.497 | 0/1 |
| C6 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 3,3 | 10.390 | 1/3 |
| C6 | native | 3 | 0,0 | 0,0 | 1,3 | 3,7 | 19.494 | 1/3 |
| M1 | fastexec | 3 | 0,3 | 0,7 | 0,0 | 8,0 | 40.413 | 0/3 |
| M1 | native | 3 | 0,0 | 0,0 | 1,3 | 15,7 | 42.913 | 0/3 |
| M2 | fastexec | 3 | 1,0 | 0,7 | 0,0 | 15,7 | 35.085 | 3/3 |
| M2 | native | 3 | 0,0 | 0,0 | 0,7 | 17,3 | 44.395 | 3/3 |
| M4 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 20,3 | 68.156 | 3/3 |
| M4 | native | 3 | 0,0 | 0,0 | 0,7 | 15,3 | 58.197 | 3/3 |
| SL5 | fastexec | 1 | 0,0 | 2,0 | 0,0 | 14,0 | 144.155 | 0/1 |
| SL5 | native | 3 | 0,0 | 0,0 | 0,7 | 12,0 | 135.203 | 0/3 |
| SL8 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 10,0 | 58.189 | 0/3 |
| SL8 | native | 3 | 0,0 | 0,0 | 0,0 | 7,7 | 55.215 | 0/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

