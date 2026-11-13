# Preflight deterministico (sem modelo): prova que o modo exato do code_graph funciona na MESMA copia de trabalho
# que as tarefas C# usam. Falha em segundos, antes de gastar uma execucao paga.
# Uso: pwsh -File .\Test-LspReady.ps1 [-Fx <fastexec.exe>]   (exit 0 = pronto, 1 = nao)
param([string]$Fx = (Join-Path $PSScriptRoot '..\..\fastexec-private\bin\fastexec.exe'))
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib\Bench.ps1')
. (Join-Path $PSScriptRoot 'tools\csoracle\CsOracle.ps1')
$Cfg = Get-Content (Join-Path $PSScriptRoot 'bench.config.json') -Raw | ConvertFrom-Json
$Fx = (Resolve-Path $Fx).Path
$checks = @(
  @{ repo = 'serilog';          symbol = 'LoggerConfiguration.CreateLogger' },
  @{ repo = 'fluentvalidation'; symbol = 'ValidationContext.AddFailure' }
)
$bad = 0
$p = Start-BenchDaemon $Cfg $Fx (Join-Path $env:TEMP 'lsp-ready-daemon.json')
try {
  foreach ($c in $checks) {
    $dir = Join-Path $Cfg.work_dir "_ready\$($c.repo)"
    if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
    $null = New-Item -ItemType Directory -Force (Split-Path $dir -Parent)
    $null = Copy-CsRepo -Src (Join-Path $Cfg.work_dir "fixtures\$($c.repo)") -Dst $dir
    [void](Invoke-BenchTool $Cfg 'code_graph' @{ action = 'index'; repo_path = $dir })
    $t = Get-Date
    $txt = (Invoke-BenchTool $Cfg 'code_graph' @{ action = 'callers'; repo_path = $dir; symbol = $c.symbol; depth = 1 } | ConvertFrom-Json).result.content[0].text
    $sec = [math]::Round(((Get-Date) - $t).TotalSeconds, 1)
    $exact = $txt -match 'exact'
    $why = ($txt -split "`n" | Where-Object { $_ -match 'lsp unavailable|exact' } | Select-Object -First 1)
    '{0,-17} {1,-6} {2}s  {3}' -f $c.repo, $(if ($exact -and $txt -notmatch 'lsp unavailable') { 'OK' } else { 'FALHA' }), $sec, $why
    if (-not ($exact -and $txt -notmatch 'lsp unavailable')) { $bad++ }
    Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
  }
} finally { Stop-BenchDaemon $Cfg $Fx $p }
exit ([int]($bad -gt 0))
