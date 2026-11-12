# Roda o benchmark: mesmo modelo, mesmas tarefas, braco "native" (ferramentas nativas do Claude Code) contra "fastexec".
# Exemplos:
#   pwsh -NoProfile -File .\Run-Bench.ps1 -DryRun                                   # valida tarefas e oraculos, sem gastar
#   pwsh -NoProfile -Command "& .\Run-Bench.ps1 -Tasks T0,T2 -Reps 1 -Tag smoke"      # fumaca barata
#   pwsh -NoProfile -Command "& .\Run-Bench.ps1 -Reps 5 -Tag full1"                   # rodada completa
# Resultados: bench\results\<Tag>.jsonl (uma linha por execucao); transcricoes em <work_dir>\runs\<Tag>\...
param([string[]]$Tasks = @(), [string[]]$Arms = @('native', 'fastexec'), [int]$Reps = 0, [string]$Tag = ('run-' + (Get-Date -Format 'yyyyMMdd-HHmm')),
      [double]$MaxRunUsd = 0, [double]$MaxTotalUsd = 0, [switch]$Cache, [string]$Provider = '', [switch]$DryRun)
$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'lib\Bench.ps1')
$cfg = Get-BenchConfig
if ($Reps -le 0) { $Reps = [int]$cfg.defaults.reps }
if ($MaxRunUsd -le 0) { $MaxRunUsd = [double]$cfg.defaults.max_run_usd }
if ($MaxTotalUsd -le 0) { $MaxTotalUsd = [double]$cfg.defaults.max_total_usd }
$work = $cfg.work_dir
$prov = $null
# Regra do Elvis: benchmarks NUNCA usam a sessao/assinatura Claude dele; somente Together AI.
if (-not $Provider) { throw "bloqueado: benchmark sem -Provider usaria a sessao Claude. Use -Provider together-dsv41flash." }
if ($Provider) { $prov = $cfg.providers.$Provider; if (-not $prov) { throw "provedor desconhecido: $Provider (existem: $(($cfg.providers.PSObject.Properties.Name) -join ', '))" }
  if (-not ([Environment]::GetEnvironmentVariable($prov.api_key_env, 'User') -or [Environment]::GetEnvironmentVariable($prov.api_key_env, 'Process'))) { throw "variavel de ambiente $($prov.api_key_env) nao definida" } }
$modelUsed = if ($prov) { $prov.model } else { $cfg.model }
$Fixtures = @{}; foreach ($p in $cfg.repos.PSObject.Properties) { $Fixtures[$p.Name] = Join-Path "$work\fixtures" $p.Name }
# fixtures GERADAS (nao vem de git clone): o Initialize-Bench.ps1 as escreve em <work>\fixtures\<subdir>
if ($cfg.generated) { foreach ($p in $cfg.generated.PSObject.Properties) { $Fixtures[$p.Name] = Join-Path "$work\fixtures" $p.Value } }

# carrega as tarefas (cada arquivo preenche $TaskTable)
$TaskTable = @{}
$script:BenchRoot = $PSScriptRoot
Get-ChildItem (Join-Path $PSScriptRoot 'tasks') -Filter *.ps1 | Sort-Object Name | ForEach-Object { . $_.FullName }
$All = $TaskTable
if (-not $Tasks.Count) { $Tasks = @($All.Keys | Sort-Object) }
foreach ($t in $Tasks) { if (-not $All.ContainsKey($t)) { throw "tarefa desconhecida: $t (existem: $(($All.Keys | Sort-Object) -join ', '))" } }

# ---------------- dry-run: oraculos aceitam o gabarito e rejeitam resposta errada; nada e gasto ----------------
if ($DryRun) {
  $bad = 0
  foreach ($id in $Tasks) {
    $t = $All[$id]; $d = "$work\runs\dry\$id"; if (Test-Path $d) { Remove-Item -Recurse -Force $d }; New-Item -ItemType Directory -Force $d | Out-Null
    foreach ($r in @($t.repos)) { if ($r -and -not (Test-Path $Fixtures[$r])) { "$id : fixture '$r' ausente (rode Initialize-Bench.ps1)"; $bad++; continue } }
    try {
      $null = & $t.Prepare $d; $gold = & $t.Gold; $ok = (& $t.Score $gold).correct
      if (Test-Path $d) { Remove-Item -Recurse -Force $d }; New-Item -ItemType Directory -Force $d | Out-Null
      $null = & $t.Prepare $d; $no = (& $t.Score 'resposta errada').correct
      $flag = if ($ok -and -not $no) { 'OK ' } else { 'FALHA'; $bad++ }
      "{0,-4} {1,-5} gabarito_aceito={2} errada_aceita={3}  {4}" -f $id, $flag, $ok, $no, $t.name
    } catch { "$id : ERRO $($_.Exception.Message)"; $bad++ }
  }
  if ($bad) { "dry-run: $bad problema(s)"; exit 1 } else { 'dry-run: tudo certo'; exit 0 }
}

