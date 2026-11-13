# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `10845bf57059` | 2.1.322 (Claude Code)
- Execuções: 2 (2 válidas), pares válidos: 1. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 2,430 (+143,0%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 2,430 (+143,0%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS3 | csharp | 1 | 68.866 | 167.312 | +143,0% | 12,0→15,0 | 12,3→44,4 | 1/1 vs 1/1 | 0,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 1 | 2,430 | +143,0% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 11,0, FastExec 14,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 14,90 | 25,60 | 1,72 | +71,8% |
| Tempo no modelo (s) | 12,40 | 13,70 | 1,10 | +10,5% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 12,00 | 6,00 | +500,0% |
| CPU (s) | 2,67 | 13,58 | 5,09 | +408,6% |
| RAM de pico (MB) | 518,00 | 682,00 | 1,32 | +31,7% |
| Custo (US$, sem cache) | 0,02 | 0,05 | 2,25 | +125,5% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 1 | 1 | 1 (100%) | 0,00 |
| fastexec | 1 | 1 | 1 (100%) | 1,00 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| CS3 | fastexec | 1 | 1,0 | 1,0 | 0,0 | 15,0 | 167.312 | 1/1 |
| CS3 | native | 1 | 0,0 | 0,0 | 1,0 | 12,0 | 68.866 | 1/1 |

## 7. Roslyn e servidores de linguagem: usado e exato?

Conta as chamadas reais a `code_graph` (action callers) do braço FastExec e de onde veio cada resposta: **exata** (servidor de linguagem), **por nome** (índice) ou **LSP indisponível** (o servidor foi pedido e não respondeu; o motivo aparece abaixo). Sem isto, uma resposta por nome parece exata.

```
runs FastExec: 1 | usaram callers: 1 (100%) | chamadas: 1
respostas: exatas=0  por nome=1  lsp indisponivel=0
```

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

