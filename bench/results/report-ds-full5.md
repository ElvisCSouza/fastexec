# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `5340bdedaf4d` | 2.1.322 (Claude Code)
- Execuções: 225 (225 válidas), pares válidos: 75. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,661 (-33,9%)**, IC95% [0,601; 0,730], n = 70 pares.
Só pares em que **os dois acertaram**: 0,669 (-33,1%), n = 68. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| E1 | falhas | 5 | 16.905 | 17.014 | +0,6% | 3,0→4,0 | 0,4→0,7 | 5/5 vs 5/5 | 0,0→1,0 |
| E2 | falhas | 5 | 13.173 | 8.790 | -33,3% | 2,0→2,0 | 0,1→0,1 | 4/5 vs 4/5 | 0,0→0,0 |
| E3 | falhas | 5 | 31.058 | 18.576 | -40,2% | 5,0→4,0 | 2,4→1,2 | 5/5 vs 5/5 | 1,0→1,0 |
| E4 | falhas | 5 | 10.255 | 7.648 | -25,4% | 3,0→2,0 | 0,1→0,8 | 5/5 vs 5/5 | 0,0→0,0 |
| R1 | codigo | 5 | 17.456 | 8.399 | -51,9% | 3,0→3,0 | 2,5→2,2 | 5/5 vs 5/5 | 0,0→0,0 |
| R2 | codigo | 5 | 16.894 | 12.163 | -28,0% | 4,0→4,0 | 2,2→0,7 | 5/5 vs 5/5 | 0,0→0,0 |
| R3 | codigo | 5 | 16.896 | 8.258 | -51,1% | 3,0→2,0 | 3,0→0,5 | 5/5 vs 5/5 | 0,0→0,0 |
| R4 | codigo | 5 | 16.057 | 11.853 | -26,2% | 3,0→3,0 | 0,3→0,4 | 5/5 vs 5/5 | 0,0→0,0 |
| T0 | baseline | 5 | 4.888 | 3.539 | -27,6% | 1,0→1,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T10 | processo | 5 | 10.165 | 7.523 | -26,0% | 2,0→2,0 | 0,0→0,0 | 5/5 vs 5/5 | 0,0→0,0 |
| T2 | dados | 5 | 30.118 | 12.264 | -59,3% | 5,0→3,0 | 2,4→1,1 | 5/5 vs 5/5 | 1,0→1,0 |
| T8 | dados | 5 | 21.764 | 17.772 | -18,3% | 4,0→5,0 | 0,8→1,8 | 5/5 vs 5/5 | 0,0→0,0 |
| T9 | edicao | 5 | 25.684 | 31.972 | +24,5% | 3,0→4,0 | 18,7→17,8 | 5/5 vs 5/5 | 0,0→0,0 |
| X1 | saida | 5 | 18.732 | 7.633 | -59,3% | 3,0→2,0 | 5,1→0,1 | 5/5 vs 5/5 | 0,0→1,0 |
| X2 | saida | 5 | 21.868 | 17.182 | -21,4% | 4,0→5,0 | 0,4→0,4 | 5/5 vs 5/5 | 0,0→2,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| codigo | 20 | 0,626 | -37,4% |
| dados | 10 | 0,539 | -46,1% |
| edicao | 5 | 1,089 | +8,9% |
| falhas | 20 | 0,658 | -34,2% |
| processo | 5 | 0,734 | -26,6% |
| saida | 10 | 0,673 | -32,7% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo 4.885 tokens, FastExec 3.536 tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 2,0, FastExec 2,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 4,10 | 3,20 | 0,78 | -22,0% |
| Tempo no modelo (s) | 2,30 | 2,00 | 0,87 | -13,0% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 1,00 | 0,50 | -50,0% |
| CPU (s) | 1,05 | 0,72 | 0,69 | -31,4% |
| RAM de pico (MB) | 436,00 | 467,00 | 1,07 | +7,1% |
| Custo (US$, sem cache) | 0,01 | 0,00 | 0,70 | -30,4% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 75 | 75 | 74 (99%) | 0,19 |
| fastexec | 75 | 75 | 74 (99%) | 0,39 |

## 5. Híbrido: ferramentas nativas + FastExec juntas

O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). % FastExec = parte das chamadas de ferramenta que foi para o FastExec.

| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |
|---|---|---|---|---|---|---|
| E1 | 5 | 16.905 | 21.469 | +27,0% | 0% | 5/5 |
| E2 | 5 | 13.173 | 17.375 | +31,9% | 12% | 5/5 |
| E3 | 5 | 31.058 | 49.178 | +58,3% | 0% | 5/5 |
| E4 | 5 | 10.255 | 13.692 | +33,5% | 0% | 5/5 |
| R1 | 5 | 17.456 | 14.508 | -16,9% | 0% | 5/5 |
| R2 | 5 | 16.894 | 22.131 | +31,0% | 43% | 5/5 |
| R3 | 5 | 16.896 | 22.291 | +31,9% | 0% | 5/5 |
| R4 | 5 | 16.057 | 21.304 | +32,7% | 0% | 5/5 |
| T0 | 5 | 4.888 | 6.647 | +36,0% | 0% | 5/5 |
| T10 | 5 | 10.165 | 13.713 | +34,9% | 0% | 5/5 |
| T2 | 5 | 30.118 | 46.535 | +54,5% | 4% | 5/5 |
| T8 | 5 | 21.764 | 45.462 | +108,9% | 0% | 5/5 |
| T9 | 5 | 25.684 | 30.902 | +20,3% | 20% | 5/5 |
| X1 | 5 | 18.732 | 24.424 | +30,4% | 0% | 5/5 |
| X2 | 5 | 21.868 | 29.274 | +33,9% | 0% | 5/5 |

**Média geométrica híbrido/nativo: 1,312 (+31,2%), n = 70 pares.**

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| R1 | fastexec | 5 | 0,0 | 0,4 | 0,0 | 2,6 | 8.399 | 5/5 |
| R1 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 2,2 | 14.508 | 5/5 |
| R1 | native | 5 | 0,0 | 0,0 | 1,0 | 3,2 | 17.456 | 5/5 |
| R2 | fastexec | 5 | 1,2 | 0,4 | 0,0 | 3,6 | 12.163 | 5/5 |
| R2 | hybrid | 5 | 1,0 | 0,0 | 1,0 | 3,8 | 22.131 | 5/5 |
| R2 | native | 5 | 0,0 | 0,0 | 1,4 | 3,6 | 16.894 | 5/5 |
| R3 | fastexec | 5 | 0,0 | 0,6 | 0,0 | 2,6 | 8.258 | 5/5 |
| R3 | hybrid | 5 | 0,0 | 0,0 | 0,8 | 3,2 | 22.291 | 5/5 |
| R3 | native | 5 | 0,0 | 0,0 | 1,0 | 3,4 | 16.896 | 5/5 |

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

