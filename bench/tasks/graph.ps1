# Tarefas de estrutura em repositorio grande (Hugo v0.139.5, ~490 arquivos Go nao-teste): onde um grafo de chamadas
# deveria compensar frente a grep. So leitura. Gabarito calculado por programa independente (tools/callers, go/ast).

function New-CallersTask($id, $name, $target) {
  $st = @{ gold = @() }; $benchRoot = $script:BenchRoot; $repoDir = $Fixtures.hugo; $scoreSet = ${function:Score-Set}
  $TaskTable[$id] = @{ name = $name; stratum = 'grafo'; repos = @('hugo')
    Prepare = { param($dir)
      Push-Location (Join-Path $benchRoot 'tools\callers')
      try { $r = @(& go run . $repoDir $target | Where-Object { $_.Trim() }) } finally { Pop-Location }
      $root = ($repoDir -replace '\\', '/') + '/'
      $st.gold = @($r | ForEach-Object { ($_ -replace '\\', '/').Replace($root, '') -replace ':(?:\w+\.)+', ':' } | Select-Object -Unique)
      if ($st.gold.Count -lt 3) { throw "${id}: gabarito pequeno demais" }
      "No repositório Go em $repoDir, liste todas as funções que chamam diretamente a função ou método de nome '$target' (qualquer receptor, correspondência pelo nome simples), ignorando arquivos _test.go. Responda uma por linha no formato arquivo:função (caminho relativo à raiz do repositório, métodos como Tipo.método), sem mais nada." }.GetNewClosure()
    Gold = { $st.gold -join "`n" }.GetNewClosure()
    Score = { param($a) $got = @(($a -split "`n") | ForEach-Object { $_.Trim().Trim('`').TrimStart('-', '*', ' ') } | Where-Object { $_ -match '^[\w./-]+\.go:[\w.]+' } | ForEach-Object { ($_ -replace '\s.*$', '' -replace ':(?:\w+\.)+', ':') }); & $scoreSet $got $st.gold }.GetNewClosure() }
}
New-CallersTask 'G1' 'Chamadores (Hugo, ~10 sítios)' 'NewFileErrorFromName'
New-CallersTask 'G2' 'Chamadores (Hugo, ~21 sítios)' 'Deprecate'
New-CallersTask 'G3' 'Chamadores (Hugo, ~31 sítios)' 'AddTemplateFuncsNamespace'
