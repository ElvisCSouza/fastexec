# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `d581e80a022d` | 2.1.322 (Claude Code)
- Execuções: 225 (225 válidas), pares válidos: 75. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,735 (-26,5%)**, IC95% [0,684; 0,796], n = 70 pares.
Só pares em que **os dois acertaram**: 0,733 (-26,7%), n = 67. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.074 | 17.869 | +11,2% | 3,0→4,0 | 0,2→0,7 | 5/5 vs 5/5 | 0,0→1,0 |
| E2 | falhas | 5 | 12.534 | 12.954 | +3,4% | 2,0→2,0 | 0,0→0,1 | 3/5 vs 4/5 | 0,0→0,0 |
| E3 | falhas | 5 | 31.867 | 18.097 | -43,2% | 5,0→4,0 | 2,4→1,2 | 5/5 vs 5/5 | 1,0→1,0 |
| E4 | falhas | 5 | 10.313 | 7.652 | -25,8% | 3,0→2,0 | 0,1→0,8 | 5/5 vs 5/5 | 0,0→0,0 |
| R1 | codigo | 5 | 11.369 | 14.086 | +23,9% | 3,0→4,0 | 2,0→3,8 | 5/5 vs 5/5 | 0,0→0,0 |
| R2 | codigo | 5 | 16.707 | 12.191 | -27,0% | 3,0→4,0 | 2,5→1,2 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 16.887 | 12.447 | -26,3% | 3,0→3,0 | 3,7→1,4 | 5/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 15.992 | 11.871 | -25,8% | 3,0→3,0 | 0,2→0,4 | 5/5 vs 5/5 | 0,0→0,0 |
| T0 | baseline | 5 | 4.886 | 3.537 | -27,6% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 10.317 | 7.631 | -26,0% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 28.061 | 12.361 | -55,9% | 5,0→4,0 | 2,0→1,3 | 5/5 vs 5/5 | 1,0→1,0 |
| T8 | dados | 5 | 21.784 | 14.422 | -33,8% | 4,0→4,0 | 0,9→2,5 | 5/5 vs 5/5 | 0,0→0,0 |
| T9 | edicao | 5 | 25.700 | 22.432 | -12,7% | 3,0→3,0 | 18,7→17,6 | 5/5 vs 5/5 | 0,0→0,0 |
| X1 | saida | 5 | 18.941 | 11.743 | -38,0% | 3,0→3,0 | 6,1→0,3 | 5/5 vs 5/5 | 0,0→2,0 |
| X2 | saida | 5 | 27.178 | 20.628 | -24,1% | 5,0→5,0 | 0,4→0,4 | 5/5 vs 5/5 | 0,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 20 | 0,788 | -21,2% |
| dados | 10 | 0,620 | -38,0% |
| edicao | 5 | 0,747 | -25,3% |
| falhas | 20 | 0,754 | -24,6% |
| processo | 5 | 0,742 | -25,8% |
| saida | 10 | 0,710 | -29,0% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.883 tokens, FastExec 3.534 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 2,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 5,90 | 5,70 | 0,97 | -3,4% |
| Tempo no modelo (s) | 4,10 | 4,00 | 0,98 | -2,4% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,06 | 0,77 | 0,73 | -27,4% |
| RAM de pico (MB) | 433,00 | 484,00 | 1,12 | +11,8% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,74 | -25,6% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 75 | 75 | 73 (97%) | 0,25 |
| fastexec | 75 | 75 | 74 (99%) | 0,47 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| E1 | 5 | 16.074 | 21.656 | +34,7% | 0% | 5/5 |
| E2 | 5 | 12.534 | 17.375 | +38,6% | 0% | 4/5 |
| E3 | 5 | 31.867 | 56.014 | +75,8% | 0% | 5/5 |
| E4 | 5 | 10.313 | 13.798 | +33,8% | 0% | 5/5 |
| R1 | 5 | 11.369 | 22.953 | +101,9% | 0% | 5/5 |
| R2 | 5 | 16.707 | 21.545 | +29,0% | 27% | 5/5 |
| R3 | 5 | 16.887 | 22.072 | +30,7% | 0% | 5/5 |
| R4 | 5 | 15.992 | 21.205 | +32,6% | 0% | 5/5 |
| T0 | 5 | 4.886 | 6.647 | +36,0% | 0% | 5/5 |
| T10 | 5 | 10.317 | 13.827 | +34,0% | 0% | 5/5 |
| T2 | 5 | 28.061 | 37.491 | +33,6% | 0% | 5/5 |
| T8 | 5 | 21.784 | 32.763 | +50,4% | 0% | 5/5 |
| T9 | 5 | 25.700 | 30.849 | +20,0% | 8% | 5/5 |
| X1 | 5 | 18.941 | 24.485 | +29,3% | 0% | 5/5 |
| X2 | 5 | 27.178 | 29.382 | +8,1% | 0% | 5/5 |

**Média geométrica híbrido/nativo: 1,303 (+30,3%), n = 70 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| R1 | fastexec | 5 | 0,0 | 0,6 | 0,0 | 3,4 | 14.086 | 5/5 |
| R1 | hybrid | 5 | 0,0 | 0,0 | 1,8 | 3,0 | 22.953 | 5/5 |
| R1 | native | 5 | 0,0 | 0,0 | 0,8 | 3,0 | 11.369 | 5/5 |
| R2 | fastexec | 5 | 1,2 | 0,2 | 0,0 | 3,6 | 12.191 | 5/5 |
| R2 | hybrid | 5 | 0,4 | 0,2 | 1,2 | 3,2 | 21.545 | 5/5 |
| R2 | native | 5 | 0,0 | 0,0 | 1,6 | 3,4 | 16.707 | 5/5 |
| R3 | fastexec | 5 | 0,0 | 0,8 | 0,0 | 3,2 | 12.447 | 5/5 |
| R3 | hybrid | 5 | 0,0 | 0,0 | 1,0 | 3,2 | 22.072 | 5/5 |
| R3 | native | 5 | 0,0 | 0,0 | 0,6 | 3,4 | 16.887 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

