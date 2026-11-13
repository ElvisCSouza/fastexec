# Metrica de uso do Roslyn/grafo num ciclo: lê os transcritos do braço FastExec e conta, por execução,
# se chamou code_graph, e se a resposta veio exata (csharp-ls/gopls) ou por nome.
# Uso: pwsh -File .\Lsp-Usage.ps1 -Tag cs-lsp3 [-Stratum CS]
param([Parameter(Mandatory)][string]$Tag, [string]$Stratum = '')
$root = Join-Path ((Get-Content (Join-Path $PSScriptRoot 'bench.config.json') -Raw | ConvertFrom-Json).work_dir) "runs\$Tag"
$runs = 0; $withGraph = 0; $calls = 0; $exact = 0; $byName = 0; $unavail = 0; $reasons = @{}
foreach ($d in Get-ChildItem $root -Directory -Filter '*-fastexec-r*' -ErrorAction SilentlyContinue) {
  if ($Stratum -and $d.Name -notlike "$Stratum*") { continue }
  $runs++; $ids = @{}; $used = $false
  foreach ($l in Get-Content (Join-Path $d.FullName 'stream.jsonl')) {
    if (-not $l.StartsWith('{')) { continue }
    try { $o = $l | ConvertFrom-Json -Depth 30 } catch { continue }
    foreach ($c in @($o.message.content)) {
      if ($c.type -eq 'tool_use' -and $c.name -like '*code_graph' -and $c.input.action -eq 'callers') { $ids[$c.id] = 1; $calls++; $used = $true }
      if ($c.type -eq 'tool_result' -and $ids.ContainsKey($c.tool_use_id)) {
        $t = if ($c.content -is [string]) { $c.content } else { ($c.content | ForEach-Object { $_.text }) -join '' }
        if ($t -match '\(exact, ') { $exact++ } elseif ($t -match 'lsp unavailable \(([^)]{0,90})') { $unavail++; $reasons[$Matches[1]]++ } else { $byName++ }
      }
    }
  }
  if ($used) { $withGraph++ }
}
"runs FastExec: $runs | usaram callers: $withGraph ($([math]::Round(100 * $withGraph / [math]::Max(1, $runs)))%) | chamadas: $calls"
"respostas: exatas=$exact  por nome=$byName  lsp indisponivel=$unavail"
$reasons.GetEnumerator() | ForEach-Object { "  $($_.Value)x $($_.Key)" }
