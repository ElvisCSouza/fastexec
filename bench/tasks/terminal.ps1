# Suíte de terminal real no Windows (estrato 'terminal'), tarefas W1-W8.
# Compara apenas os braços native e fastexec. Cada Prepare gera o cenário inteiro num
# diretório temporário (sem rede, sem efeito fora dele) e o oráculo é calculado por
# programa a partir do que foi gerado — nunca escrito à mão. Nada aqui chama modelo.
# Ferramenta ausente no host (git/go) degrada a tarefa com aviso documentado, em vez de
# quebrar o dry-run num host sem ela.

$script:TermUtf8 = New-Object Text.UTF8Encoding $false

function Test-TermRunnable {
  # 'Get-Command' não basta: em Windows o alias da Microsoft Store existe e falha com
  # exit 9009 ao executar. Só vale o que executa e termina com código 0.
  param([string]$Exe, [string[]]$ToolArgs = @())
  if (-not (Get-Command $Exe -ErrorAction SilentlyContinue)) { return $false }
  try { $null = & $Exe @ToolArgs 2>&1; return ($LASTEXITCODE -eq 0) } catch { return $false }
}

function Write-TermSkip {
  # Aviso de tarefa degradada. Cada tarefa é preparada duas vezes no dry-run; o conjunto evita repetir a linha.
  param([string]$Msg)
  if (Test-Path Env:BENCH_TERMINAL_QUIET) { return }
  if (-not $script:TermSkipVistos) { $script:TermSkipVistos = @{} }
  if ($script:TermSkipVistos.ContainsKey($Msg)) { return }
  $script:TermSkipVistos[$Msg] = $true
  Write-Host "  [terminal] $Msg" -ForegroundColor DarkYellow
}

function Write-TermText { param([string]$Path, [string]$Text) [IO.File]::WriteAllText($Path, $Text, $script:TermUtf8) }

