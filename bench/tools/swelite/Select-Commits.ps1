# Escolhe e VERIFICA os commits de bug-fix real que viram as tarefas SL1..SL8 da suite SWE-LITE (tasks\swelite.ps1).
#
# Regra determinista (nesta ordem, do commit mais novo para o mais antigo; o primeiro que passa leva):
#   1. mensagem do commit contem `fix`/`Fix`/`fixes`/`fixed`/`fixing`
#   2. o diff toca exatamente 2 arquivos: 1 `_test.go` e 1 nao-teste, no MESMO diretorio (mesmo pacote)
#   3. o diff de codigo tem menos de -MaxCodeLines linhas somadas (add+del)
#   4. as linhas alteradas do arquivo de teste pertencem a EXATAMENTE UMA funcao top-level `Test*` (mapeamento por
#      faixa de linhas nas duas versoes do arquivo: linha `+` -> versao do commit, linha `-` -> versao do pai)
#   5. o commit PAI tem go.mod (o harness roda em modo modulo; em cobra antigo, GOPATH, o `go test` nem inicia)
#   6. VERIFICACAO POR PROGRAMA, no diretorio de brinquedo (copia do cache, nunca o fixture):
#        a. cache no commit PAI + apenas o arquivo de teste do commit -> `go test -run ^Test$` DEVE FALHAR
#        b. + o arquivo de codigo do commit -> `go test -run ^Test$` DEVE PASSAR e o pacote INTEIRO DEVE PASSAR
#   Candidato que falhar em qualquer passo e descartado e o proximo entra.
#
# O "pai" de um commit e sempre `commit^` (o primeiro pai de verdade), NUNCA a entrada anterior do `git log`: a ordem
# do log nao e garantidamente a dos pais e usa-la produzia um diff vazio (commit contra ele mesmo).
#
# Todas as chamadas de `go test` usam -vet=off: em Go 1.27 o vet embutido quebra em codigo antigo do hugo
# (cmd/vendor/.../unitchecker) e o comando sairia com erro mesmo com o teste passando.
#
# Saida: tasks\swelite.commits.json (escrito a cada commit aceito: interromper nao perde o que ja foi verificado;
# rodar de novo com -Resume continua de onde parou).

param([int]$MaxCobra = 4, [int]$MaxHugo = 4, [int]$LogCobra = 3000, [int]$LogHugo = 1500,
      [int]$MaxCodeLines = 40, [double]$MaxPkgSec = 40, [switch]$Resume)
$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'Swelite.ps1')
. (Join-Path $script:SweliteBench 'lib\Bench.ps1')
$cfg = Get-BenchConfig
if (-not (Test-SweliteGo)) { throw 'swelite: `go` nao esta no PATH' }
$play = Join-Path $cfg.work_dir 'swelite-src\_verify'
$quota = [ordered]@{ cobra = $MaxCobra; hugo = $MaxHugo }

$json = $script:SweliteCommitsJson
$acc = New-Object Collections.Generic.List[object]
if ($Resume -and (Test-Path $json)) {
  foreach ($c in (Get-Content $json -Raw | ConvertFrom-Json).commits) { $acc.Add($c); $quota[$c.repo] = $quota[$c.repo] - 1 }
  "swelite: retomando com $($acc.Count) ja verificados ($($quota.cobra) cobra / $($quota.hugo) hugo faltando)"
}

# ---- mapeia cada linha alterada do arquivo de teste para a funcao top-level Test* que a contem ----
function Get-TestFuncAt([string[]]$Lines, [int]$Line) {
  $cur = $null
  for ($i = 0; $i -lt [math]::Min($Line, $Lines.Count); $i++) {
    if ($Lines[$i] -match '^func\s+(Test\w*)\s*\(') { $cur = $Matches[1] } elseif ($Lines[$i] -match '^func\s') { $cur = $null }
  }
  return $cur
}
function Get-TouchedTestFuncs([string]$Src, [string]$Parent, [string]$Commit, [string]$TestFile) {
  $d = & git -C $Src diff -U0 $Parent $Commit -- $TestFile 2>&1
  if ($LASTEXITCODE) { return @() }
  $newLines = @(& git -C $Src show "${Commit}:$TestFile" 2>&1)
  $oldLines = @(& git -C $Src show "${Parent}:$TestFile" 2>&1)
  $co = 0; $cn = 0; $names = New-Object Collections.Generic.List[string]
  foreach ($l in $d) {
    if ($l -match '^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@') { $co = [int]$Matches[1]; $cn = [int]$Matches[2]; continue }
    if ($l.StartsWith('+++') -or $l.StartsWith('---')) { continue }
    if ($l.StartsWith('+')) { $f = Get-TestFuncAt $newLines $cn; if ($f) { $names.Add($f) }; $cn++ }
    elseif ($l.StartsWith('-')) { $f = Get-TestFuncAt $oldLines $co; if ($f) { $names.Add($f) }; $co++ }
  }
  return @($names | Select-Object -Unique)
}

