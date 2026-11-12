# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `1ee7db7ef50f` | 2.1.322 (Claude Code)
- Execuções: 80 (77 válidas), pares válidos: 37. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,897 (-10,3%)**, IC95% [0,791; 1,037], n = 37 pares.
Só pares em que **os dois acertaram**: 0,907 (-9,3%), n = 32. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C3 | contexto | 2 | 17.495 | 13.744 | -21,4% | 3,5→4,0 | 2,2→2,1 | 2/2 vs 2/2 | 0,0→0,0 |
| C4 | contexto | 2 | 13.097 | 26.384 | +101,5% | 2,0→5,5 | 7,3→7,6 | 1/2 vs 2/2 | 0,0→0,0 |
| C6 | contexto | 2 | 23.160 | 19.336 | -16,5% | 4,5→4,0 | 6,8→7,2 | 0/2 vs 1/2 | 0,0→0,0 |
| CS1 | csharp | 2 | 13.870 | 23.270 | +67,8% | 2,5→4,5 | 1,1→8,5 | 2/2 vs 2/2 | 0,0→0,0 |
| CS2 | csharp | 2 | 32.420 | 36.232 | +11,8% | 6,0→7,0 | 13,4→10,5 | 2/2 vs 2/2 | 0,0→0,0 |
| CS3 | csharp | 1 | 63.275 | 63.889 | +1,0% | 12,0→11,0 | 12,7→14,7 | 1/1 vs 1/1 | 0,0→0,0 |
| CS4 | csharp | 1 | 60.698 | 91.757 | +51,2% | 9,0→14,0 | 11,4→24,9 | 1/1 vs 1/1 | 0,0→2,0 |
| CS5 | csharp | 1 | 10.763 | 7.977 | -25,9% | 3,0→3,0 | 0,8→1,0 | 1/1 vs 1/1 | 0,0→0,0 |
| CS7 | csharp | 1 | 35.439 | 13.976 | -60,6% | 8,0→5,0 | 10,3→4,3 | 1/1 vs 1/1 | 0,0→0,0 |
| CS8 | csharp | 1 | 161.396 | 183.372 | +13,6% | 19,0→16,0 | 26,0→35,8 | 1/1 vs 1/1 | 0,0→1,0 |
| D1 | docs | 1 | 19.406 | 20.949 | +8,0% | 7,0→6,0 | 6,7→5,8 | 1/1 vs 1/1 | 0,0→0,0 |
| D2 | docs | 1 | 33.885 | 21.600 | -36,3% | 7,0→7,0 | 6,2→7,2 | 1/1 vs 1/1 | 0,0→0,0 |
| D3 | docs | 1 | 10.359 | 7.587 | -26,8% | 2,0→2,0 | 0,1→0,3 | 1/1 vs 1/1 | 0,0→0,0 |
| D4 | docs | 1 | 28.929 | 16.178 | -44,1% | 10,0→7,0 | 8,9→8,1 | 1/1 vs 1/1 | 0,0→0,0 |
| D5 | docs | 1 | 32.406 | 22.837 | -29,5% | 8,0→6,0 | 5,3→12,8 | 1/1 vs 1/1 | 0,0→0,0 |
| D6 | docs | 1 | 30.270 | 18.622 | -38,5% | 7,0→6,0 | 3,6→4,9 | 1/1 vs 1/1 | 0,0→0,0 |
| M1 | edicao-multi | 2 | 33.486 | 24.506 | -26,8% | 9,0→6,0 | 4,4→3,4 | 2/2 vs 2/2 | 0,0→0,0 |
| M2 | edicao-multi | 2 | 35.470 | 38.331 | +8,1% | 14,0→16,5 | 5,2→5,6 | 2/2 vs 2/2 | 0,0→1,0 |
| M3 | edicao-multi | 2 | 33.445 | 43.620 | +30,4% | 13,0→17,5 | 2,8→4,9 | 2/2 vs 2/2 | 0,5→1,5 |
| M4 | edicao-multi | 2 | 75.509 | 51.034 | -32,4% | 15,5→16,0 | 7,6→4,1 | 2/2 vs 2/2 | 0,0→0,0 |
| M5 | edicao-multi | 2 | 61.374 | 79.843 | +30,1% | 10,5→13,5 | 4,4→4,6 | 2/2 vs 2/2 | 0,5→1,5 |
| M6 | edicao-multi | 2 | 23.405 | 18.358 | -21,6% | 8,5→9,5 | 1,3→1,2 | 2/2 vs 2/2 | 0,0→0,0 |
| P3 | mcp | 2 | 10.450 | 7.691 | -26,4% | 2,0→2,0 | 0,2→0,2 | 2/2 vs 2/2 | 0,0→0,0 |
| P4 | mcp | 2 | 75.806 | 43.580 | -42,5% | 11,0→8,5 | 1,6→1,1 | 2/2 vs 0/2 | 1,0→1,5 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 6 | 1,099 | +9,9% |
| csharp | 9 | 1,002 | +0,2% |
| docs | 6 | 0,704 | -29,6% |
| edicao-multi | 12 | 0,937 | -6,3% |
| mcp | 4 | 0,652 | -34,8% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 7,0, FastExec 6,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 5,70 | 7,30 | 1,28 | +28,1% |
| Tempo no modelo (s) | 3,60 | 4,70 | 1,31 | +30,6% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,41 | 1,02 | 0,72 | -27,7% |
| RAM de pico (MB) | 452,00 | 514,00 | 1,14 | +13,7% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,78 | -22,0% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 40 | 38 | 35 (88%) | 0,10 |
| fastexec | 40 | 39 | 35 (88%) | 0,50 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C4 | fastexec | 2 | 2,0 | 0,0 | 0,0 | 5,5 | 26.384 | 2/2 |
| C4 | native | 2 | 0,0 | 0,0 | 1,0 | 2,0 | 13.097 | 1/2 |
| C5 | fastexec | 2 | 2,5 | 0,5 | 0,0 | 12,5 | 79.548 | 0/2 |
| C6 | fastexec | 2 | 0,0 | 1,0 | 0,0 | 4,0 | 19.336 | 1/2 |
| C6 | native | 2 | 0,0 | 0,0 | 2,5 | 4,5 | 23.160 | 0/2 |
| CS1 | fastexec | 2 | 0,0 | 0,5 | 0,0 | 4,5 | 23.270 | 2/2 |
| CS1 | native | 2 | 0,0 | 0,0 | 1,0 | 2,5 | 13.870 | 2/2 |
| CS2 | fastexec | 2 | 0,0 | 0,5 | 0,0 | 7,0 | 36.232 | 2/2 |
| CS2 | native | 2 | 0,0 | 0,0 | 2,0 | 6,0 | 32.420 | 2/2 |
| CS3 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 11,0 | 63.889 | 1/1 |
| CS3 | native | 1 | 0,0 | 0,0 | 2,0 | 12,0 | 63.275 | 1/1 |
| CS4 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 14,0 | 91.757 | 1/1 |
| CS4 | native | 1 | 0,0 | 0,0 | 2,0 | 9,0 | 60.698 | 1/1 |
| CS5 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 3,0 | 7.977 | 1/1 |
| CS5 | native | 1 | 0,0 | 0,0 | 2,0 | 3,0 | 10.763 | 1/1 |
| CS7 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 5,0 | 13.976 | 1/1 |
| CS7 | native | 1 | 0,0 | 0,0 | 3,0 | 8,0 | 35.439 | 1/1 |
| CS8 | fastexec | 1 | 0,0 | 2,0 | 0,0 | 16,0 | 183.372 | 1/1 |
| CS8 | native | 1 | 0,0 | 0,0 | 5,0 | 19,0 | 161.396 | 1/1 |
| D6 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 6,0 | 18.622 | 1/1 |
| D6 | native | 1 | 0,0 | 0,0 | 0,0 | 7,0 | 30.270 | 1/1 |
| M1 | fastexec | 2 | 0,0 | 0,5 | 0,0 | 6,0 | 24.506 | 2/2 |
| M1 | native | 2 | 0,0 | 0,0 | 2,0 | 9,0 | 33.486 | 2/2 |
| M2 | fastexec | 2 | 1,0 | 0,5 | 0,0 | 16,5 | 38.331 | 2/2 |
| M2 | native | 2 | 0,0 | 0,0 | 0,5 | 14,0 | 35.470 | 2/2 |
| M3 | fastexec | 2 | 1,0 | 0,0 | 0,0 | 17,5 | 43.620 | 2/2 |
| M3 | native | 2 | 0,0 | 0,0 | 0,0 | 13,0 | 33.445 | 2/2 |
| M4 | fastexec | 2 | 0,0 | 0,5 | 0,0 | 16,0 | 51.034 | 2/2 |
| M4 | native | 2 | 0,0 | 0,0 | 0,5 | 15,5 | 75.509 | 2/2 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

