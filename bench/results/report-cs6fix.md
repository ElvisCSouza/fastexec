# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `ef0a7d1fae8e` | 2.1.322 (Claude Code)
- Execuções: 30 (30 válidas), pares válidos: 15. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,903 (-9,7%)**, IC95% [0,747; 1,063], n = 15 pares.
Só pares em que **os dois acertaram**: 0,905 (-9,5%), n = 14. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| C4 | contexto | 3 | 13.135 | 14.500 | +10,4% | 2,0→4,0 | 7,3→3,8 | 3/3 vs 3/3 | 0,0→0,0 |
| CS1 | csharp | 3 | 10.918 | 12.574 | +15,2% | 2,0→3,0 | 1,0→1,4 | 3/3 vs 3/3 | 0,0→0,0 |
| CS6 | csharp | 3 | 128.676 | 56.253 | -56,3% | 16,0→9,0 | 16,0→10,6 | 3/3 vs 3/3 | 0,0→0,0 |
| T9 | edicao | 3 | 25.650 | 22.590 | -11,9% | 3,0→3,0 | 18,7→17,6 | 3/3 vs 3/3 | 0,0→0,0 |
| W6 | terminal | 3 | 23.796 | 25.291 | +6,3% | 5,0→5,0 | 1,1→1,7 | 2/3 vs 3/3 | 0,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| contexto | 3 | 1,274 | +27,4% |
| csharp | 6 | 0,794 | -20,6% |
| edicao | 3 | 0,752 | -24,8% |
| terminal | 3 | 0,991 | -0,9% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 3,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 3,10 | 3,80 | 1,23 | +22,6% |
| Tempo no modelo (s) | 2,60 | 2,80 | 1,08 | +7,7% |
| Tempo fora do modelo: ferramentas (s) | 1,00 | 1,00 | 1,00 | 0,0% |
| CPU (s) | 0,92 | 1,20 | 1,30 | +30,4% |
| RAM de pico (MB) | 464,00 | 531,00 | 1,14 | +14,4% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,91 | -8,6% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 15 | 15 | 14 (93%) | 0,20 |
| fastexec | 15 | 15 | 15 (100%) | 0,27 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| C4 | fastexec | 3 | 1,0 | 1,0 | 0,0 | 4,3 | 14.500 | 3/3 |
| C4 | native | 3 | 0,0 | 0,0 | 1,0 | 2,0 | 13.135 | 3/3 |
| CS1 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 3,0 | 12.574 | 3/3 |
| CS1 | native | 3 | 0,0 | 0,0 | 1,0 | 2,0 | 10.918 | 3/3 |
| CS6 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 11,3 | 56.253 | 3/3 |
| CS6 | native | 3 | 0,0 | 0,0 | 2,0 | 14,3 | 128.676 | 3/3 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

