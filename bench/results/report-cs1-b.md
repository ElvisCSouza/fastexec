# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `1d4d84611291` | 2.1.322 (Claude Code)
- Execuções: 2 (2 válidas), pares válidos: 1. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 1,219 (+21,9%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 1,219 (+21,9%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS1 | csharp | 1 | 10.773 | 13.133 | +21,9% | 2,0→3,0 | 1,0→1,9 | 1/1 vs 1/1 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 1 | 1,219 | +21,9% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 1,0, FastExec 2,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 5,80 | 3,40 | 0,59 | -41,4% |
| Tempo no modelo (s) | 5,20 | 2,40 | 0,46 | -53,8% |
| Tempo fora do modelo: ferramentas (s) | 1,00 | 1,00 | 1,00 | 0,0% |
| CPU (s) | 0,59 | 0,92 | 1,56 | +55,9% |
| RAM de pico (MB) | 459,00 | 483,00 | 1,05 | +5,2% |
| Custo (US$, sem cache) | 0,00 | 0,00 | 1,28 | +27,9% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 1 | 1 | 1 (100%) | 0,00 |
| fastexec | 1 | 1 | 1 (100%) | 0,00 |

## 7. Roslyn e servidores de linguagem: usado e exato?

Conta as chamadas reais a `code_graph` (action callers) do braço FastExec e de onde veio cada resposta: **exata** (servidor de linguagem), **por nome** (índice) ou **LSP indisponível** (o servidor foi pedido e não respondeu; o motivo aparece abaixo). Sem isto, uma resposta por nome parece exata.

```
runs FastExec: 1 | usaram callers: 0 (0%) | chamadas: 0
respostas: exatas=0  por nome=0  lsp indisponivel=0
```

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