function New-TermToken { param([int]$N = 6)
  -join (1..$N | ForEach-Object { 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'[(Get-Random -Maximum 32)] }) }

# Número/palavra inteira: não casa colado em dígito, ponto ou vírgula vizinhos (5120 não casa dentro de 15120).
function Test-TermWord { param([string]$Text, [string]$Word)
  [bool]($Text -match ('(?<![\d.,])' + [regex]::Escape($Word) + '(?![\d])')) }

# Campo rotulado da resposta: aceita "rotulo=valor" / "rotulo: valor"; sem rótulo, cai para a palavra inteira.
function Test-TermField {
  param([string]$Text, [string]$Label, [string]$Value)
  if ($Text -match ('(?i)' + [regex]::Escape($Label) + '\s*[=:]\s*"?([^\r\n;|]+)')) { return ($Matches[1] -like "*$Value*") }
  return (Test-TermWord $Text $Value)
}
function Get-TermField {
  param([string]$Text, [string]$Label)
  if ($Text -match ('(?i)' + [regex]::Escape($Label) + '\s*[=:]\s*"?([^\r\n;|]+)')) { return $Matches[1].Trim() }
  return ''
}
# Lista dentro de um campo (IPs, PIDs): compara item a item. Test-TermWord não serve aqui —
# o lookbehind recusaria "10.0.0.23" por vir depois de vírgula, e o ponto faz parte do valor.
function Get-TermList {
  param([string]$Text, [string]$Label)
  $v = Get-TermField $Text $Label; if (-not $v) { $v = $Text }
  return @($v -split '[,;\s]+' | Where-Object { $_ }) }
function Test-TermHas { param([string[]]$List, [string]$Item) [bool]($List -contains $Item) }

# Executa um processo externo sem passar pelo shell do harness: argumento vai como argumento,
# nunca reinterpretado. Sai com saída dos dois streams e código.
function Invoke-TermProc {
  param([string]$Exe, [string[]]$ArgList = @(), [string]$Dir = $null, [hashtable]$Env = $null)
  $psi = New-Object Diagnostics.ProcessStartInfo
  $psi.FileName = $Exe
  if ($psi.PSObject.Properties['ArgumentList']) { foreach ($a in $ArgList) { [void]$psi.ArgumentList.Add($a) } }
  else { $psi.Arguments = (($ArgList | ForEach-Object { '"' + ($_ -replace '"', '\"') + '"' }) -join ' ') }
  $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true
  $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
  if ($Dir) { $psi.WorkingDirectory = $Dir }
  if ($Env) { foreach ($k in $Env.Keys) { $psi.Environment[$k] = [string]$Env[$k] } }
  try {
    $p = [Diagnostics.Process]::Start($psi)
    $o = $p.StandardOutput.ReadToEnd(); $e = $p.StandardError.ReadToEnd(); $p.WaitForExit()
    return @{ code = $p.ExitCode; out = $o; err = $e }
  } catch { return @{ code = -1; out = ''; err = $_.Exception.Message } }
}

# ---------------------------------------------------------------------------
# W1 - caminhos com espaços e acentos: contar e ordenar por tamanho
# ---------------------------------------------------------------------------
$TaskTable['W1'] = @{ name = 'Caminhos com espaços e acentos (40 arquivos .txt)'; stratum = 'terminal'
  Prepare = { param($dir)
    $rnd = New-Object System.Random 11
    $sizes = New-Object Collections.Generic.List[int]
    for ($i = 0; $i -lt 40; $i++) { $sizes.Add($(if ($rnd.Next(0, 2) -eq 0) { $rnd.Next(300, 1020) } else { $rnd.Next(1030, 2500) })) }
    $sizes[3] = 1024                                          # exatamente 1024 bytes: NÃO é "mais de 1 KB" (armadilha de limite)
    $sizes[10] = 3584; $sizes[20] = 5120; $sizes[30] = 4096    # os três maiores, tamanhos únicos
    $bases = @('Relatório Final', 'Análise de Consumo', 'Resumo Executivo', 'Índice Geral', 'Ata da Reunião', 'Orçamento Aprovado', 'Estatística Mensal', 'Diagnóstico Técnico')
    $subs = @('Relatórios Finais 2026', 'Anexos e Comprovantes\Pasta Sem Nome')
    $filler = 'conteudo de teste 0123456789 '
    $itens = @()
    for ($i = 0; $i -lt 40; $i++) {
      $nome = "$($bases[$i % 8]) (v$([math]::Floor($i / 8) + 1)).txt"
      $full = Join-Path $dir ("$($subs[$i % 2])\$nome")
      New-Item -ItemType Directory -Force (Split-Path $full -Parent) | Out-Null
      $corpo = "# $nome`n"; $falta = $sizes[$i] - [Text.Encoding]::UTF8.GetByteCount($corpo)
      while ($falta -gt 0) { $n = [math]::Min($falta, $filler.Length); $corpo += $filler.Substring(0, $n); $falta -= $n }
      Write-TermText $full $corpo
      $itens += [pscustomobject]@{ nome = $nome; tam = (Get-Item $full).Length }
    }
    $maiores = @($itens | Where-Object { $_.tam -gt 1024 } | Sort-Object tam -Descending)
    $script:W1 = @{ n = $maiores.Count; top = @($maiores | Select-Object -First 3) }
    "Na árvore $dir há 40 arquivos .txt, em pastas com espaços e acentos no nome. Contando apenas os arquivos com mais de 1 KB, responda em uma linha: a quantidade e os 3 maiores, do maior para o menor, no formato nome do arquivo=tamanho em bytes, separados por ponto e vírgula." }
  Gold = { "$($script:W1.n); " + (($script:W1.top | ForEach-Object { "$($_.nome)=$($_.tam)" }) -join '; ') }
  Score = { param($a)
    $w = $script:W1; $ok = (Test-TermWord $a ([string]$w.n))
    foreach ($t in $w.top) { $ok = $ok -and ($a -like "*$($t.nome)*") -and (Test-TermWord $a ([string]$t.tam)) }
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W2 - git: histórico, autores e branches
# ---------------------------------------------------------------------------
$script:TermGitOk = Test-TermRunnable 'git' @('--version')
$script:W2Authors = @(
  @{ nome = 'Alice Souza'; email = 'alice@exemplo.com' },
  @{ nome = 'Bruno Lima'; email = 'bruno@exemplo.com' },
  @{ nome = 'Carla Nunes'; email = 'carla@exemplo.com' })
# 15 commits em main + 10 em feature; a feature sai do 8º commit de main e nunca é mesclada.
$script:W2Commits = @(
  @{ b = 'main'; a = 0; m = 'commit inicial'; f = @('README.md') },
  @{ b = 'main'; a = 0; m = 'estrutura do servico'; f = @('src/app.go', 'src/util.go') },
  @{ b = 'main'; a = 1; m = 'adiciona handler de pedidos'; f = @('src/app.go') },
  @{ b = 'main'; a = 2; m = 'normaliza caminhos'; f = @('src/util.go') },
  @{ b = 'main'; a = 1; m = 'adiciona funcao Legado'; f = @('src/app.go') },
  @{ b = 'main'; a = 0; m = 'trata erro de leitura'; f = @('src/app.go') },
  @{ b = 'main'; a = 1; m = 'ajusta log do handler'; f = @('src/app.go') },
  @{ b = 'main'; a = 2; m = 'documenta e corrige handler'; f = @('README.md', 'src/app.go') },
  @{ b = 'feature'; a = 2; m = 'esboco do filtro'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 1; m = 'filtro por periodo'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 0; m = 'teste do filtro'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 2; m = 'trata periodo vazio'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 1; m = 'cache do filtro'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 0; m = 'documenta o filtro'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 2; m = 'limite de pagina'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 1; m = 'ordena resultado'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 0; m = 'corrige ordenacao'; f = @('src/feature.go') },
  @{ b = 'feature'; a = 2; m = 'filtro pronto'; f = @('src/feature.go') },
  @{ b = 'main'; a = 0; m = 'remove duplicacao'; f = @('src/util.go') },
  @{ b = 'main'; a = 1; m = 'otimiza busca'; f = @('src/app.go') },
  @{ b = 'main'; a = 2; m = 'corrige limite'; f = @('src/app.go') },
  @{ b = 'main'; a = 1; m = 'remove funcao Legado'; f = @('src/app.go') },
  @{ b = 'main'; a = 0; m = 'renomeia handler'; f = @('src/app.go') },
  @{ b = 'main'; a = 2; m = 'ajusta parser'; f = @('src/util.go') },
  @{ b = 'main'; a = 0; m = 'atualiza instrucoes'; f = @('README.md') })

function Invoke-TermGit {
  param([string]$Repo, [string[]]$GitArgs, [hashtable]$Env = $null)
  $full = @('-c', 'commit.gpgsign=false', '-c', 'core.autocrlf=false', '-c', 'advice.detachedHead=false', '-C', $Repo) + $GitArgs
  Invoke-TermProc 'git' $full $null $Env
}

$TaskTable['W2'] = @{ name = 'Histórico git: autor, hash e commits exclusivos'; stratum = 'terminal'
  Prepare = { param($dir)
    if (-not $script:TermGitOk) {
      Write-TermSkip 'W2: sem git no host, tarefa degradada (resposta trivial)'
      $script:W2 = @{ degr = $true }
      return 'O git não está disponível neste computador. Responda apenas: indisponivel'
    }
    $repo = Join-Path $dir 'repo'
    New-Item -ItemType Directory -Force $repo | Out-Null
    [void](Invoke-TermProc 'git' @('-c', 'init.defaultBranch=main', 'init', '-q', $repo))
    $base = [datetime]'2026-10-01T09:00:00'
    $legado = $false; $hash = ''; $n = 0; $branch = 'main'
    foreach ($c in $script:W2Commits) {
      $n++
      # Troca de branch por lista de argumentos inteira: passar string vazia some no caminho e o checkout não acontece.
      if ($c.b -ne $branch) {
        [void](Invoke-TermGit $repo $(if ($c.b -eq 'feature') { @('checkout', '-q', '-b', 'feature') } else { @('checkout', '-q', 'main') }))
        $branch = $c.b
      }
      if ($c.m -eq 'adiciona funcao Legado') { $legado = $true }
      if ($c.m -eq 'remove funcao Legado') { $legado = $false }   # antes de escrever: o commit que remove não pode conter a função
      foreach ($f in @($c.f)) {
        $full = Join-Path $repo ($f -replace '/', '\')
        New-Item -ItemType Directory -Force (Split-Path $full -Parent) | Out-Null
        switch -Regex ($f) {
          'app\.go$' { $t = "package app`n`n// app.go - revisao $n`n`nfunc Handler() int {`n`treturn $n`n}`n"
            if ($legado) { $t += "`n// Legado mantem a compatibilidade com o formato antigo.`nfunc Legado() string {`n`treturn `"v$n`"`n}`n" }
            Write-TermText $full $t }
          'util\.go$' { Write-TermText $full "package app`n`n// util.go - revisao $n`n`nfunc Normalizar(s string) string {`n`treturn s + `"-$n`"`n}`n" }
          default { Write-TermText $full "# Projeto de exemplo`n`nRevisao $n do README.`n" }
        }
      }
      [void](Invoke-TermGit $repo @('add', '-A'))
      $a = $script:W2Authors[$c.a]
      $env2 = @{ GIT_AUTHOR_NAME = $a.nome; GIT_AUTHOR_EMAIL = $a.email; GIT_COMMITTER_NAME = $a.nome; GIT_COMMITTER_EMAIL = $a.email
        GIT_AUTHOR_DATE = $base.AddMinutes($n * 7).ToString('yyyy-MM-ddTHH:mm:sszzz'); GIT_COMMITTER_DATE = $base.AddMinutes($n * 7).ToString('yyyy-MM-ddTHH:mm:sszzz') }
      [void](Invoke-TermGit $repo @('commit', '-q', '--no-verify', '-m', $c.m) $env2)
      if ($c.m -eq 'remove funcao Legado') { $hash = (Invoke-TermGit $repo @('rev-parse', '--short', 'HEAD')).out.Trim() }
    }
    [void](Invoke-TermGit $repo @('checkout', '-q', 'main'))
    $cnt = @{}
    foreach ($c in $script:W2Commits) { if ($c.f -contains 'src/app.go') { $cnt[$c.a] = 1 + [int]$cnt[$c.a] } }
    $top = @($cnt.GetEnumerator() | Sort-Object Value -Descending | Select-Object -First 1)[0]
    $excl = @($script:W2Commits | Where-Object { $_.b -eq 'feature' }).Count
    $script:W2 = @{ degr = $false; repo = $repo; autor = $script:W2Authors[[int]$top.Key].nome; hash = $hash; excl = $excl }
    "O repositório git em $repo tem 25 commits, 3 autores e duas branches, main e feature. Responda em uma linha: qual autor mais alterou o arquivo src/app.go (nome completo); o hash curto do commit que removeu a função Legado; e quantos commits a branch feature tem que não estão em main. Formato: autor=...; hash=...; exclusivos=..." }
  Gold = { if ($script:W2.degr) { 'indisponivel' } else { "autor=$($script:W2.autor); hash=$($script:W2.hash); exclusivos=$($script:W2.excl)" } }
  Score = { param($a)
    if ($script:W2.degr) { return (Score-Bool ($a -like '*indisponivel*')) }
    $ok = (Test-TermField $a 'autor' $script:W2.autor) -and (Test-TermField $a 'hash' $script:W2.hash) -and (Test-TermField $a 'exclusivos' ([string]$script:W2.excl))
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W3 - log de 20 mil linhas em formatos mistos
# ---------------------------------------------------------------------------
$TaskTable['W3'] = @{ name = 'Log misto de 20 mil linhas: erros por código e IPs'; stratum = 'terminal'
  Prepare = { param($dir)
    $codes = @('E4213', 'E1102', 'E9001')
    $pool = @('10.0.0.7', '10.0.0.23', '192.168.4.9', '192.168.4.51', '172.16.8.3', '172.16.8.44', '10.1.2.5', '10.1.2.88', '203.0.113.14', '203.0.113.77')
    # Peso de cada IP (5º e 6º bem separados, para o "top 5" não ter empate na fronteira).
    # Nada de '1..$w[$k]': com peso 0 isso vira a faixa DESCENDENTE 1,0 e o peso 0 vale 2.
    $w = @(28, 20, 15, 11, 9, 7, 5, 3, 2, 1)
    $tab = @()
    for ($k = 0; $k -lt $w.Count; $k++) { for ($j = 0; $j -lt $w[$k]; $j++) { $tab += $k } }
    $rnd = New-Object System.Random 3
    $lines = New-Object Collections.Generic.List[string]
    $t0 = [datetime]'2026-11-10T00:00:00'; $erros = @(); $ipErro = @{}
    for ($i = 0; $i -lt 20000; $i++) {
      $ts = $t0.AddSeconds($i * 3)
      if ($rnd.Next(0, 100) -lt 7) {
        $cod = $codes[$rnd.Next(0, 3)]; $ip = $pool[$tab[$rnd.Next(0, $tab.Count)]]
        $erros += [pscustomobject]@{ cod = $cod; ip = $ip }
        $ipErro[$ip] = 1 + [int]$ipErro[$ip]
        switch ($rnd.Next(0, 3)) {
          0 { $lines.Add("$($ts.ToString('yyyy-MM-dd HH:mm:ss')) ERROR [$cod] falha ao processar lote ip=$ip") }
          1 { $lines.Add("ts=$($ts.ToString('yyyy-MM-ddTHH:mm:ssZ')) level=error code=$cod client=$ip msg=`"timeout no parceiro`"") }
          default { $lines.Add("$($ts.ToString('yyyy-MM-dd HH:mm:ss')) ERROR [$cod] ip=$ip retry=$($rnd.Next(1, 4))") }
        }
      }
      elseif ($rnd.Next(0, 100) -lt 4) { $lines.Add("ts=$($ts.ToString('yyyy-MM-ddTHH:mm:ssZ')) level=warn msg=`"disco $($rnd.Next(70, 90))% cheio`"") }
      else { $lines.Add("$($ts.ToString('yyyy-MM-dd HH:mm:ss')) INFO heartbeat ok worker=$($rnd.Next(1, 5))") }
    }
    [IO.File]::WriteAllLines("$dir\app.log", $lines, $script:TermUtf8)
    $porCod = @{}; foreach ($e in $erros) { $porCod[$e.cod] = 1 + [int]$porCod[$e.cod] }
    $ordem = @($ipErro.GetEnumerator() | Sort-Object Value -Descending)
    $top5 = @($ordem | Select-Object -First 5 | ForEach-Object { $_.Key })
    $script:W3 = @{ total = $erros.Count; cod = $porCod; top5 = $top5; sexto = $(if ($ordem.Count -gt 5) { $ordem[5].Key } else { '' }) }
    "O arquivo $dir\app.log tem 20 mil linhas em formatos mistos (o nível de erro aparece como ERROR ou level=error). Responda em uma linha com: o total de linhas de nível ERROR; a contagem por código no formato CODIGO=quantidade; e os 5 endereços IP mais frequentes entre as linhas de ERROR, do mais para o menos frequente. Formato: total=...; E4213=...; E1102=...; E9001=...; ips=ip1,ip2,ip3,ip4,ip5" }
  Gold = { $w = $script:W3
    "total=$($w.total); " + (($w.cod.GetEnumerator() | Sort-Object Name | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join '; ') + "; ips=" + ($w.top5 -join ',') }
  Score = { param($a)
    $w = $script:W3; $ok = (Test-TermField $a 'total' ([string]$w.total))
    foreach ($k in $w.cod.Keys) { $ok = $ok -and (Test-TermField $a $k ([string]$w.cod[$k])) }
    $campo = Get-TermList $a 'ips'
    foreach ($ip in $w.top5) { $ok = $ok -and (Test-TermHas $campo $ip) }
    if ($w.sexto) { $ok = $ok -and (-not (Test-TermHas $campo $w.sexto)) }
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W4 - processos: listar, identificar e encerrar só o segundo
# ---------------------------------------------------------------------------
function Test-TermAlive {
  param([int]$ProcId, [string]$Token)
  try { $p = Get-CimInstance Win32_Process -Filter "ProcessId=$ProcId" -ErrorAction SilentlyContinue } catch { $p = $null }
  if ($p) { return [bool]($p.CommandLine -and $p.CommandLine.Contains($Token)) }
  return [bool](Get-Process -Id $ProcId -ErrorAction SilentlyContinue)
}
function Stop-TermProc { param([int]$ProcId)
  if ($ProcId -gt 0) { [void](Invoke-TermProc 'taskkill' @('/PID', [string]$ProcId, '/T', '/F')) } }

$TaskTable['W4'] = @{ name = 'Três processos: identificar e encerrar apenas o segundo'; stratum = 'terminal'
  Prepare = { param($dir)
    if ($script:W4 -and $script:W4.pids) { foreach ($p in $script:W4.pids) { Stop-TermProc $p } }   # limpa sobra da preparação anterior
    $tok = 'W4-' + (New-TermToken 6)
    $pids = @()
    foreach ($k in 1..3) {
      $psi = New-Object Diagnostics.ProcessStartInfo
      $psi.FileName = $env:ComSpec
      $psi.Arguments = "/c `"set TERM_MARCA=$tok-$k & ping -n 300 127.0.0.1 >nul`""
      $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
      $psi.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
      $pids += ([Diagnostics.Process]::Start($psi)).Id
    }
    $script:W4 = @{ tok = $tok; pids = $pids; vivo = @($pids[0], $pids[2]) }
    for ($i = 0; $i -lt 30 -and (Test-TermAlive $pids[0] $tok) -eq $false; $i++) { Start-Sleep -Milliseconds 100 }
    "Foram iniciados 3 processos, cada um com a marca TERM_MARCA=$tok-N na própria linha de comando, com N valendo 1, 2 e 3. Liste o PID e a linha de comando completa de cada um e encerre APENAS o processo de número 2, deixando os outros dois rodando. Responda em uma linha no formato: marca=$tok; pids=pid1,pid2,pid3; vivos=pids que continuaram rodando" }
  Gold = { $w = $script:W4; Stop-TermProc $w.pids[1]
    for ($i = 0; $i -lt 30 -and (Test-TermAlive $w.pids[1] $w.tok); $i++) { Start-Sleep -Milliseconds 100 }
    "marca=$($w.tok); pids=$($w.pids -join ','); vivos=$($w.vivo -join ',')" }
  Score = { param($a)
    $w = $script:W4
    $estado = (Test-TermAlive $w.pids[0] $w.tok) -and (Test-TermAlive $w.pids[2] $w.tok) -and (-not (Test-TermAlive $w.pids[1] $w.tok))
    $pids = Get-TermList $a 'pids'
    $vivos = Get-TermList $a 'vivos'
    $ok = $estado -and (Test-TermField $a 'marca' $w.tok)
    foreach ($p in $w.pids) { $ok = $ok -and (Test-TermHas $pids ([string]$p)) }
    foreach ($p in $w.vivo) { $ok = $ok -and (Test-TermHas $vivos ([string]$p)) }
    $ok = $ok -and (-not (Test-TermHas $vivos ([string]$w.pids[1])))
    foreach ($p in $w.pids) { Stop-TermProc $p }        # limpeza sempre, inclusive quando a resposta está errada
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W5 - variáveis de ambiente: um script .cmd e um .ps1
# ---------------------------------------------------------------------------
$script:TermPsExe = $(if (Test-TermRunnable 'pwsh' @('-NoProfile', '-Command', 'exit 0')) { 'pwsh' } elseif (Test-TermRunnable 'powershell' @('-NoProfile', '-Command', 'exit 0')) { 'powershell' } else { '' })

$TaskTable['W5'] = @{ name = 'Dois scripts definindo FOO_BAR=42 (cmd e PowerShell)'; stratum = 'terminal'
  Prepare = { param($dir)
    $script:W5 = @{ dir = $dir }
    "Crie dois scripts no diretório $dir : definir.cmd (batch, para o cmd.exe) e definir.ps1 (PowerShell). Cada um deve definir a variável de ambiente FOO_BAR com o valor 42 e imprimir uma linha no formato FOO_BAR=valor, lendo o valor da própria variável. Depois execute os dois e responda em uma linha com a saída de cada um. Formato: cmd=<saída>; ps1=<saída>" }
  Gold = { $d = $script:W5.dir
    [IO.File]::WriteAllText("$d\definir.cmd", "@echo off`r`nset FOO_BAR=42`r`necho FOO_BAR=%FOO_BAR%`r`n", [Text.Encoding]::ASCII)
    Write-TermText "$d\definir.ps1" "`$env:FOO_BAR = 42`nWrite-Output `"FOO_BAR=`$env:FOO_BAR`"`n"
    'cmd=FOO_BAR=42; ps1=FOO_BAR=42' }
  Score = { param($a)
    $d = $script:W5.dir; $ok = $true
    $cmd = "$d\definir.cmd"; $ps1 = "$d\definir.ps1"
    $ok = $ok -and (Test-Path $cmd) -and (Test-Path $ps1)
    if ($ok) {
      $tc = [IO.File]::ReadAllText($cmd); $tp = [IO.File]::ReadAllText($ps1)
      $ok = $ok -and ($tc -match '%FOO_BAR%|!FOO_BAR!')
      $ok = $ok -and ($tp -match '(?i)\$env:foo_bar|getenvironmentvariable\(\s*.foo_bar')
      $rc = Invoke-TermProc $env:ComSpec @('/c', $cmd)
      $rp = if ($script:TermPsExe) { Invoke-TermProc $script:TermPsExe @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $ps1) } else { $null }
      $ok = $ok -and ($rc.code -eq 0) -and ($rc.out -match 'FOO_BAR=42')
      if ($rp) { $ok = $ok -and ($rp.code -eq 0) -and ($rp.out -match 'FOO_BAR=42') } else { Write-TermSkip 'W5: sem pwsh/powershell, oráculo só dos arquivos e da resposta' }
    }
    $ok = $ok -and (Test-TermField $a 'cmd' 'FOO_BAR=42') -and (Test-TermField $a 'ps1' 'FOO_BAR=42')
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W6 - arquivo de 80 MB com 3 linhas de texto em posições conhecidas
# ---------------------------------------------------------------------------
function Write-TermZeros { param($Fs, [long]$Bytes)
  $buf = New-Object byte[] 1048576
  while ($Bytes -gt 0) { $n = [int][math]::Min($Bytes, $buf.Length); $Fs.Write($buf, 0, $n); $Bytes -= $n } }

$TaskTable['W6'] = @{ name = '80 MB com 3 linhas de texto: achar as posições em bytes'; stratum = 'terminal'
  Prepare = { param($dir)
    $offs = @(7300000, 41600000, 68900000); $total = 83886080L
    # Uma marca por linha. NÃO escrever @('PMK' + (f), 'PMK' + (g), ...): a vírgula liga mais forte
    # que o '+', a lista vira UM elemento e as 3 marcas acabam concatenadas na linha 1 (as linhas 2 e 3
    # ficariam sem marca — e o oráculo, que lê o mesmo arquivo, aceitaria a fixtura quebrada).
    $tok = @(1..3 | ForEach-Object { 'PMK' + (New-TermToken 5) })
    if ($tok.Count -ne 3 -or @($tok | Where-Object { $_.Length -ne 8 }).Count) { throw "W6: marcas inválidas ($($tok -join '|'))" }
    # List[byte[]] e nao array do pipeline: o PowerShell achata byte[] em bytes soltos e so 1 byte seria gravado
    $marcas = New-Object 'System.Collections.Generic.List[byte[]]'
    for ($i = 0; $i -lt 3; $i++) { $marcas.Add([Text.Encoding]::UTF8.GetBytes("PONTO-MARCADO $($i+1) $($tok[$i])`n")) }
    $full = Join-Path $dir 'disco.img'
    $fs = [IO.File]::Open($full, 'Create', 'Write')
    try {
      $pos = 0L
      for ($i = 0; $i -lt 3; $i++) { Write-TermZeros $fs ($offs[$i] - $pos); $fs.Write($marcas[$i], 0, $marcas[$i].Length); $pos = $offs[$i] + $marcas[$i].Length }
      Write-TermZeros $fs ($total - $pos)
    } finally { $fs.Close() }
    $script:W6 = @{ file = $full; offs = $offs; tok = $tok }
    "O arquivo $full tem 80 MB e é quase todo zeros, mas contém 3 linhas de texto começando por PONTO-MARCADO, em posições diferentes. Descubra o texto exato de cada linha e a posição em bytes onde cada uma começa (a posição do primeiro byte da linha). Não carregue o arquivo inteiro na conversa. Formato: linha1=<texto exato>@<posição>; linha2=...; linha3=..." }
  Gold = { $w = $script:W6
    (1..3 | ForEach-Object { "linha$_=PONTO-MARCADO $_ $($w.tok[$_-1])@$($w.offs[$_-1])" }) -join '; ' }
  Score = { param($a)
    $w = $script:W6; $ok = $true
    for ($i = 1; $i -le 3; $i++) {
      $t = $w.tok[$i - 1]; $off = $w.offs[$i - 1]
      $ok = $ok -and ($a -match ('(?i)linha' + $i + '\s*[=:]\s*"?[^\r\n;]*' + [regex]::Escape($t) + '\s*@\s*' + $off + '(?![\d])'))
      $buf = New-Object byte[] 64
      $fs = [IO.File]::Open($w.file, 'Open', 'Read'); try { [void]$fs.Seek($off, 'Begin'); [void]$fs.Read($buf, 0, 64) } finally { $fs.Close() }
      $ok = $ok -and ([Text.Encoding]::UTF8.GetString($buf).StartsWith("PONTO-MARCADO $i $t"))
    }
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W7 - erro de compilação em cadeia num projeto Go de 6 arquivos
# ---------------------------------------------------------------------------
$script:TermGoExe = $(if (Test-TermRunnable 'go' @('version')) { (Get-Command go).Source } else { '' })

# Fontes quebradas (3 erros, um por pacote, revelados em cadeia: calc -> relatorio -> main)
function Get-TermW7Broken {
  @{
    'go.mod' = "module exemplo.local/relatorio`n`ngo 1.21`n"
    'itens/itens.go' = @'
package itens

// Item é um produto do catálogo, com o preço em centavos.
type Item struct {
	Nome  string
	Preco int
}

// Catalogo devolve o catálogo fixo usado pelo relatório.
func Catalogo() []Item {
	return []Item{
		{Nome: "Caneta", Preco: 250},
		{Nome: "Caderno", Preco: 1890},
		{Nome: "Mochila", Preco: 12990},
		{Nome: "Estojo", Preco: 3450},
		{Nome: "Regua", Preco: 780},
	}
}
'@
    'calc/soma.go' = @'
package calc

import "exemplo.local/relatorio/itens"

// Somar devolve a soma dos preços da lista.
func Somar(lista []itens.Item) (int, error) {
	if len(lista) == 0 {
		return 0, ErrVazio
	}
	total := 0
	for _, it := range lista {
		total += it.Valor
	}
	return total, nil
}
'@
    'calc/erros.go' = @'
package calc

import "errors"

// ErrVazio indica lista sem itens.
var ErrVazio = errors.New("catalogo vazio")
'@
    'relatorio/relatorio.go' = @'
package relatorio

import (
	"exemplo.local/relatorio/calc"
	"exemplo.local/relatorio/itens"
)

// Relatorio é o resumo do catálogo.
type Relatorio struct {
	Titulo string
	Total  int
}

// Novo monta o relatório a partir da lista de itens.
func Novo(lista []itens.Item, titulo string) (*Relatorio, error) {
	total := calc.Somar(lista)
	return &Relatorio{Titulo: titulo, Total: total}, nil
}
'@
    'relatorio/formato.go' = @'
package relatorio

import "strconv"

// Linha devolve a linha final de saída do relatório.
func (r *Relatorio) Linha() string {
	return "total=" + strconv.Itoa(r.Total)
}
'@
    'main.go' = @'
package main

import (
	"fmt"

	"exemplo.local/relatorio/itens"
	"exemplo.local/relatorio/relatorio"
)

func main() {
	rel, err := relatorio.Novo(itens.Catalogo())
	if err != nil {
		panic(err)
	}
	fmt.Println(rel.Linha())
}
'@
  }
}
# Fontes corrigidos: mesma semântica, os 3 erros resolvidos.
function Get-TermW7Fixed {
  $f = Get-TermW7Broken
  $f['calc/soma.go'] = $f['calc/soma.go'].Replace('total += it.Valor', 'total += it.Preco')
  $f['relatorio/relatorio.go'] = $f['relatorio/relatorio.go'].Replace(
    "`ttotal := calc.Somar(lista)`n`treturn &Relatorio{Titulo: titulo, Total: total}, nil",
    "`ttotal, err := calc.Somar(lista)`n`tif err != nil {`n`t`treturn nil, err`n`t}`n`treturn &Relatorio{Titulo: titulo, Total: total}, nil")
  $f['main.go'] = $f['main.go'].Replace('relatorio.Novo(itens.Catalogo())', 'relatorio.Novo(itens.Catalogo(), "catalogo")')
  return $f
}
function Write-TermW7 {
  param([string]$Dir, [hashtable]$Files)
  foreach ($k in $Files.Keys) {
    $full = Join-Path $Dir ($k -replace '/', '\')
    New-Item -ItemType Directory -Force (Split-Path $full -Parent) | Out-Null
    Write-TermText $full $Files[$k]
  }
}
function Invoke-TermGo {
  param([string]$Dir, [string[]]$GoArgs)
  $old = $env:GOTOOLCHAIN; $env:GOTOOLCHAIN = 'local'
  try { return (Invoke-TermProc $script:TermGoExe $GoArgs $Dir) } finally { if ($old) { $env:GOTOOLCHAIN = $old } else { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue } }
}

$TaskTable['W7'] = @{ name = 'Três erros de compilação em cadeia (Go, 6 arquivos)'; stratum = 'terminal'
  Prepare = { param($dir)
    if (-not $script:TermGoExe) {
      Write-TermSkip 'W7: sem go no host, tarefa degradada (resposta trivial)'
      $script:W7 = @{ degr = $true }
      return 'O compilador Go não está disponível neste computador. Responda apenas: indisponivel'
    }
    $proj = Join-Path $dir 'projeto'
    Write-TermW7 $proj (Get-TermW7Broken)
    $intactos = @('itens/itens.go', 'calc/erros.go', 'relatorio/formato.go', 'go.mod')
    $hashes = @{}
    foreach ($k in $intactos) { $hashes[$k] = (Get-FileHash (Join-Path $proj ($k -replace '/', '\')) -Algorithm SHA256).Hash }
    $script:W7 = @{ degr = $false; proj = $proj; hashes = $hashes; total = 19360 }
    "O projeto Go em $proj não compila: a execução de go build ./... aponta um erro de cada vez, e corrigir um revela o seguinte. Conserte até go build ./... passar e o programa (go run . na raiz do projeto) terminar imprimindo uma linha no formato total=<valor>, com a soma dos preços do catálogo. Não altere os arquivos itens/itens.go, calc/erros.go, relatorio/formato.go e go.mod: eles devem ficar byte a byte como estão. Responda apenas: feito" }
  Gold = { if ($script:W7.degr) { 'indisponivel' } else { Write-TermW7 $script:W7.proj (Get-TermW7Fixed); 'feito' } }
  Score = { param($a)
    if ($script:W7.degr) { return (Score-Bool ($a -like '*indisponivel*')) }
    $w = $script:W7
    $b = Invoke-TermGo $w.proj @('build', './...')
    $r = if ($b.code -eq 0) { Invoke-TermGo $w.proj @('run', '.') } else { @{ code = -1; out = '' } }
    $ok = ($b.code -eq 0) -and ($r.code -eq 0) -and ($r.out -match ('(?m)^total=' + $w.total + '\s*$'))
    foreach ($k in $w.hashes.Keys) {
      $p = Join-Path $w.proj ($k -replace '/', '\')
      $ok = $ok -and (Test-Path $p) -and ((Get-FileHash $p -Algorithm SHA256).Hash -eq $w.hashes[$k])
    }
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# W8 - JSON de 15 MB em uma linha só
# ---------------------------------------------------------------------------
$TaskTable['W8'] = @{ name = 'JSON de 15 MB numa linha: soma e maior id'; stratum = 'terminal'
  Prepare = { param($dir)
    $rnd = New-Object System.Random 7
    $sb = New-Object Text.StringBuilder 17000000
    [void]$sb.Append('{"itens":[')
    # Os últimos 60 itens ficam ativos de propósito: sem isso o maior id inativo costuma ser o último
    # item, e a resposta "maior_id" viraria o total de itens — adivinhável sem abrir o arquivo.
    $soma = 0L; $maxInativo = 0; $n = 392500; $caudaAtiva = 60
    for ($i = 1; $i -le $n; $i++) {
      $ativo = ($i -gt ($n - $caudaAtiva)) -or ($rnd.Next(0, 10) -lt 6); $valor = $rnd.Next(1, 10000)
      if ($ativo) { $soma += $valor } elseif ($i -gt $maxInativo) { $maxInativo = $i }
      if ($i -gt 1) { [void]$sb.Append(',') }
      [void]$sb.Append('{"id":').Append($i).Append(',"ativo":').Append($(if ($ativo) { 'true' } else { 'false' })).Append(',"valor":').Append($valor).Append('}')
    }
    [void]$sb.Append(']}')
    $full = Join-Path $dir 'dados.json'
    [IO.File]::WriteAllText($full, $sb.ToString(), $script:TermUtf8)
    $script:W8 = @{ soma = $soma; max = $maxInativo; mb = [math]::Round((Get-Item $full).Length / 1MB, 1); n = $n }
    "O arquivo $full é um JSON de $($script:W8.mb) MB escrito em uma única linha, com $n itens no formato {id, ativo, valor}. Responda em uma linha: a soma do campo valor de todos os itens com ativo verdadeiro e o maior id entre os itens com ativo falso. Formato: soma=...; maior_id=..." }
  Gold = { "soma=$($script:W8.soma); maior_id=$($script:W8.max)" }
  Score = { param($a)
    $w = $script:W8
    Score-Bool ((Test-TermField $a 'soma' ([string]$w.soma)) -and (Test-TermField $a 'maior_id' ([string]$w.max))) } }
