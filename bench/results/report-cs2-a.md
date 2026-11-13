# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `13602360c229` | 2.1.322 (Claude Code)
- Execuções: 2 (2 válidas), pares válidos: 1. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,814 (-18,6%)**, IC95% [NaN; NaN], n = 1 pares.
Só pares em que **os dois acertaram**: 0,814 (-18,6%), n = 1. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS2 | csharp | 1 | 24.417 | 19.866 | -18,6% | 5,0→3,0 | 10,3→11,1 | 1/1 vs 1/1 | 0,0→0,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 1 | 0,814 | -18,6% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 4,0, FastExec 2,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 7,60 | 11,30 | 1,49 | +48,7% |
| Tempo no modelo (s) | 6,40 | 9,70 | 1,52 | +51,6% |
| Tempo fora do modelo: ferramentas (s) | 1,00 | 2,00 | 2,00 | +100,0% |
| CPU (s) | 1,52 | 1,33 | 0,88 | -12,5% |
| RAM de pico (MB) | 527,00 | 548,00 | 1,04 | +4,0% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 0,83 | -16,8% |

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

