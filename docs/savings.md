# Relatório de economia (painel "FastExec savings") — spec

> Para o chat conversar e escrever diretiva sem abrir o código. Linguagem natural, sem colar código.
> Quem mantém: o desenvolvedor que muda o comportamento, o contrato ou um invariante deste módulo.

## Responsabilidade
Monta, a partir do SQLite local, o relatório de economia de um período (`today`, `week`, `all`) e o painel
HTML que o desenha. Compara dois mundos: o que o modelo **leu** (com FastExec) e o que teria lido **sem**
FastExec. Não mede conteúdo de arquivo nem sai da máquina: só lê `audit` e `saving`.

## Fluxo
1. O painel (`fastexec monitor` / tray) pede o relatório do período; `BuildSavingsReport` abre o banco e chama
   `buildSavingsReport`.
2. `periodClauses` decide a janela (`WHERE`) e o bucket de cada período: hora do dia (24) em `today`, dias (7) em
   `week`, semanas (12, agrupadas no máximo em 11) em `all`. Se `meta.savings_since` existe, a janela começa ali.
3. Totais de leitura vêm de `audit` (`est_tokens_out`); economia vem de `saving` (`bytes ÷ 3,5`), excluindo
   `cat='edit'`.
4. Duas séries por bucket, mesmo tamanho e mesma indexação (mais antigo primeiro): `series` (tokens economizados)
   e `sent_series` (tokens lidos). O front desenha as curvas cumulativas.
5. Eventos recentes (uma linha por chamada, com as partes por categoria) fecham o relatório.

## Contratos
`SavingsReport` (JSON, tudo aditivo):
- Totais: `period`, `calls`, `failed_calls`, `active_ms`, `sent_tokens`, `saved_tokens`, `without_tokens`
  (= `sent_tokens + saved_tokens`), `percent`, `cost_*_usd`, `trips_avoided`, `saved_ms`, `failures_avoided`.
- Séries: `series` (tokens economizados por intervalo) e `sent_series` (tokens lidos por intervalo, `SUM(est_tokens_out)`
  do `audit`). Ambas com o mesmo número de buckets (`n`) e o mesmo significado de índice.
- Rótulos: `series_start` / `series_end` (ex.: `00:00` / `now` em `today`).
- Listas: `cats`, `recent` (com `parts`), `last_id`, `since`, `generated`.

Painel: a linha **With** é o acumulado de `sent_series`; a **Without** é a With mais o acumulado de `series`. O eixo X
termina no último intervalo com dado (não estica o resto do dia vazio). Os dois pontos finais são os totais medidos:
`sent_tokens` e `without_tokens`.

## Invariantes
- `series` e `sent_series` têm sempre o mesmo tamanho `n` do período — o front indexa as duas em paralelo.
- `SUM(sent_series) == sent_tokens` no mesmo período — as duas consultas usam a mesma cláusula de janela.
- `without_tokens == sent_tokens + saved_tokens`; os fins de curva não podem divergir da tabela "Without/With".
- `sent_series` é aditivo: relatório de versão antiga chega sem o campo e o front cai no cálculo anterior.
- Custo é estimativa (`tokens × USD/MTok`), tempo/idas evitadas valem uma ida ao modelo por retentativa evitada.
- O bloco `SAVINGS` do `fastexec stats` soma com a MESMA função dos totais do relatório (`savingsTotals`): em
  `today`, `week` (168h) e `all` os números do CLI e do painel são idênticos campo a campo — não existe uma segunda
  contabilidade. Janelas só do CLI (ex.: 1h, 24h) usam o mesmo corte de `timestamp`.
- Nada de rede; só SQLite local.

## Decisões
- Curva cumulativa por tokens lidos, não por evento de economia — motivo: a curva antiga ficava plana por horas
  onde não houve economia, embora o modelo tivesse lido tokens, e parecia congelada — 2026-11-12.
- Quem anda é o "With"; o "Without" é derivado (With + economia acumulada) — motivo: garante o gap igual ao
  `saved_tokens` medido e mantém os pontos finais iguais aos totais — 2026-11-12.
- Sem TTL/invalidação: o dado só muda quando uma chamada grava; o painel recarrega pelo `last_id`.
- Corte (`cut`): a economia é `antes − depois` só quando o texto encolhe; o registro guarda o detalhe do que ficou de
  fora (linhas e bytes, motivo, filtro, cursor, spill). Abrir o spill com `read_file` é registrado (`cut_recovered`), mas
  isso é acesso, não recuperação total: o bloco "Cut recovery" de `stats --measure` mostra quantos cortes foram abertos e
  os bytes omitidos dos cortes nunca abertos, sem somar os abertos como recuperados. Não altera a economia gravada.
  Segredo mascarado não conta como perda.
  Linhas antigas de corte ficam "desconhecidas", sem reconstrução.

## Limites e dívidas conhecidas
- A série é por bucket de calendário, não por evento: dentro de uma hora a curva é linear entre os extremos do bucket.
- `saving` sem `audit_id` (ex.: `schema`) entra na economia mas não na série de leitura.
- O fim de curva é "último bucket com dado"; se um bucket do meio estiver vazio a linha atravessa reta por ele.

## Mapa
- `internal/telemetry/savings_report.go` — monta o relatório e as duas séries; `BuildSavingsReport` é a porta; `savingsTotals` é a soma compartilhada com o CLI.
- `internal/telemetry/stats.go` — bloco `SAVINGS` do `fastexec stats`, consome `savingsTotals`.
- `internal/telemetry/audit.go` — tabela `audit` (fonte de `est_tokens_out`) e `setupAuditSchema`.
- `internal/telemetry/saving.go` — tabela `saving` (economia por categoria).
- `cli/monitor_ui.html` — desenha o gráfico contrafactual, a tabela Without/With e o texto do painel.
- `cli/monitor.go`, `cli/tray_windows.go` — consumidores do relatório.
- Testes: `internal/telemetry/saving_test.go` (`TestSavingsReport` cobre totais e a soma de `sent_series`).
