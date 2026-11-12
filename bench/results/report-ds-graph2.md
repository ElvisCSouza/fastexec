# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `a0827e7fcbe5` | 2.1.322 (Claude Code)
- Execuções: 45 (37 válidas), pares válidos: 12. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,540 (-46,0%)**, IC95% [0,385; 0,725], n = 12 pares.
Só pares em que **os dois acertaram**: 0,550 (-45,0%), n = 11. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| G1 | grafo | 4 | 44.132 | 11.301 | -74,4% | 5,5→4,5 | 18,2→1,4 | 4/5 vs 4/5 | 0,0→0,0 |
| G2 | grafo | 4 | 41.771 | 26.416 | -36,8% | 5,0→5,5 | 18,1→6,6 | 3/5 vs 5/5 | 0,0→0,5 |
| G3 | grafo | 4 | 19.264 | 14.198 | -26,3% | 3,0→3,5 | 4,3→4,3 | 4/5 vs 5/5 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| grafo | 12 | 0,540 | -46,0% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 4,0, FastExec 4,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 10,00 | 5,90 | 0,59 | -41,0% |
| Tempo no modelo (s) | 7,95 | 5,25 | 0,66 | -34,0% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,69 | 0,89 | 0,53 | -47,5% |
| RAM de pico (MB) | 483,00 | 524,50 | 1,09 | +8,6% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,50 | -50,0% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 15 | 12 | 11 (73%) | 0,07 |
| fastexec | 15 | 14 | 14 (93%) | 0,27 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| G1 | 3 | 45.780 | 25.046 | -45,3% | 29% | 4/4 |
| G2 | 1 | 41.902 | 38.482 | -8,2% | 50% | 2/2 |
| G3 | 4 | 19.264 | 32.095 | +66,6% | 27% | 5/5 |

**Média geométrica híbrido/nativo: 1,052 (+5,2%), n = 8 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| G1 | fastexec | 4 | 1,0 | 1,8 | 0,0 | 4,8 | 11.301 | 4/4 |
| G1 | hybrid | 4 | 1,0 | 0,0 | 1,8 | 4,5 | 28.878 | 4/4 |
| G1 | native | 4 | 0,0 | 0,0 | 1,0 | 6,0 | 44.132 | 4/4 |
| G2 | fastexec | 5 | 1,4 | 0,4 | 0,0 | 5,8 | 26.860 | 5/5 |
| G2 | hybrid | 2 | 1,0 | 0,5 | 0,5 | 4,0 | 27.329 | 2/2 |
| G2 | native | 4 | 0,0 | 0,0 | 1,2 | 5,0 | 41.771 | 3/4 |
| G3 | fastexec | 5 | 1,0 | 0,2 | 0,0 | 3,8 | 14.240 | 5/5 |
| G3 | hybrid | 5 | 0,8 | 0,0 | 0,6 | 4,0 | 31.334 | 5/5 |
| G3 | native | 4 | 0,0 | 0,0 | 1,0 | 3,0 | 19.264 | 4/4 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

