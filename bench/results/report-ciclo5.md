# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `1ee7db7ef50f` | 2.1.322 (Claude Code)
- Execuções: 12 (12 válidas), pares válidos: 6. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,718 (-28,2%)**, IC95% [0,575; 0,901], n = 6 pares.
Só pares em que **os dois acertaram**: 0,718 (-28,2%), n = 6. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS1 | csharp | 1 | 10.847 | 8.163 | -24,7% | 2,0→2,0 | 1,0→1,5 | 1/1 vs 1/1 | 0,0→0,0 |
| CS4 | csharp | 1 | 99.652 | 101.726 | +2,1% | 13,0→15,0 | 18,4→20,8 | 1/1 vs 1/1 | 0,0→0,0 |
| CS8 | csharp | 1 | 108.659 | 60.385 | -44,4% | 13,0→11,0 | 15,1→15,3 | 1/1 vs 1/1 | 0,0→2,0 |
| M2 | edicao-multi | 1 | 51.805 | 34.401 | -33,6% | 17,0→16,0 | 5,2→5,9 | 1/1 vs 1/1 | 0,0→1,0 |
| M3 | edicao-multi | 1 | 48.713 | 49.385 | +1,4% | 15,0→15,0 | 3,2→5,6 | 1/1 vs 1/1 | 0,0→2,0 |
| P4 | mcp | 1 | 95.833 | 45.592 | -52,4% | 13,0→9,0 | 2,0→1,2 | 1/1 vs 1/1 | 1,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 3 | 0,753 | -24,7% |
| edicao-multi | 2 | 0,820 | -18,0% |
| mcp | 1 | 0,476 | -52,4% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 12,0, FastExec 12,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 11,85 | 7,15 | 0,60 | -39,7% |
| Tempo no modelo (s) | 7,65 | 5,10 | 0,67 | -33,3% |
| Tempo fora do modelo: ferramentas (s) | 3,50 | 2,00 | 0,57 | -42,9% |
| CPU (s) | 3,00 | 1,47 | 0,49 | -51,0% |
| RAM de pico (MB) | 593,00 | 524,00 | 0,88 | -11,6% |
| Custo (US$, sem cache) | 0,02 | 0,02 | 0,65 | -35,2% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 6 | 6 | 6 (100%) | 0,17 |
| fastexec | 6 | 6 | 6 (100%) | 0,83 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| CS4 | fastexec | 1 | 0,0 | 1,0 | 0,0 | 15,0 | 101.726 | 1/1 |
| CS4 | native | 1 | 0,0 | 0,0 | 3,0 | 13,0 | 99.652 | 1/1 |
| CS8 | fastexec | 1 | 1,0 | 1,0 | 0,0 | 11,0 | 60.385 | 1/1 |
| CS8 | native | 1 | 0,0 | 0,0 | 3,0 | 13,0 | 108.659 | 1/1 |
| M2 | fastexec | 1 | 1,0 | 1,0 | 0,0 | 16,0 | 34.401 | 1/1 |
| M2 | native | 1 | 0,0 | 0,0 | 1,0 | 17,0 | 51.805 | 1/1 |
| M3 | fastexec | 1 | 1,0 | 1,0 | 0,0 | 15,0 | 49.385 | 1/1 |
| M3 | native | 1 | 0,0 | 0,0 | 1,0 | 15,0 | 48.713 | 1/1 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

