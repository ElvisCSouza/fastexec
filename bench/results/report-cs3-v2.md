# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `2098336d5b47` | 2.1.322 (Claude Code)
- Execuções: 2 (2 válidas), pares válidos: 1. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,433 (-56,7%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 0,433 (-56,7%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS3 | csharp | 1 | 141.913 | 61.466 | -56,7% | 17,0→11,0 | 15,3→12,0 | 1/1 vs 1/1 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 1 | 0,433 | -56,7% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 16,0, FastExec 10,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 18,10 | 21,00 | 1,16 | +16,0% |
| Tempo no modelo (s) | 15,10 | 8,70 | 0,58 | -42,4% |
| Tempo fora do modelo: ferramentas (s) | 3,00 | 12,00 | 4,00 | +300,0% |
| CPU (s) | 4,28 | 9,11 | 2,13 | +112,9% |
| RAM de pico (MB) | 661,00 | 730,00 | 1,10 | +10,4% |
| Custo (US$, sem cache) | 0,05 | 0,02 | 0,44 | -56,1% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 1 | 1 | 1 (100%) | 0,00 |
| fastexec | 1 | 1 | 1 (100%) | 0,00 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| CS3 | fastexec | 1 | 2,0 | 0,0 | 0,0 | 11,0 | 61.466 | 1/1 |
| CS3 | native | 1 | 0,0 | 0,0 | 4,0 | 17,0 | 141.913 | 1/1 |

## 7. Roslyn e servidores de linguagem: usado e exato?

Conta as chamadas reais a `code_graph` (action callers) do braço FastExec e de onde veio cada resposta: **exata** (servidor de linguagem), **por nome** (índice) ou **LSP indisponível** (o servidor foi pedido e não respondeu; o motivo aparece abaixo). Sem isto, uma resposta por nome parece exata.

```
runs FastExec: 1 | usaram callers: 1 (100%) | chamadas: 1
respostas: exatas=1  por nome=0  lsp indisponivel=0
```

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

