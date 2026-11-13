# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `a657b819f601` | 2.1.322 (Claude Code)
- Execuções: 30 (30 válidas), pares válidos: 15. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,679 (-32,1%)**, IC95% [0,448; 1,031], n = 15 pares.
Só pares em que **os dois acertaram**: 0,725 (-27,5%), n = 14. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C4 | contexto | 3 | 13.292 | 8.211 | -38,2% | 2,0→3,0 | 7,3→0,9 | 3/3 vs 3/3 | 0,0→0,0 |
| CS1 | csharp | 3 | 10.820 | 12.516 | +15,7% | 2,0→3,0 | 1,0→1,9 | 3/3 vs 3/3 | 0,0→0,0 |
| G1 | grafo | 3 | 34.051 | 51.987 | +52,7% | 4,0→19,0 | 17,4→15,0 | 2/3 vs 3/3 | 0,0→0,0 |
| G2 | grafo | 3 | 46.499 | 13.987 | -69,9% | 7,0→5,0 | 20,1→1,8 | 3/3 vs 3/3 | 0,0→0,0 |
| G3 | grafo | 3 | 19.793 | 19.980 | +0,9% | 3,0→4,0 | 4,5→3,7 | 3/3 vs 3/3 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 3 | 0,526 | -47,4% |
| csharp | 3 | 1,002 | +0,2% |
| grafo | 9 | 0,650 | -35,0% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 3,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 6,60 | 4,50 | 0,68 | -31,8% |
| Tempo no modelo (s) | 4,10 | 3,00 | 0,73 | -26,8% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,64 | 4,58 | 2,79 | +179,3% |
| RAM de pico (MB) | 463,00 | 1.144,00 | 2,47 | +147,1% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,63 | -37,3% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 15 | 15 | 14 (93%) | 0,07 |
| fastexec | 15 | 15 | 15 (100%) | 0,13 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C4 | fastexec | 3 | 1,3 | 0,7 | 0,0 | 3,0 | 8.211 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,0 | 2,3 | 13.292 | 3/3 |
| CS1 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 3,3 | 12.516 | 3/3 |
| CS1 | native | 3 | 0,0 | 0,0 | 1,3 | 2,3 | 10.820 | 3/3 |
| G1 | fastexec | 3 | 1,3 | 2,7 | 0,0 | 17,0 | 51.987 | 3/3 |
| G1 | native | 3 | 0,0 | 0,0 | 1,0 | 7,3 | 34.051 | 2/3 |
| G2 | fastexec | 3 | 2,0 | 0,0 | 0,0 | 5,0 | 13.987 | 3/3 |
| G2 | native | 3 | 0,0 | 0,0 | 1,7 | 7,3 | 46.499 | 3/3 |
| G3 | fastexec | 3 | 1,3 | 0,0 | 0,0 | 4,7 | 19.980 | 3/3 |
| G3 | native | 3 | 0,0 | 0,0 | 1,0 | 3,0 | 19.793 | 3/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

