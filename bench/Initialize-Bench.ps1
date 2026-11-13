# Prepara o ambiente uma vez: compila o medidor (runjob), baixa as fixtures publicas e congela o binario do FastExec.
# Uso: pwsh -NoProfile -File .\Initialize-Bench.ps1 [-FastExec <caminho>] [-Refresh]
# -FreezeOnly: so congela o binario atual (o que muda a cada deploy). Runjob, fixtures e restore do .NET ja existem; refazer
# isso a cada tarefa custava ~30 s para nada.
param([string]$FastExec = '', [switch]$Refresh, [switch]$FreezeOnly)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib\Bench.ps1')
$cfg = Get-BenchConfig
$work = $cfg.work_dir
New-Item -ItemType Directory -Force $work, "$work\fixtures", "$work\bin", "$work\runs" | Out-Null

if (-not $FreezeOnly) {
# 1) medidor: roda um comando dentro de um Job Object e devolve CPU, RAM de pico e tempo da arvore inteira de processos
$rj = Get-RunJobPath
if ($Refresh -or -not (Test-Path $rj)) {
  Push-Location (Split-Path $rj -Parent); try { go build -o runjob.exe . } finally { Pop-Location }
}
"runjob: $rj"

# 2) fixtures publicas, ref fixada (reprodutivel)
foreach ($p in $cfg.repos.PSObject.Properties) {
  $dest = Join-Path "$work\fixtures" $p.Name
  if ($Refresh -and (Test-Path $dest)) { Remove-Item -Recurse -Force $dest }
  if (-not (Test-Path $dest)) { git clone --quiet --depth 1 --branch $p.Value.ref $p.Value.url $dest }
  "fixture $($p.Name): $dest @ $(git -C $dest describe --tags --always)"
}

# 2b) fixtures GERADAS (nao vem de git clone): o corpus sintetico de Markdown do estrato 'docs'.
#     New-DocsCorpus e' deterministico e apaga/reescreve o destino, entao rodar sempre deixa a
#     fixture no estado esperado sem precisar de -Refresh; Test-DocsFacts confere o resultado.
if ($cfg.generated) {
  . (Join-Path $PSScriptRoot 'tools\docs\New-DocsCorpus.ps1')
  foreach ($p in $cfg.generated.PSObject.Properties) {
    $dest = Join-Path "$work\fixtures" $p.Value
    $r = New-DocsCorpus -Dest $dest
    $null = Test-DocsFacts -Corpus $dest
    "fixture gerada $($p.Name): $dest ($($r.mds) MDs, $($r.files) arquivos)"
  }
}

# 2c) .NET: um restore por projeto alvo, uma vez, para que o build das tarefas rode offline depois.
#     O restore roda numa COPIA descartavel: Copy-CsRepo reescreve o global.json (os pins antigos
#     dos repos nao existem nesta maquina) e a fixture compartilhada fica intacta. O que precisa
#     persistir e' o cache NuGet (dotnet.nuget_packages), nao a copia.
if ($cfg.dotnet -and $cfg.dotnet.projects) {
  . (Join-Path $PSScriptRoot 'tools\csoracle\CsOracle.ps1')
  foreach ($p in $cfg.dotnet.projects.PSObject.Properties) {
    $repo = $p.Name; $spec = $p.Value
    $src = Join-Path "$work\fixtures" $repo
    if (-not (Test-Path $src)) { "restore ${repo}: fixture ausente, pulado"; continue }
    if (-not (Test-Path (Join-Path $src $spec.project))) { "restore ${repo}: $($spec.project) ausente, pulado"; continue }
    $tmp = "$work\_restore\$repo"
    if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    New-Item -ItemType Directory -Force (Split-Path $tmp -Parent) | Out-Null
    $null = Copy-CsRepo -Src $src -Dst $tmp
    $r = Invoke-CsDotnet -CsArgs @('restore', (Join-Path $tmp $spec.project)) -WorkDir $tmp -TimeoutSec 900
    $ok = (-not $r.timeout) -and ($r.exit -eq 0)
    "restore ${repo}: $(if ($r.timeout) { 'TIMEOUT' } elseif ($ok) { 'ok' } else { "rc=$($r.exit)" })"
    if (-not $ok) { ($r.out -split "`n" | Where-Object { $_.Trim() } | Select-Object -Last 6) | ForEach-Object { "  $_" } }
    Remove-Item -Recurse -Force $tmp
  }
}

} # fim do -not $FreezeOnly

# 3) congela o FastExec sob teste: um deploy no meio da rodada nao pode trocar o binario medido
if (-not $FastExec) { $FastExec = (Resolve-Path (Join-Path $script:BenchRoot $cfg.fastexec_exe)).Path }
$sha = (Get-FileHash $FastExec -Algorithm SHA256).Hash.Substring(0, 12).ToLower()
$frozen = "$work\bin\fastexec-$sha.exe"
if (-not (Test-Path $frozen)) { Copy-Item $FastExec $frozen }
"fastexec congelado: $frozen"
& $frozen version 2>&1 | Select-Object -First 2
Set-Content "$work\bin\current.txt" $frozen -Encoding UTF8

$p = Test-BenchPreflight $cfg
if ($p) { "AVISOS:"; $p | ForEach-Object { " - $_" } } else { 'preflight: ok' }
