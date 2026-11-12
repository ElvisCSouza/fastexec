# Suite `swelite` (SL1..SL8): correcao de bug REAL em repositorio publico, no espirito do SWE-bench Verified - mas leve.
# Nao e o SWE-bench oficial: nao usa Docker/Linux, nao usa o harness deles e nao roda o modelo; cada tarefa e um commit
# de bug-fix real de cobra/hugo (a lista verificada esta em swelite.commits.json, escolhida por
# tools\swelite\Select-Commits.ps1), com o PROPRIO teste do commit como oraculo.
#
# Contrato de cada tarefa (mesmo das outras suites):
#   Prepare($dir) -> prompt : copia o cache de historia para $dir, volta ao commit PAI e aplica SOMENTE o arquivo de teste
#                             do commit. O prompt descreve o sintoma (mensagem do commit + nome do teste), sem dar o diff
#                             nem o nome do arquivo de codigo.
#   Gold()                  : aplica o arquivo de codigo do commit (o gabarito de verdade).
#   Score($answer)          : checa o estado do repositorio, nao a resposta: o teste alvo passa, o pacote inteiro passa,
#                             nenhum *_test.go mudou em relacao ao estado preparado e nada fora do pacote ficou sujo.
. (Join-Path (Split-Path $PSScriptRoot -Parent) 'tools\swelite\Swelite.ps1')

# Escopo das closures - a mesma armadilha do graph.ps1/contexto.ps1, em dose tripla. Os scriptblocks nascem DENTRO da
# funcao New-SweliteTask e um scriptblock de GetNewClosure() so enxerga o escopo LOCAL de quem o criou: nem as variaveis
# do arquivo, nem as do Run-Bench.ps1, nem as funcoes do harness ou desta suite. Dai, aqui dentro:
#   (a) o caminho da lib vira um local ($lib), para ser capturado;
#   (b) Get-BenchConfig e Score-Bool (do lib\Bench.ps1) sao copiadas como ${function:...}, porque o nome nao resolve
#       quando a closure roda ("termo nao reconhecido");
#   (c) Prepare/Gold/Score refazem `. $lib`, que traz as funcoes do Swelite.ps1 para dentro da closure.
# Sem isso o dry-run falha com ". $null" em todas as 8 tarefas.
$sweliteLib = Join-Path (Split-Path $PSScriptRoot -Parent) 'tools\swelite\Swelite.ps1'

function New-SweliteTask($id, $c) {
  $st = @{ dir = ''; base = $null }
  # $script:BenchRoot e o caminho que o Run-Bench.ps1 publica e que graph/contexto ja usam; $sweliteLib cobre o
  # carregamento avulso do arquivo (sem Run-Bench.ps1 no meio).
  $lib = if ($script:BenchRoot) { Join-Path $script:BenchRoot 'tools\swelite\Swelite.ps1' } else { $sweliteLib }
  $gcfg = ${function:Get-BenchConfig}
  $scb = ${function:Score-Bool}
  # Pacote raiz: `package` vem vazio quando o arquivo de codigo esta na raiz do repo. Para exibir e para montar o
  # comando do `go test` o pacote raiz e '.', nunca ''.
  $pkgDisp = if ($c.package) { $c.package } else { '.' }
  $pkgArg = if ($c.package) { "./$($c.package)/" } else { '.' }
  $TaskTable[$id] = @{ name = "SWE-LITE: $($c.summary)"; stratum = 'swelite'
    Prepare = { param($dir)
      . $lib
      if (-not (Test-SweliteGo)) { throw "$id : 'go' nao esta no PATH" }
      $cfg = & $gcfg
      New-SweliteTaskDir $cfg $c.repo $dir $c.parent $c.hash $c.test_file | Out-Null
      $st.dir = $dir; $st.base = Get-SweliteBaseline $dir $c.package
      @"
Sintoma relatado no repositorio Go em $dir (estado imediatamente anterior a correcao): $($c.summary)
O teste $($c.test), no pacote $pkgDisp (arquivo $($c.test_file), ja presente no repositorio), expoe esse sintoma e
esta falhando. Corrija o codigo de producao do pacote $pkgDisp para que esse teste passe.

Regras: nao altere nenhum arquivo *_test.go (o teste e o criterio, nao a solucao); nao altere arquivos fora do pacote
$pkgDisp; nao e para "consertar" o teste nem para remove-lo.

Para conferir:
  cd $dir
  go test $pkgArg -run "^$($c.test)$" -count=1 -vet=off
O -vet=off e obrigatorio neste repositorio (o vet do Go atual quebra em codigo antigo e o comando falharia mesmo com os
testes passando). No fim, rode tambem `go test $pkgArg -count=1 -vet=off`, que deve passar inteiro.
Responda apenas: corrigido
"@
    }.GetNewClosure()
    Gold = { . $lib; $cfg = & $gcfg; Set-SweliteFromCommit $cfg $st.dir $c.repo $c.hash $c.code_file; 'corrigido' }.GetNewClosure()
    Score = { param($a)
      . $lib
      if (-not $st.dir -or -not $st.base) { return & $scb $false }
      $t = Invoke-SweliteGoTest $st.dir $c.package $c.test
      if ($t.exit -ne 0) { return & $scb $false }                           # teste alvo tem de passar
      if ((Invoke-SweliteGoTest $st.dir $c.package $null).exit -ne 0) { return & $scb $false }   # pacote inteiro idem
      $pkgDir = Join-Path $st.dir ($c.package -replace '/', '\')            # *_test.go intocados (hash) e nenhum novo
      $seen = @{}
      foreach ($f in @(Get-ChildItem $pkgDir -Filter '*_test.go' -File -ErrorAction SilentlyContinue)) {
        $k = "$($c.package)/$($f.Name)"; $seen[$k] = 1
        if (-not $st.base.tests.ContainsKey($k)) { return & $scb $false }
        if ((Get-FileHash $f.FullName -Algorithm SHA256).Hash -ne $st.base.tests[$k]) { return & $scb $false }
      }
      if ($seen.Count -ne $st.base.tests.Count) { return & $scb $false }     # algum teste do pacote foi apagado
      foreach ($p in Get-SweliteDirty $st.dir) {                             # sujeira nova so pode estar dentro do pacote
        if ($st.base.dirty -contains $p) { continue }
        # Pacote raiz: sem diretorio, "dentro do pacote" e qualquer arquivo na raiz do repo (sem '/').
        $inPkg = if ($c.package) { $p -like "$($c.package)/*" } else { $p -notlike '*/*' }
        if (-not $inPkg) { return & $scb $false }
      }
      & $scb $true
    }.GetNewClosure()
  }
}

foreach ($id in (Get-SwelitePlan).Keys) { New-SweliteTask $id (Get-SwelitePlan)[$id] }
