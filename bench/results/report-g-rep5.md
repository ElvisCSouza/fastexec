# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `a657b819f601` | 2.1.322 (Claude Code)
- Execuções: 20 (20 válidas), pares válidos: 10. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,318 (-68,2%)**, IC95% [0,255; 0,402], n = 10 pares.
Só pares em que **os dois acertaram**: 0,318 (-68,2%), n = 10. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| G1 | grafo | 5 | 43.481 | 8.849 | -79,6% | 6,0→3,0 | 10,1→1,3 | 5/5 vs 5/5 | 0,0→0,0 |
| G3 | grafo | 5 | 26.789 | 8.703 | -67,5% | 4,0→3,0 | 5,3→1,3 | 5/5 vs 5/5 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| grafo | 10 | 0,318 | -68,2% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 4,5, FastExec 2,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 9,05 | 4,15 | 0,46 | -54,1% |
| Tempo no modelo (s) | 5,55 | 2,70 | 0,49 | -51,4% |
| Tempo fora do modelo: ferramentas (s) | 3,00 | 1,00 | 0,33 | -66,7% |
| CPU (s) | 2,41 | 4,30 | 1,79 | +79,0% |
| RAM de pico (MB) | 480,00 | 1.166,50 | 2,43 | +143,0% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,30 | -70,3% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 10 | 10 | 10 (100%) | 0,00 |
| fastexec | 10 | 10 | 10 (100%) | 0,00 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| G1 | fastexec | 5 | 1,2 | 1,6 | 0,0 | 5,8 | 8.849 | 5/5 |
| G1 | native | 5 | 0,0 | 0,0 | 1,4 | 10,4 | 43.481 | 5/5 |
| G3 | fastexec | 5 | 1,2 | 0,4 | 0,0 | 3,2 | 8.703 | 5/5 |
| G3 | native | 5 | 0,0 | 0,0 | 1,2 | 4,0 | 26.789 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