# ---------------- execucao ----------------
$pre = Test-BenchPreflight $cfg
if ($pre) { $pre | ForEach-Object { "PREFLIGHT: $_" }; throw 'corrija o ambiente antes de rodar' }
$fxFile = Join-Path $work 'bin\current.txt'
if (-not (Test-Path $fxFile)) { throw 'rode .\Initialize-Bench.ps1 antes (congela o binario do FastExec)' }
$Fx = (Get-Content $fxFile -Raw).Trim(); $fxSha = (Get-FileHash $Fx -Algorithm SHA256).Hash.Substring(0, 12).ToLower()
$claudeVer = ((& claude --version 2>&1) | Select-Object -First 1)
$resDir = Join-Path $PSScriptRoot 'results'; New-Item -ItemType Directory -Force $resDir | Out-Null
$resFile = Join-Path $resDir "$Tag.jsonl"
$done = @{}; if (Test-Path $resFile) { foreach ($l in Get-Content $resFile) { if ($l.Trim()) { $o = $l | ConvertFrom-Json; $done["$($o.task)|$($o.arm)|$($o.rep)"] = 1 } } }

$repos = @($Tasks | ForEach-Object { $All[$_].repos } | Where-Object { $_ } | Select-Object -Unique | ForEach-Object { $Fixtures[$_] })
if ($repos.Count -and ($Arms -contains 'fastexec')) { Initialize-BenchGraph $cfg $Fx $repos }   # indice do code_graph aquecido fora da medicao

$spent = 0.0
"Tag=$Tag  modelo=$modelUsed  cache=$([bool]$Cache)  fastexec=$fxSha  claude=$claudeVer  tarefas=$($Tasks -join ',')  reps=$Reps  tetos=run `$$MaxRunUsd / total `$$MaxTotalUsd"
for ($rep = 1; $rep -le $Reps; $rep++) {
  foreach ($id in $Tasks) {
    foreach ($arm in ($Arms | Sort-Object { Get-Random })) {     # ordem aleatoria por tarefa: deriva de carga nao favorece ninguem
      if ($done.ContainsKey("$id|$arm|$rep")) { continue }
      if ($spent -ge $MaxTotalUsd) { "PARADA: gasto `$$([math]::Round($spent, 3)) >= teto `$$MaxTotalUsd"; return }
      $dir = "$work\runs\$Tag\$id-$arm-r$rep"; if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }; New-Item -ItemType Directory -Force $dir | Out-Null
      $prompt = & $All[$id].Prepare $dir
      $r = Invoke-Arm -Cfg $cfg -Arm $arm -Prompt $prompt -Dir $dir -Fx $Fx -MaxUsd $MaxRunUsd -Cache:$Cache -Provider $prov
      $sc = & $All[$id].Score $r.answer; $spent += $r.cost
      $valid = ($r.stop -eq 'success') -and (-not $r.is_error) -and ($arm -ne 'fastexec' -or $r.fx_calls -gt 0 -or $id -eq 'T0')
      $row = [ordered]@{ date = (Get-Date).ToString('s'); tag = $Tag; task = $id; stratum = $All[$id].stratum; rep = $rep; model = $modelUsed; provider = $(if ($prov) { $prov.label } else { 'Anthropic' }); cache = [bool]$Cache; fx_sha = $fxSha; claude = $claudeVer }
      foreach ($p in $r.PSObject.Properties) { if ($p.Name -ne 'answer') { $row[$p.Name] = $p.Value } }
      $row.correct = $sc.correct; $row.precision = $sc.precision; $row.recall = $sc.recall; $row.valid = $valid
      ($row | ConvertTo-Json -Compress -Depth 4) | Add-Content $resFile -Encoding UTF8
      "{0,-4} {1,-8} r{2} | tok={3,7} (in1={4}) turns={5} res={6}KB err={7} cost=`${8:N3} wall={9}s cpu={10}s ram={11}MB ok={12} valid={13}" -f $id, $arm, $rep, $r.tokens_total, $r.first_turn_in, $r.turns, $r.result_kb, $r.tool_errors, $r.cost, $r.wall_s, $r.cpu_s, $r.ram_mb, $sc.correct, $valid
      if ($id -eq 'T0' -and $r.first_turn_in -gt [int]$cfg.baseline_warn_tokens) { "AVISO: primeiro turno com $($r.first_turn_in) tokens (> $($cfg.baseline_warn_tokens)): contexto contaminado (CLAUDE.md, memoria, plugins)?" }
    }
  }
}
"FIM: gasto `$$([math]::Round($spent, 3))  ->  pwsh -File .\Report.ps1 -Tag $Tag"
