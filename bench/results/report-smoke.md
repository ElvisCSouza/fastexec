# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `claude-sonnet-5-5` | cache de prompt: desligado (tokens comparáveis) | FastExec `40dc3d157330` | 2.1.322 (Claude Code)
- Execuções: 4 (4 válidas), pares válidos: 2. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,417 (-58,3%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 0,417 (-58,3%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| T0 | baseline | 1 | 7.650 | 6.578 | -14,0% | 1,0→1,0 | 0,0→0,0 | 1/1 vs 1/1 | 0,0→0,0 |
| T2 | dados | 1 | 33.410 | 13.921 | -58,3% | 4,0→2,0 | 1,1→0,4 | 1/1 vs 1/1 | 1,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| dados | 1 | 0,417 | -58,3% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas, mediana do 1º turno): nativo 7.688 tokens, FastExec 6.618 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 1,5, FastExec 0,5.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 5,70 | 3,70 | 0,65 | -35,1% |
| Tempo no modelo (s) | 3,70 | 2,05 | 0,55 | -44,6% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,35 | 0,82 | 0,61 | -38,9% |
| RAM de pico (MB) | 461,50 | 509,00 | 1,10 | +10,3% |
| Custo (US$, sem cache) | 0,04 | 0,02 | 0,49 | -50,7% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 2 | 2 | 2 (100%) | 0,50 |
| fastexec | 2 | 2 | 2 (100%) | 0,00 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