function Test-Candidate($Repo, $Src, $Commit, $Parent, $Code, $Test, $Pkg, $TestName) {
  if (Test-Path $play) { Remove-Item -Recurse -Force $play }
  Copy-SweliteTree $Src $play
  $r = Invoke-SweliteGit $play @('checkout', '--quiet', '--detach', $Parent)
  if ($r.exit) { return @{ ok = $false; why = "checkout do pai falhou: $($r.out -join ' ')" } }
  $r = Invoke-SweliteGit $play @('checkout', '--quiet', $Commit, '--', $Test)
  if ($r.exit) { return @{ ok = $false; why = "aplicar teste falhou: $($r.out -join ' ')" } }
  $fail = Invoke-SweliteGoTest $play $Pkg $TestName
  if ($fail.exit -eq 0) { return @{ ok = $false; why = 'o teste JA PASSA no pai (nao serve de oraculo)' } }
  if ($fail.out -match 'cannot find main module|no required module|build constraints exclude|undefined:') {
    return @{ ok = $false; why = 'o pai nao compila (era pre-modulos?)' } }
  Set-SweliteFromCommit $cfg $play $Repo $Commit $Code
  $pass = Invoke-SweliteGoTest $play $Pkg $TestName
  if ($pass.exit) { return @{ ok = $false; why = "com o codigo do commit o teste ainda falha: $(($pass.out | Select-Object -Last 2) -join ' ')" } }
  $pkgRun = Invoke-SweliteGoTest $play $Pkg $null
  if ($pkgRun.exit) { return @{ ok = $false; why = "o pacote inteiro quebrou: $(($pkgRun.out | Select-Object -Last 2) -join ' ')" } }
  if ($pkgRun.sec -gt $MaxPkgSec) { return @{ ok = $false; why = "pacote pesado demais ($($pkgRun.sec)s > ${MaxPkgSec}s)" } }
  return @{ ok = $true; parent_s = $fail.sec; pkg_s = $pkgRun.sec }
}

function Save-SweliteCommits {
  $i = 0; foreach ($c in $acc) { $i++; $c.id = "SL$i" }
  $sorted = @($acc | Sort-Object -Property @{ Expression = { if ($_.repo -eq 'cobra') { 0 } else { 1 } } }, @{ Expression = { $_.date } })
  $j = 0; foreach ($c in $sorted) { $j++; $c.id = "SL$j" }
  $o = [ordered]@{ generated = (Get-Date).ToString('s'); suite = 'swelite'
    rule = 'commit com "fix" na mensagem; diff de 1 arquivo de teste + 1 de codigo no mesmo pacote; <40 linhas de codigo; linhas alteradas do teste em 1 unica funcao Test*; verificado por go test -vet=off (falha no pai com o teste do commit, passa com o codigo do commit, pacote inteiro passa)'
    vet_off = $true; commits = @($sorted) }
  [IO.File]::WriteAllText($json, ($o | ConvertTo-Json -Depth 6), (New-Object Text.UTF8Encoding $false))
}

