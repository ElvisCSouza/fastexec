# Compara o braco fastexec de uma rodada com a MEDIA historica do nativo (o nativo nao muda, entao nao se roda de novo).
# Uso: pwsh -NoProfile -File .\Compare-Baseline.ps1 -Tag cs3-v5 [-Exclude cs3-v5]
# Baseline = media de tokens_total das execucoes nativas validas e corretas, de todos os results\*.jsonl (menos a propria rodada).
param([Parameter(Mandatory)][string]$Tag)
$ErrorActionPreference = 'Stop'
$rows = Get-ChildItem (Join-Path $PSScriptRoot 'results') -Filter *.jsonl | ForEach-Object { Get-Content $_.FullName | ForEach-Object { try { $_ | ConvertFrom-Json } catch {} } }
$fx = $rows | Where-Object { $_.tag -eq $Tag -and $_.arm -eq 'fastexec' }
if (-not $fx) { throw "sem linhas fastexec para a tag $Tag" }
foreach ($r in $fx) {
  $nat = @($rows | Where-Object { $_.arm -eq 'native' -and $_.task -eq $r.task -and $_.valid -and $_.correct } | ForEach-Object { [double]$_.tokens_total })
  if (-not $nat.Count) { "{0}: sem baseline nativo" -f $r.task; continue }
  $mean = ($nat | Measure-Object -Average).Average
  $sorted = $nat | Sort-Object; $med = $sorted[[int][Math]::Floor(($sorted.Count - 1) / 2)]
  "{0} rep{1}: fastexec={2:N0} tokens | nativo media={3:N0} (n={4}, mediana={5:N0}) | vs media {6:P1} | vs mediana {7:P1} | acerto={8} turnos={9} KB={10}" -f `
    $r.task, $r.rep, $r.tokens_total, $mean, $nat.Count, $med, ($r.tokens_total / $mean - 1), ($r.tokens_total / $med - 1), $r.correct, $r.turns, $r.result_kb
}
