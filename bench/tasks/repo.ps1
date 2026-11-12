# Tarefas sobre codigo real e publico (cobra, ref fixada no bench.config.json). So leitura: a pasta e compartilhada.
# O gabarito e calculado na hora, no proprio host, por programa independente das duas ferramentas.

function Get-CobraGoFiles { Get-ChildItem $Fixtures.cobra -Recurse -Filter *.go -File | Where-Object { $_.Name -notlike '*_test.go' -and $_.FullName -notmatch '\\\.git\\' } }

$TaskTable['R1'] = @{ name = 'Contagem em repositório (funções Mark*)'; stratum = 'codigo'; repos = @('cobra')
  Prepare = { param($dir)
    $n = 0; $files = @{}
    foreach ($f in Get-CobraGoFiles) { foreach ($l in [IO.File]::ReadLines($f.FullName)) { if ($l -match '^func\s+(\([^)]*\)\s*)?Mark\w*') { $n++; $files[$f.FullName] = 1 } } }
    $script:r1 = @{ funcs = $n; files = $files.Count }
    "No repositório Go em $($Fixtures.cobra), ignorando arquivos _test.go: quantas declarações func (funções ou métodos) têm nome começando com 'Mark', e em quantos arquivos elas estão? Responda em uma linha: funcoes; arquivos." }
  Gold = { "$($script:r1.funcs); $($script:r1.files)" }
  Score = { param($a) $nums = @([regex]::Matches($a, '\d+') | ForEach-Object { [int]$_.Value }); Score-Bool (($nums.Count -ge 2) -and ($nums[0] -eq $script:r1.funcs) -and ($nums[1] -eq $script:r1.files)) } }

$TaskTable['R2'] = @{ name = 'Chamadores de função (Go, cobra)'; stratum = 'codigo'; repos = @('cobra')
  Prepare = { param($dir)
    Push-Location (Join-Path $script:BenchRoot 'tools\callers')
    try { $r = @(& go run . $Fixtures.cobra 'stripFlags' | Where-Object { $_.Trim() }) } finally { Pop-Location }
    $script:r2 = @($r | ForEach-Object { ($_ -replace '\\', '/') -replace ':(?:\w+\.)+', ':' } | ForEach-Object { $_.Replace(($Fixtures.cobra -replace '\\', '/') + '/', '') } | Select-Object -Unique)
    if ($script:r2.Count -lt 1) { throw 'R2: gabarito vazio (alvo sem chamadores?)' }
    "No repositório Go em $($Fixtures.cobra), liste todas as funções que chamam diretamente a função stripFlags, ignorando arquivos _test.go. Responda uma por linha no formato arquivo:função (caminho relativo à raiz do repositório, métodos como Tipo.método), sem mais nada." }
  Gold = { $script:r2 -join "`n" }
  Score = { param($a) $got = @(($a -split "`n") | ForEach-Object { $_.Trim().Trim('`').TrimStart('-', '*', ' ') } | Where-Object { $_ -match '^[\w./-]+\.go:[\w.]+' } | ForEach-Object { ($_ -replace '\s.*$', '' -replace ':(?:\w+\.)+', ':') }); Score-Set $got $script:r2 } }

$TaskTable['R3'] = @{ name = 'Localizar função em arquivo grande'; stratum = 'codigo'; repos = @('cobra')
  Prepare = { param($dir)
    $f = Join-Path $Fixtures.cobra 'command.go'; $L = [IO.File]::ReadAllLines($f); $start = -1; $end = -1
    for ($i = 0; $i -lt $L.Count; $i++) { if ($start -lt 0 -and $L[$i] -match '^func \(c \*Command\) ExecuteC\(\)') { $start = $i } elseif ($start -ge 0 -and $L[$i] -eq '}') { $end = $i; break } }
    $script:r3 = @{ line = $start + 1; len = $end - $start + 1 }
    "No arquivo $f, em que linha começa a declaração de 'func (c *Command) ExecuteC()' e quantas linhas tem a função, da linha da declaração até a chave de fechamento inclusive? Responda em uma linha: linha; quantidade." }
  Gold = { "$($script:r3.line); $($script:r3.len)" }
  Score = { param($a) $nums = @([regex]::Matches($a, '\d+') | ForEach-Object { [int]$_.Value }); Score-Bool (($nums.Count -ge 2) -and ($nums[0] -eq $script:r3.line) -and ($nums[1] -eq $script:r3.len)) } }

$TaskTable['R4'] = @{ name = 'Encadeado: 3 maiores arquivos e contagem de TODO'; stratum = 'codigo'; repos = @('cobra')
  Prepare = { param($dir)
    $top = Get-CobraGoFiles | Sort-Object Length -Descending | Select-Object -First 3
    $script:r4 = @($top | ForEach-Object { $c = 0; foreach ($l in [IO.File]::ReadLines($_.FullName)) { if ($l -match 'TODO') { $c++ } }; @{ name = $_.Name; todo = $c } })
    "No repositório Go em $($Fixtures.cobra), considerando só arquivos .go que não sejam _test.go: quais são os 3 maiores arquivos (em bytes) e quantas linhas de cada um contêm a palavra 'TODO'? Responda uma linha por arquivo no formato nome_do_arquivo; quantidade, do maior para o menor." }
  Gold = { ($script:r4 | ForEach-Object { "$($_.name); $($_.todo)" }) -join "`n" }
  Score = { param($a) $ok = $true; foreach ($e in $script:r4) { if (-not ($a -match ('(?m)' + [regex]::Escape($e.name) + '\W+' + $e.todo + '\b'))) { $ok = $false } }; Score-Bool $ok } }
