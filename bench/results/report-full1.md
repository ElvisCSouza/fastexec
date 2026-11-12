# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `claude-sonnet-5-5` (Anthropic) | cache de prompt: desligado (tokens comparáveis) | FastExec `40dc3d157330` | 2.1.322 (Claude Code)
- Execuções: 130 (130 válidas), pares válidos: 65. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,759 (-24,1%)**, IC95% [0,703; 0,817], n = 60 pares.
Só pares em que **os dois acertaram**: 0,749 (-25,1%), n = 54. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.295 | 14.170 | -13,0% | 2,0→2,0 | 0,1→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| E2 | falhas | 5 | 16.837 | 14.461 | -14,1% | 2,0→2,0 | 0,1→0,1 | 5/5 vs 5/5 | 0,0→0,0 |
| E3 | falhas | 5 | 15.841 | 14.396 | -9,1% | 2,0→2,0 | 0,1→1,7 | 5/5 vs 5/5 | 0,0→0,0 |
| E4 | falhas | 5 | 15.658 | 13.635 | -12,9% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| R1 | codigo | 5 | 16.807 | 14.714 | -12,5% | 2,0→2,0 | 2,0→2,1 | 2/5 vs 5/5 | 0,0→0,0 |
| R2 | codigo | 5 | 24.896 | 13.793 | -44,6% | 3,0→2,0 | 1,5→0,2 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 16.656 | 14.066 | -15,5% | 3,0→3,0 | 0,8→0,3 | 2/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 15.923 | 14.043 | -11,8% | 2,0→2,0 | 0,1→0,2 | 5/5 vs 5/5 | 0,0→0,0 |
| T0 | baseline | 5 | 7.644 | 6.572 | -14,0% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 15.724 | 13.736 | -12,6% | 2,0→2,0 | 0,0→0,1 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 33.234 | 13.896 | -58,2% | 4,0→2,0 | 0,5→0,4 | 5/5 vs 5/5 | 1,0→0,0 |
| T8 | dados | 5 | 16.179 | 14.423 | -10,9% | 2,0→3,0 | 0,2→0,7 | 5/5 vs 5/5 | 0,0→0,0 |
| T9 | edicao | 5 | 32.642 | 13.719 | -58,0% | 4,0→2,0 | 0,3→0,1 | 5/5 vs 5/5 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 20 | 0,831 | -16,9% |
| dados | 10 | 0,585 | -41,5% |
| edicao | 5 | 0,435 | -56,5% |
| falhas | 20 | 0,877 | -12,3% |
| processo | 5 | 0,874 | -12,6% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 7.730 tokens, FastExec 6.661 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 1,0, FastExec 1,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 4,70 | 4,40 | 0,94 | -6,4% |
| Tempo no modelo (s) | 2,80 | 2,70 | 0,96 | -3,6% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,12 | 0,83 | 0,74 | -25,9% |
| RAM de pico (MB) | 480,00 | 511,00 | 1,06 | +6,5% |
| Custo (US$, sem cache) | 0,03 | 0,03 | 0,87 | -12,7% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 65 | 65 | 59 (91%) | 0,08 |
| fastexec | 65 | 65 | 65 (100%) | 0,00 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

