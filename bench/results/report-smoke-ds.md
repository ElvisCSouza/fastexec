# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `40dc3d157330` | 2.1.322 (Claude Code)
- Execuções: 4 (4 válidas), pares válidos: 2. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,538 (-46,2%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 0,538 (-46,2%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| T0 | baseline | 1 | 4.885 | 4.245 | -13,1% | 1,0→1,0 | 0,0→0,0 | 1/1 vs 1/1 | 0,0→0,0 |
| T2 | dados | 1 | 35.294 | 19.003 | -46,2% | 6,0→4,0 | 2,2→0,7 | 1/1 vs 1/1 | 1,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| dados | 1 | 0,538 | -46,2% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.882 tokens, FastExec 4.242 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,5, FastExec 1,5.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 3,90 | 2,30 | 0,59 | -41,0% |
| Tempo no modelo (s) | 2,70 | 1,70 | 0,63 | -37,0% |
| Tempo fora do modelo: ferramentas (s) | 1,00 | 1,00 | 1,00 | 0,0% |
| CPU (s) | 0,82 | 0,59 | 0,73 | -27,4% |
| RAM de pico (MB) | 409,50 | 434,50 | 1,06 | +6,1% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,58 | -42,4% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 2 | 2 | 2 (100%) | 0,50 |
| fastexec | 2 | 2 | 2 (100%) | 1,00 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

