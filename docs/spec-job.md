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

## Invariantes
- Um `start` nunca é repetido automaticamente.
- `job_id` segue o padrão de nome seguro (`linuxJobIDRegex`), sem barras nem espaços.
- Estado vem do host (systemd ou Job Object), não de memória do processo que chamou.

## Decisões
- Espera por processo: `job(action='wait')` no handle, sem `wait_for` genérico (não há prova de necessidade e cresceria `tools/list`).
- Espera por arquivo ou porta: um job no host faz a checagem curta e termina quando o arquivo existe ou a porta responde; quem espera é o job.
- Espera por log: cursor do `tail`, sem campo novo no schema.
- `dispatch` é um seam de teste; em produção é `transport.Dispatch`.

## Limites
- Sem agendador distribuído, cron ou evento novo.
- `wait` tem teto de 45 s por chamada.
- Verificação real de systemd só com host de integração (`FASTEXEC_TEST_HOST`); a suíte padrão usa fake.

## Mapa
- `internal/tools/job.go`: `HandleJob`, `handleJob`, `dispatch`, ramo Linux de `start`.
- `internal/tools/job_linux_state.go`, `job_linux_ops.go`: estado e operações systemd.
- `internal/tools/sleep_promotion_test.go`: job Windows real e promoção com fake.
- `internal/tools/ping_promotion_test.go`: payload Linux com fake de `dispatch`.
- Ver também: [spec-exec.md](spec-exec.md), [tools.md](tools.md).
