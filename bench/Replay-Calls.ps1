# Replay sem modelo: refaz no binario atual a chamada code_graph callers que um transcript gravou e compara o
# tamanho da resposta com o gravado. Mede formato (agrupamento, notas) sem gastar uma execucao paga.
# Uso: pwsh -File .\Replay-Calls.ps1 -Tag cs1-c [-Fx <fastexec.exe>]
param([string]$Tag, [string]$Fx = (Join-Path $PSScriptRoot '..\..\fastexec-private\bin\fastexec.exe'))
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib\Bench.ps1')
. (Join-Path $PSScriptRoot 'tools\csoracle\CsOracle.ps1')
$Cfg = Get-Content (Join-Path $PSScriptRoot 'bench.config.json') -Raw | ConvertFrom-Json
$Fx = (Resolve-Path $Fx).Path

# 1) o que o transcript gravou: cada code_graph callers e o tamanho do resultado dele
$calls = @()
Get-ChildItem (Join-Path $Cfg.work_dir "runs\$Tag") -Recurse -Filter stream.jsonl | ForEach-Object {
  $pending = @{}
  foreach ($l in Get-Content $_.FullName) {
    if ($l -notmatch '^\{') { continue }
    $j = $l | ConvertFrom-Json
    if ($j.type -eq 'assistant') {
      foreach ($c in $j.message.content) {
        if ($c.type -eq 'tool_use' -and $c.name -like '*code_graph' -and $c.input.action -eq 'callers') { $pending[$c.id] = $c.input }
      }
    } elseif ($j.type -eq 'user') {
      foreach ($c in $j.message.content) {
        if ($c.type -eq 'tool_result' -and $pending.ContainsKey($c.tool_use_id)) {
          $t = if ($c.content -is [string]) { $c.content } else { ($c.content | ForEach-Object { $_.text }) -join '' }
          $calls += [pscustomobject]@{ run = $_.Directory.Name; input = $pending[$c.tool_use_id]; old = $t.Length }
        }
      }
    }
  }
}
if (-not $calls) { 'nenhuma chamada callers gravada em ' + $Tag; exit 0 }

# 2) refaz no binario atual
$p = Start-BenchDaemon $Cfg $Fx (Join-Path $env:TEMP 'replay-daemon.json')
try {
  foreach ($c in $calls) {
    $repo = Split-Path -Leaf $c.input.repo_path
    $dir = Join-Path $Cfg.work_dir "_replay\$repo"
    if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
    $null = New-Item -ItemType Directory -Force (Split-Path $dir -Parent)
    $null = Copy-CsRepo -Src (Join-Path $Cfg.work_dir "fixtures\$repo") -Dst $dir
    [void](Invoke-BenchTool $Cfg 'code_graph' @{ action = 'index'; repo_path = $dir })
    $args2 = @{}; foreach ($k in $c.input.PSObject.Properties.Name) { $args2[$k] = $c.input.$k }
    $args2.repo_path = $dir
    $txt = (Invoke-BenchTool $Cfg 'code_graph' $args2 | ConvertFrom-Json).result.content[0].text
    $files = ([regex]::Matches($txt, '(?m)^- (\S+?)[ :]')).Count
    '{0}  {1}  antes={2} chars  agora={3} chars  ({4:P0})  linhas "- ": {5}' -f $c.run, $c.input.symbol, $c.old, $txt.Length, ($txt.Length / [double]$c.old), $files
    $txt -split "`n" | Select-Object -First 14 | ForEach-Object { '   ' + $_ }
    Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
  }
} finally { Stop-BenchDaemon $Cfg $Fx $p }
