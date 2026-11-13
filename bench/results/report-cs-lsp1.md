# Benchmark: Claude nativo vs Claude com FastExec

- Modelo: `deepseek-ai/DeepSeek-V4.1-Flash` (DeepSeek V4.1 Flash (Together)) | cache de prompt: automático do provedor | FastExec `a657b819f601` | 2.1.322 (Claude Code)
- Execuções: 48 (48 válidas), pares válidos: 24. Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0.

## 1. Economia de tokens (foco)

**Média geométrica da razão FastExec/nativo: 0,967 (-3,3%)**, IC95% [0,792; 1,196], n = 24 pares.
Só pares em que **os dois acertaram**: 0,967 (-3,3%), n = 24. (Economia que vem de errar mais cedo não conta.)

| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |
|---|---|---|---|---|---|---|---|---|---|
| CS1 | csharp | 3 | 10.860 | 8.368 | -22,9% | 2,0→3,0 | 1,0→1,4 | 3/3 vs 3/3 | 0,0→0,0 |
| CS2 | csharp | 3 | 27.349 | 25.637 | -6,3% | 4,0→4,0 | 9,1→9,7 | 3/3 vs 3/3 | 0,0→0,0 |
| CS3 | csharp | 3 | 85.679 | 81.890 | -4,4% | 13,0→12,0 | 11,4→14,9 | 3/3 vs 3/3 | 0,0→0,0 |
| CS4 | csharp | 3 | 98.624 | 100.228 | +1,6% | 13,0→14,0 | 14,0→17,7 | 3/3 vs 3/3 | 0,0→0,0 |
| CS5 | csharp | 3 | 11.812 | 8.856 | -25,0% | 3,0→3,0 | 1,0→1,8 | 3/3 vs 3/3 | 0,0→0,0 |
| CS6 | csharp | 3 | 46.100 | 37.951 | -17,7% | 7,0→7,0 | 9,9→13,7 | 3/3 vs 3/3 | 0,0→0,0 |
| CS7 | csharp | 3 | 20.044 | 21.562 | +7,6% | 5,0→6,0 | 6,0→7,3 | 3/3 vs 3/3 | 0,0→0,0 |
| CS8 | csharp | 3 | 155.291 | 89.557 | -42,3% | 17,0→13,0 | 14,7→22,4 | 3/3 vs 3/3 | 1,0→1,0 |

### Por estrato

| Estrato | n | razão tokens (média geom.) | Δ |
|---|---|---|---|
| csharp | 24 | 0,967 | -3,3% |

## 2. De onde vem a diferença

- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo NaN tokens, FastExec NaN tokens.
- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima.
- Chamadas de ferramenta por execução (mediana): nativo 5,5, FastExec 6,0.

## 3. Extras: latência, CPU, RAM e custo

Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor.

| Métrica | nativo | FastExec | razão | Δ |
|---|---|---|---|---|
| Tempo total (s) | 5,95 | 6,15 | 1,03 | +3,4% |
| Tempo no modelo (s) | 4,35 | 4,20 | 0,97 | -3,4% |
| Tempo fora do modelo: ferramentas (s) | 2,00 | 2,00 | 1,00 | 0,0% |
| CPU (s) | 1,80 | 1,46 | 0,81 | -19,2% |
| RAM de pico (MB) | 495,50 | 525,50 | 1,06 | +6,1% |
| Custo (US$, sem cache) | 0,01 | 0,01 | 1,16 | +16,0% |

## 4. Qualidade (não vale economizar errando)

| Braço | execuções | válidas | acertos | erros de ferramenta (média) |
|---|---|---|---|---|
| native | 24 | 24 | 24 (100%) | 0,21 |
| fastexec | 24 | 24 | 24 (100%) | 0,25 |

## 6. Uso do grafo (code_graph / code_outline)

Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token.

| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |
|---|---|---|---|---|---|---|---|---|
| CS1 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 2,7 | 8.368 | 3/3 |
| CS1 | native | 3 | 0,0 | 0,0 | 1,0 | 2,0 | 10.860 | 3/3 |
| CS2 | fastexec | 3 | 0,0 | 0,3 | 0,0 | 5,0 | 25.637 | 3/3 |
| CS2 | native | 3 | 0,0 | 0,0 | 1,7 | 3,3 | 27.349 | 3/3 |
| CS3 | fastexec | 3 | 1,0 | 1,0 | 0,0 | 14,0 | 81.890 | 3/3 |
| CS3 | native | 3 | 0,0 | 0,0 | 1,7 | 13,3 | 85.679 | 3/3 |
| CS4 | fastexec | 3 | 0,0 | 0,7 | 0,0 | 14,3 | 100.228 | 3/3 |
| CS4 | native | 3 | 0,0 | 0,0 | 2,3 | 12,7 | 98.624 | 3/3 |
| CS5 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 3,0 | 8.856 | 3/3 |
| CS5 | native | 3 | 0,0 | 0,0 | 1,7 | 3,3 | 11.812 | 3/3 |
| CS6 | fastexec | 3 | 0,0 | 1,0 | 0,0 | 8,0 | 37.951 | 3/3 |
| CS6 | native | 3 | 0,0 | 0,0 | 1,3 | 7,7 | 46.100 | 3/3 |
| CS7 | fastexec | 3 | 0,7 | 1,7 | 0,0 | 6,3 | 21.562 | 3/3 |
| CS7 | native | 3 | 0,0 | 0,0 | 1,7 | 6,0 | 20.044 | 3/3 |
| CS8 | fastexec | 3 | 0,3 | 1,0 | 0,0 | 13,7 | 89.557 | 3/3 |
| CS8 | native | 3 | 0,0 | 0,0 | 2,0 | 16,3 | 155.291 | 3/3 |

## 7. Roslyn e servidores de linguagem: usado e exato?

Conta as chamadas reais a `code_graph` (action callers) do braço FastExec e de onde veio cada resposta: **exata** (servidor de linguagem), **por nome** (índice) ou **LSP indisponível** (o servidor foi pedido e não respondeu; o motivo aparece abaixo). Sem isto, uma resposta por nome parece exata.

```
runs FastExec: 24 | usaram callers: 6 (25%) | chamadas: 6
respostas: exatas=0  por nome=1  lsp indisponivel=5
  2x global.json pins SDK 7.0.102; installed: 8.0.428 (set rollForward or install 7.0.102
  3x restore needed: run dotnet restore (src/Serilog/Serilog.csproj
```

## Como ler

- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec).
- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe.
- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com `-Cache` para ver.

