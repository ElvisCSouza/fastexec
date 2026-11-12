# Suporte da suite SWE-LITE: cache de historia dos repos, preparo do diretorio da tarefa e oraculo `go test`.
# Usado por tools\swelite\Select-Commits.ps1 (escolha/verificacao dos commits) e por tasks\swelite.ps1 (as tarefas).
#
# Dois tipos de copia do repositorio:
#   - FIXTURE (work_dir\fixtures\<repo>): clone raso da ref fixa, feito pelo Initialize-Bench.ps1; so tem 1 commit.
#   - CACHE (work_dir\swelite-src\<repo>): clone "blobless" (--filter=blob:none) da MESMA url/ref, com historia completa.
#     A historia e necessaria para voltar ao commit pai. Os blobs faltantes sao baixados sob demanda no checkout, entao
#     so o delta entre a ref e o pai de cada tarefa trafega na rede (uma vez; depois fica em cache).
# O diretorio da tarefa e sempre uma COPIA do cache (nunca o cache): a tarefa pode sujar a copia a vontade.

$script:SweliteBench = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent   # bench\
$script:SweliteCommitsJson = Join-Path $script:SweliteBench 'tasks\swelite.commits.json'

function Get-SwelitePlan([string]$Json = $script:SweliteCommitsJson) {
  if (-not (Test-Path $Json)) { throw "swelite: $Json ausente (rode tools\swelite\Select-Commits.ps1)" }
  $o = Get-Content $Json -Raw | ConvertFrom-Json
  $h = [ordered]@{}; $i = 0
  foreach ($c in $o.commits) { $i++; $h["SL$i"] = $c }
  return $h
}

function Invoke-SweliteGit([string]$Dir, [string[]]$GitArgs) {
  $o = & git -C $Dir @GitArgs 2>&1
  return [pscustomobject]@{ exit = $LASTEXITCODE; out = @($o) }
}

# Garante o cache de historia (work_dir\swelite-src\<repo>) materializado na ref fixa do bench.config.json.
function Get-SweliteSource($Cfg, [string]$Repo, [switch]$Quiet) {
  $src = Join-Path $Cfg.work_dir "swelite-src\$Repo"
  if (Test-Path (Join-Path $src '.git')) { return $src }
  $url = $Cfg.repos.$Repo.url; $ref = $Cfg.repos.$Repo.ref
  if (-not $url) { throw "swelite: repo '$Repo' nao esta em bench.config.json" }
  New-Item -ItemType Directory -Force (Split-Path $src -Parent) | Out-Null
  if (-not $Quiet) { "swelite: criando cache de historia de $Repo em $src (clone blobless, uma vez)" }
  & git clone --quiet --filter=blob:none --no-checkout $url $src 2>&1 | Out-Null
  if ($LASTEXITCODE) { throw "swelite: falha ao clonar $url" }
  $r = Invoke-SweliteGit $src @('checkout', '--quiet', $ref)
  if ($r.exit) { throw "swelite: falha ao materializar $ref em $src" }
  return $src
}

function Copy-SweliteTree([string]$From, [string]$To) {
  New-Item -ItemType Directory -Force $To | Out-Null
  & robocopy $From $To /MIR /NFL /NDL /NJH /NJS /NP /MT:8 | Out-Null
  if ($LASTEXITCODE -ge 8) { throw "swelite: robocopy falhou ($LASTEXITCODE) copiando $From -> $To" }
  $global:LASTEXITCODE = 0
}

# Diretorio da tarefa: copia do cache no commit PAI, com APENAS o arquivo de teste do commit aplicado.
function New-SweliteTaskDir($Cfg, [string]$Repo, [string]$Dir, [string]$Parent, [string]$Commit, [string]$TestFile) {
  $src = Get-SweliteSource $Cfg $Repo
  Copy-SweliteTree $src $Dir
  $r = Invoke-SweliteGit $Dir @('checkout', '--quiet', '--detach', $Parent)
  if ($r.exit) { throw "swelite: checkout do pai $Parent falhou: $($r.out -join ' ')" }
  $r = Invoke-SweliteGit $Dir @('checkout', '--quiet', $Commit, '--', $TestFile)
  if ($r.exit) { throw "swelite: aplicacao do teste $TestFile falhou: $($r.out -join ' ')" }
  return $Dir
}

# Aplica um arquivo do commit (usado pelo Gold) e devolve o caminho.
function Set-SweliteFromCommit($Cfg, [string]$Dir, [string]$Repo, [string]$Commit, [string]$Path) {
  $r = Invoke-SweliteGit $Dir @('checkout', '--quiet', $Commit, '--', $Path)
  if ($r.exit) { throw "swelite: checkout de $Path em $Commit falhou: $($r.out -join ' ')" }
}

# Roda `go test` de um pacote. -vet=off e obrigatorio: em Go 1.27 o vet quebra em codigo antigo do hugo (unitchecker)
# e o comando sairia com erro mesmo com o teste passando ("ok" impresso). -count=1 evita cache de resultado.
function Invoke-SweliteGoTest([string]$Dir, [string]$Pkg, [string]$TestName, [int]$TimeoutSec = 300) {
  # Pacote raiz vem como '' (ou '.') quando o arquivo de codigo esta na raiz do repo: `./$Pkg/` viraria `.//`.
  $pkgArg = if ([string]::IsNullOrEmpty($Pkg) -or $Pkg -eq '.') { '.' } else { "./$Pkg/" }
  $a = @('test', $pkgArg, '-count=1', '-vet=off', '-timeout', "${TimeoutSec}s")
  if ($TestName) { $a += @('-run', "^$TestName$") }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  Push-Location $Dir
  try { $o = & go $a 2>&1 } finally { Pop-Location }
  return [pscustomobject]@{ exit = $LASTEXITCODE; out = @($o); sec = [math]::Round($sw.Elapsed.TotalSeconds, 1) }
}

function Test-SweliteGo {
  return [bool](Get-Command go -ErrorAction SilentlyContinue)
}

# Caminhos sujos agora (git status --porcelain), normalizados com '/' como o baseline.
function Get-SweliteDirty([string]$Dir) {
  $r = Invoke-SweliteGit $Dir @('status', '--porcelain')
  return @($r.out | Where-Object { $_.Trim() } | ForEach-Object { ($_ -replace '^\s*\S+\s+', '').Trim('"') -replace '\\', '/' })
}

# Assinatura do estado do repositorio logo apos o Prepare: caminhos ja sujos e hash de cada *_test.go do pacote.
function Get-SweliteBaseline([string]$Dir, [string]$Pkg) {
  $dirty = Get-SweliteDirty $Dir
  $t = @{}
  $pkgDir = Join-Path $Dir ($Pkg -replace '/', '\')
  foreach ($f in @(Get-ChildItem $pkgDir -Filter '*_test.go' -File -ErrorAction SilentlyContinue)) { $t["$Pkg/$($f.Name)"] = (Get-FileHash $f.FullName -Algorithm SHA256).Hash }
  return [pscustomobject]@{ dirty = $dirty; tests = $t }
}
