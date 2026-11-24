# Spec: job

## Responsabilidade
Inicia, consulta, espera, acompanha e para processos em segundo plano em um host. Cada job tem um handle (`job_id`).
Não há API nova: `start`, `status`, `wait`, `tail`, `stop`, `list` continuam sendo as ações.

## Fluxo
1. `HandleJob` resolve o host e traduz caminhos Git Bash em Windows.
2. Host Windows local: Job Object nomeado (`handleLocalJob`).
3. Host Linux (local ou remoto): `systemd-run --user` com o comando em base64, via `dispatch` (seam de `transport.Dispatch`).
4. `start` com `job_id` já existente: replay do mesmo pedido, ou conflito. Nada novo começa.
5. `wait` bloqueia no máximo 45 s por chamada; quem quer esperar mais chama de novo.

## Contratos
- `start`: exige `command` (ou `argv`); devolve handle, ou a saída final se o processo terminar dentro do orçamento.
- `wait`: devolve estado e rc quando termina; senão, estado atual e o próximo passo.
- `tail`: cursor por posição de log; cada chamada devolve a partir do último ponto lido.
- `status`: estado do unit/job sem bloquear.
- Nomes de parâmetro: `target`→`host`; `dir`/`workdir`/`directory`→`cwd`; `id`→`job_id`. Sem alias para timeout ou
  wait. Nome desconhecido é recusado (nada roda) com a lista válida para a ação pedida e o próximo passo.

## Invariantes
- Um `start` nunca é repetido automaticamente.
- `job_id` segue o padrão de nome seguro (`linuxJobIDRegex`), sem barras nem espaços.
- Estado vem do host (systemd ou Job Object), não de memória do processo que chamou.
- Alias só quando há um único destino: `timeout_ms` não vira nada, porque `wait` não tem prazo e traduzir mudaria o sentido.

## Decisões
- Espera por processo: `job(action='wait')` no handle, sem `wait_for` genérico (não há prova de necessidade e cresceria `tools/list`).
- Espera por arquivo ou porta: um job no host faz a checagem curta e termina quando o arquivo existe ou a porta responde; quem espera é o job.
- Espera por log: cursor do `tail`, sem campo novo no schema.
- `dispatch` é um seam de teste; em produção é `transport.Dispatch`.
- Recusa de parâmetro desconhecido lista só o que a ação lê (`jobActionParams` em `internal/mcp/argnorm.go`), para o
  modelo não tentar parâmetros de outra ação. O schema de `job` não mudou.

## Limites
- Sem agendador distribuído, cron ou evento novo.
- `wait` tem teto de 45 s por chamada.
- Verificação real de systemd só com host de integração (`FASTEXEC_TEST_HOST`); a suíte padrão usa fake.

## Mapa
- `internal/tools/job.go`: `HandleJob`, `handleJob`, `dispatch`, ramo Linux de `start`.
- `internal/tools/job_linux_state.go`, `job_linux_ops.go`: estado e operações systemd.
- `internal/tools/sleep_promotion_test.go`: job Windows real e promoção com fake.
- `internal/tools/ping_promotion_test.go`: payload Linux com fake de `dispatch`.
- `internal/mcp/job_args_test.go`: recusa de `timeout_ms` por ação, aliases `dir`/`id`, `jobActionParams` contra `JobArgs`.
- Ver também: [spec-exec.md](spec-exec.md), [tools.md](tools.md).
