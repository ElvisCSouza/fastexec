# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `cf02eefad81c` | 2.1.322 (Claude Code)
- Execuções: 45 (38 válidas), pares válidos: 10. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,632 (-36,8%)**, IC95% [0,438; 0,896], n = 10 pares.
Só pares em que **os dois acertaram**: 0,632 (-36,8%), n = 10. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| G1 | grafo | 3 | 22.018 | 8.407 | -61,8% | 4,0→3,0 | 5,5→1,2 | 3/5 vs 5/5 | 0,0→0,0 |
| G2 | grafo | 2 | 34.204 | 22.273 | -34,9% | 5,0→5,5 | 23,9→4,5 | 3/5 vs 4/5 | 0,0→0,0 |
| G3 | grafo | 5 | 27.633 | 31.442 | +13,8% | 4,0→7,0 | 5,8→5,3 | 5/5 vs 5/5 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| grafo | 10 | 0,632 | -36,8% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 3,0, FastExec 3,5.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 10,15 | 6,15 | 0,61 | -39,4% |
| Tempo no modelo (s) | 7,00 | 5,55 | 0,79 | -20,7% |
| Tempo fora do modelo: ferramentas (s) | 3,00 | 1,00 | 0,33 | -66,7% |
| CPU (s) | 1,65 | 0,78 | 0,47 | -52,6% |
| RAM de pico (MB) | 477,00 | 512,50 | 1,07 | +7,4% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,82 | -18,3% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 15 | 11 | 11 (73%) | 0,00 |
| fastexec | 15 | 14 | 14 (93%) | 0,13 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| G1 | 3 | 22.018 | 38.568 | +75,2% | 34% | 5/5 |
| G2 | 1 | 41.230 | 38.909 | -5,6% | 27% | 3/3 |
| G3 | 5 | 27.633 | 24.922 | -9,8% | 35% | 5/5 |

**Média geométrica híbrido/nativo: 1,081 (+8,1%), n = 9 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| G1 | fastexec | 5 | 1,4 | 0,6 | 0,0 | 3,8 | 8.407 | 5/5 |
| G1 | hybrid | 5 | 1,0 | 1,2 | 2,6 | 7,4 | 38.568 | 5/5 |
| G1 | native | 3 | 0,0 | 0,0 | 1,0 | 4,0 | 22.018 | 3/3 |
| G2 | fastexec | 4 | 1,5 | 0,5 | 0,0 | 5,5 | 22.362 | 4/4 |
| G2 | hybrid | 3 | 1,0 | 0,0 | 1,7 | 4,7 | 38.161 | 3/3 |
| G2 | native | 3 | 0,0 | 0,0 | 1,3 | 6,0 | 41.230 | 3/3 |
| G3 | fastexec | 5 | 1,2 | 0,8 | 0,0 | 5,8 | 31.442 | 5/5 |
| G3 | hybrid | 5 | 1,2 | 0,0 | 1,2 | 4,4 | 24.922 | 5/5 |
| G3 | native | 5 | 0,0 | 0,0 | 1,2 | 5,0 | 27.633 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