foreach ($repo in @('cobra', 'hugo')) {
  if ($quota[$repo] -le 0) { continue }
  $src = Get-SweliteSource $cfg $repo
  $ref = $cfg.repos.$repo.ref
  $n = if ($repo -eq 'cobra') { $LogCobra } else { $LogHugo }
  "swelite/$repo : lendo historico (ultimos $n commits)..."
  $log = & git -C $src log -n $n --no-merges --name-status --format='CL|%H|%ct|%s' $ref 2>&1
  $h = $null; $subj = ''; $date = ''; $files = @()
  # $cnt e um objeto mutavel de proposito: `& $flush` roda em escopo filho, entao `$cand++` criaria uma copia local
  # e o total relatado ficaria sempre em 0.
  $cnt = @{ cand = 0 }
  $flush = {
    if (-not $h) { return }
    if ($quota[$repo] -le 0) { return }
    if ($subj -notmatch '(?i)\bfix(es|ed|ing)?\b') { return }
    $tst = @($files | Where-Object { $_[1] -like '*_test.go' }); $code = @($files | Where-Object { $_[1] -notlike '*_test.go' })
    if ($files.Count -ne 2 -or $tst.Count -ne 1 -or $code.Count -ne 1) { return }
    $c = $code[0][1]; $t = $tst[0][1]
    if ((Split-Path $c -Parent) -ne (Split-Path $t -Parent)) { return }
    if ($c -like 'vendor/*' -or $t -like 'vendor/*') { return }
    # `package` e caminho de pacote Go: sempre com '/', nunca com '\'. Split-Path devolve o separador do Windows e
    # o valor vaza para o prompt e para o `go test ./pkg/` do oraculo.
    $pkg = (Split-Path $c -Parent) -replace '\\', '/'
    $stat = & git -C $src diff --numstat "$h^" $h -- $c 2>&1
    $p = ($stat | Select-Object -First 1) -split "\t"
    if ($p.Count -lt 2 -or $p[0] -eq '-' -or $p[1] -eq '-') { return }
    $lines = [int]$p[0] + [int]$p[1]
    if ($lines -ge $MaxCodeLines) { return }
    # @(...) e obrigatorio: com 1 elemento o PowerShell desembrulha o array na volta da funcao, `$names` vira
    # string e `$names[0]` seria o primeiro CARACTERE do nome do teste (era o bug do "-run ^T$": sem teste
    # selecionado o `go test` sai 0 e o candidato era descartado como "ja passa no pai").
    $names = @(Get-TouchedTestFuncs $src "$h^" $h $t)
    if ($names.Count -ne 1) { return }
    # O harness roda em modo modulo: sem go.mod no pai o `go test` nem inicia (cobra antigo era GOPATH).
    # ls-tree le so as arvores, entao nao dispara download no clone blobless.
    if (-not ($(Invoke-SweliteGit $src @('ls-tree', '--name-only', "${h}^", '--', 'go.mod')).out -join '').Trim()) { return }
    $cnt.cand++
    $res = Test-Candidate $repo $src $h "$($h)^" $c $t $pkg $names[0]
    if (-not $res.ok) { "  $repo $($h.Substring(0,10)) descartado: $($res.why)"; return }
    $quota[$repo] = $quota[$repo] - 1
    $acc.Add([ordered]@{ id = ''; repo = $repo; hash = $h; parent = (& git -C $src rev-parse "$h^").Trim(); ref = $ref; date = $date
      code_file = $c; test_file = $t; package = $pkg; test = $names[0]; code_lines = $lines; summary = $subj
      parent_fail_s = $res.parent_s; pkg_test_s = $res.pkg_s })
    Save-SweliteCommits
    "  $repo $($h.Substring(0,10)) ACEITO  $($names[0])  [$c]  ($lines linhas, pai falha em $($res.parent_s)s, pacote $($res.pkg_s)s)  quota=$($quota[$repo])"
  }
  foreach ($l in $log) {
    if ($l -match '^CL\|([0-9a-f]{40})\|(\d+)\|(.*)$') {
      & $flush
      $h = $Matches[1]; $date = [DateTimeOffset]::FromUnixTimeSeconds([int64]$Matches[2]).ToString('yyyy-MM-dd'); $subj = $Matches[3]; $files = @()
    } elseif ($l -match "\t") { $p = $l -split "\t"; $files += , @($p[0], $p[-1]) }
  }
  & $flush
  "swelite/$repo : $($cnt.cand) candidato(s) avaliado(s), quota restante $($quota[$repo])"
}

Save-SweliteCommits
"swelite: $($acc.Count) commit(s) em $json"
$acc | ForEach-Object { "  {0} {1,-5} {2} {3} {4} :: {5}" -f $_.id, $_.repo, $_.hash.Substring(0, 10), $_.package, $_.test, $_.summary }
if ($acc.Count -lt 8) { "swelite: ATENCAO - menos de 8 tarefas (aumente -LogCobra/-LogHugo ou -MaxPkgSec)" }
