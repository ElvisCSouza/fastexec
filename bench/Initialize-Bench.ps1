# Prepara o ambiente uma vez: compila o medidor (runjob), baixa as fixtures publicas e congela o binario do FastExec.
# Uso: pwsh -NoProfile -File .\Initialize-Bench.ps1 [-FastExec <caminho>] [-Refresh]
param([string]$FastExec = '', [switch]$Refresh)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib\Bench.ps1')
$cfg = Get-BenchConfig
$work = $cfg.work_dir
New-Item -ItemType Directory -Force $work, "$work\fixtures", "$work\bin", "$work\runs" | Out-Null

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
