# Spec: exec

## Responsabilidade
Executa um comando no host escolhido e devolve a saída. Antes de executar, a remediação automática pode trocar uma
espera longa por um job em segundo plano, sem mudar o comando, o cwd ou o host.

## Fluxo
1. `HandleExec` resolve host e cwd e calcula o rótulo (`stripHeredocs`).
2. `tryRemediateSleep`: pausa pura `<= 15 s` roda em Go; `> 15 s` vira job (`promoteSleepJob`).
3. `tryPromotePingWait`: ping silencioso de loopback com mais de 15 probes vira job pelo mesmo caminho.
4. Demais casos seguem a execução normal (guardas de sleep, saída limitada).

Promoção de ping só acontece quando **todos** os itens valem:
- o comando começa com `ping` (ou `ping.exe`), ou é uma linha de shell (`cmd /c`, `bash -c`, `powershell -Command`);
- destino é um token exato: `localhost`, `127.0.0.1` ou `::1`;
- contagem: `-n N` / `/n N` (Windows) ou `-c N` (Linux) com `N > 15`, e nenhuma outra flag;
- saída suprimida no fim do segmento do ping: `>nul`, `>/dev/null`, `>$null`, `| Out-Null`, com `2>&1` opcional;
- depois do supressor só fim de linha ou um operador de cadeia (`&&`, `||`, `&`, `;`).

## Contratos
- Entrada: `ExecArgs` (sem campos novos para a promoção).
- Saída de promoção: aviso `[FastExec Auto-Remediation] A ping wait of N probes ... promoted to a background job`,
  seguido da saída do `start` do job com o handle e o próximo passo `job(action='wait', job_id=...)`.
- Um ping argv direto (sem shell) nunca é promovido: não há supressor de saída.

## Invariantes
- O comando promovido é o mesmo que o usuário enviou (no Linux, só o prefixo `cd` que o `start` sempre adiciona).
- Nenhum retry automático de comando mutável: a promoção é um único `start`.
- Falha no `start` é erro devolvido, nunca sucesso falso.
- Ping diagnóstico (saída visível, contagem curta, host remoto, flags extras, `-t`) não é tocado.

## Decisões
- Regra estreita: um falso negativo deixa a espera bloqueante como antes; um falso positivo esconderia um diagnóstico.
- O detector é separado de `ping.go` (diagnóstico de rede), que não muda.
- Teste usa o seam `promoteSleepJob` e o seam `dispatch` (job) com fake; nada toca host real.

## Limites
- Não há agendador distribuído, cron, nem protocolo de evento novo.
- Padrões fora da lista (ex.: `ping` dentro de subshell, variáveis, `for`) ficam inline.

## Mapa
- `internal/tools/exec.go`: `HandleExec`, `tryRemediateSleep`.
- `internal/tools/ping_wait.go`: `tryPromotePingWait`, `parsePingWait`, `pingWaitScript`.
- `internal/tools/ping_promotion_test.go`: positivos, negativos, argv e payload Linux.
- `internal/tools/sleep_promotion_test.go`: pausas e ping de diagnóstico inalterado.
- Ver também: [spec-job.md](spec-job.md), [tools.md](tools.md).
