# Suíte de uso de MCP multi-etapa (estrato 'mcp'), tarefas P1-P6.
# Compara apenas os braços native e fastexec. Cada Prepare gera o cenário num diretório
# temporário (sem rede externa, sem efeito fora dele) e o oráculo sai de programa, nunca
# escrito à mão nem deduzido do texto da resposta. Nada aqui chama modelo nem o provedor.
#
# Por que estas tarefas: MCP-Universe, MCPMark e MCP-Bench mostram que a dificuldade está
# em tarefas de várias etapas com saída grande e estado entre chamadas, e o ToFu atribui a
# economia de tokens a limitar a saída das ferramentas e a ler/editar vários arquivos por
# chamada. Nenhuma tarefa das outras suítes mede o ganho de ler vários arquivos de uma vez
# nem o custo de uma saída de ~1 MB:
#   P1 leitura em lote (12 configs)         P2 saída de ~1 MB (20 mil linhas)
#   P3 job longo (~50 s) com estado         P4 job em segundo plano + porta + limpeza
#   P5 busca em árvore de 3000 arquivos     P6 pipeline de 3 etapas até um JSON
# Ferramenta ausente no host degrada a tarefa com aviso documentado, em vez de quebrar o
# dry-run; o silenciador é BENCH_MCP_QUIET=1. BENCH_MCP_QUICK=1 encurta a P3 (só para
# iterar no desenvolvimento: o prompt e a faixa do oráculo acompanham a duração real).

$script:McpUtf8 = New-Object Text.UTF8Encoding $false

function Write-McpText { param([string]$Path, [string]$Text) [IO.File]::WriteAllText($Path, $Text, $script:McpUtf8) }

function Write-McpSkip {
  # Aviso de tarefa degradada. Prepare roda duas vezes no dry-run; o conjunto evita repetir a linha.
  param([string]$Msg)
  if (Test-Path Env:BENCH_MCP_QUIET) { return }
  if (-not $script:McpSkipVistos) { $script:McpSkipVistos = @{} }
  if ($script:McpSkipVistos.ContainsKey($Msg)) { return }
  $script:McpSkipVistos[$Msg] = $true
  Write-Host "  [mcp] $Msg" -ForegroundColor DarkYellow
}

function New-McpToken { param([int]$N = 6)
  -join (1..$N | ForEach-Object { 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'[(Get-Random -Maximum 32)] }) }

# Número/palavra inteira: não casa colado em dígito, ponto ou vírgula vizinhos.
function Test-McpWord { param([string]$Text, [string]$Word)
  [bool]($Text -match ('(?<![\d.,])' + [regex]::Escape($Word) + '(?![\d])')) }

# Campo rotulado: aceita "rotulo=valor" / "rotulo: valor". O valor é comparado por substring.
function Test-McpField {
  param([string]$Text, [string]$Label, [string]$Value)
  if ($Text -match ('(?i)' + [regex]::Escape($Label) + '\s*[=:]\s*"?([^\r\n;|]+)')) { return ($Matches[1] -like "*$Value*") }
  return $false }

# Campo rotulado numérico: exige IGUALDADE. Com substring, "marca=1234567" passaria por "12345".
function Test-McpNum {
  param([string]$Text, [string]$Label, [long]$Value)
  if ($Text -match ('(?i)' + [regex]::Escape($Label) + '\s*[=:]\s*"?\s*(\d+)')) { return ([long]$Matches[1] -eq $Value) }
  return $false }

# Uma linha da resposta que traga o caminho completo E o número da linha pedido.
function Test-McpPathLine {
  param([string]$Answer, [string]$Path, [long]$Line)
  foreach ($l in ($Answer -split "`r?`n")) {
    $x = $l -replace '/', '\'
    if ($x.IndexOf($Path, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
      if ($x -match ('(?<![\d.])' + $Line + '(?![\d])')) { return $true } } }
  return $false }

# Executa um processo externo sem passar pelo shell do harness: argumento vai como argumento,
# nunca reinterpretado. Sai com os dois streams e o código.
function Invoke-McpProc {
  param([string]$Exe, [string[]]$ArgList = @(), [string]$Dir = $null)
  $psi = New-Object Diagnostics.ProcessStartInfo
  $psi.FileName = $Exe
  if ($psi.PSObject.Properties['ArgumentList']) { foreach ($a in $ArgList) { [void]$psi.ArgumentList.Add($a) } }
  else { $psi.Arguments = (($ArgList | ForEach-Object { '"' + ($_ -replace '"', '\"') + '"' }) -join ' ') }
  $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true
  $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
  if ($Dir) { $psi.WorkingDirectory = $Dir }
  try {
    $p = [Diagnostics.Process]::Start($psi)
    $o = $p.StandardOutput.ReadToEnd(); $e = $p.StandardError.ReadToEnd(); $p.WaitForExit()
    return @{ code = $p.ExitCode; out = $o; err = $e }
  } catch { return @{ code = -1; out = ''; err = $_.Exception.Message } }
}

function Stop-McpProc { param([int]$ProcId)
  if ($ProcId -gt 0) { [void](Invoke-McpProc 'taskkill' @('/PID', [string]$ProcId, '/T', '/F')) } }

# Porta alta livre: o SO escolhe, devolvemos e fechamos. A janela de corrida é mínima.
function Get-McpFreePort {
  $l = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
  $l.Start(); $p = [int]$l.LocalEndpoint.Port; $l.Stop(); return $p }

function Test-McpPort { param([int]$Port)
  $c = New-Object Net.Sockets.TcpClient
  try { $c.Connect('127.0.0.1', $Port); return $true } catch { return $false } finally { $c.Close() } }

function Get-McpPortOwners {
  # Quem ficou escutando: o Score precisa saber antes de matar, e matar sempre.
  param([int]$Port)
  try { return @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
      Select-Object -ExpandProperty OwningProcess -Unique) } catch { return @() } }

function Stop-McpPort { param([int]$Port)
  foreach ($own in (Get-McpPortOwners $Port)) { Stop-McpProc ([int]$own) }
  for ($i = 0; $i -lt 20 -and (Test-McpPort $Port); $i++) { Start-Sleep -Milliseconds 100 } }

function Invoke-McpHttp {
  param([string]$Url, [int]$TimeoutMs = 6000)
  try {
    $r = [Net.HttpWebRequest]::Create($Url); $r.Proxy = $null
    $r.Timeout = $TimeoutMs; $r.ReadWriteTimeout = $TimeoutMs
    $resp = $r.GetResponse()
    try {
      $sr = New-Object IO.StreamReader($resp.GetResponseStream(), [Text.Encoding]::UTF8)
      return $sr.ReadToEnd().Trim()
    } finally { $resp.Close() }
  } catch { return '' } }

# pwsh é o que o próprio bench usa; sem ele as tarefas que executam script degradam.
$script:McpPsExe = $(if (Get-Command pwsh -ErrorAction SilentlyContinue) { 'pwsh' }
  elseif (Get-Command powershell -ErrorAction SilentlyContinue) { 'powershell' } else { '' })

# ---------------------------------------------------------------------------
# P1 - leitura em lote: o valor de `timeout` em 12 arquivos de configuração
# ---------------------------------------------------------------------------
$TaskTable['P1'] = @{ name = 'Leitura em lote: timeout em 12 arquivos de configuração'; stratum = 'mcp'
  Prepare = { param($dir)
    $sub = Join-Path $dir 'config'
    New-Item -ItemType Directory -Force $sub | Out-Null
    $rnd = New-Object System.Random 31337
    # Cada arquivo tem uma opção chamada exatamente `timeout` e uma isca de nome parecido:
    # quem casa por substring em "timeout" acerta a isca e erra o valor.
    $formas = @('yaml', 'json', 'ini')
    $itens = @()
    for ($i = 1; $i -le 12; $i++) {
      $nome = ('servico{0:D2}.{1}' -f $i, $formas[($i - 1) % 3])
      $to = $rnd.Next(5, 300); $tent = $rnd.Next(2, 6); $leitura = $rnd.Next(600, 1200); $ms = $to * 1000
      $full = Join-Path $sub $nome
      switch ($formas[($i - 1) % 3]) {
        'yaml' { Write-McpText $full ("servico: pedidos$i`nrede:`n  tentativas: $tent`n  timeout: $to`n  read_timeout: $leitura`n") }
        'json' { Write-McpText $full ('{"servico":"pedidos' + $i + '","rede":{"tentativas":' + $tent + ',"timeout":' + $to + ',"read_timeout":' + $leitura + '},"timeout_ms":' + $ms + '}') }
        'ini'  { Write-McpText $full "[servico]`nnome = pedidos$i`n`n[rede]`ntentativas = $tent`ntimeout = $to`ntimeout_ms = $ms`n" }
      }
      $itens += [pscustomobject]@{ nome = $nome; valor = $to }
    }
    # O oráculo não confia na tabela acima: relê os 12 arquivos do disco e extrai o `timeout` de cada um.
    $script:P1 = @{ dir = $sub; itens = @(Get-McpP1Prova $sub) }
    if ($script:P1.itens.Count -ne 12) { throw "P1: oráculo leu $($script:P1.itens.Count) arquivos, esperava 12" }
    for ($i = 0; $i -lt 12; $i++) {
      if ($script:P1.itens[$i].nome -ne $itens[$i].nome -or [int]$script:P1.itens[$i].valor -ne [int]$itens[$i].valor) {
        throw "P1: oráculo divergiu da fixture em $($itens[$i].nome)" } }
    "Na pasta $sub há 12 arquivos de configuração em três formatos diferentes (YAML, JSON e INI). Descubra o valor da opção chamada exatamente ``timeout`` em cada arquivo — as opções de nome parecido, como ``read_timeout`` e ``timeout_ms``, não valem. Responda uma linha por arquivo, no formato nome_do_arquivo=valor, em qualquer ordem." }
  Gold = { (($script:P1.itens | ForEach-Object { "$($_.nome)=$($_.valor)" }) -join "`n") }
  Score = { param($a)
    $ok = $true
    foreach ($it in $script:P1.itens) {
      $re = '(?im)^\s*' + [regex]::Escape($it.nome) + '\s*[=:]\s*"?\s*' + $it.valor + '\s*"?\s*$'
      $ok = $ok -and ($a -match $re) }
    Score-Bool $ok } }

# Extrai nome e `timeout` de cada arquivo da pasta, lendo o que está no disco.
function Get-McpP1Prova {
  param([string]$Pasta)
  $out = @()
  foreach ($f in (Get-ChildItem $Pasta -File | Sort-Object Name)) {
    $t = [IO.File]::ReadAllText($f.FullName)
    $v = $null
    if ($f.Extension -eq '.json') { $v = (($t | ConvertFrom-Json).rede).timeout }
    elseif ($f.Extension -eq '.yaml') {
      foreach ($l in ($t -split "`r?`n")) { if ($l -match '^\s+timeout:\s*(\d+)\s*$') { $v = [int]$Matches[1] } } }
    else {
      foreach ($l in ($t -split "`r?`n")) { if ($l -match '^\s*timeout\s*=\s*(\d+)\s*$') { $v = [int]$Matches[1] } } }
    if ($null -eq $v) { throw "P1: não achei timeout em $($f.Name)" }
    $out += [pscustomobject]@{ nome = $f.Name; valor = [int]$v } }
  return $out }

# ---------------------------------------------------------------------------
# P2 - saída de ~1 MB: a linha MARCA-7F3A e a soma dos números divisíveis por 10
# ---------------------------------------------------------------------------
$script:McpP2Tpl = @'
$n = 20000
$marca = __MARCA__
for ($i = 1; $i -le $n; $i++) {
  $x = (($i * 7919) % 90000) + 10000
  if ($x -eq $marca) { Write-Output "$x :: MARCA-7F3A" }
  else { Write-Output "$x :: modulo $x compilado com sucesso; bloco $($i % 97); sem avisos" }
}
'@

$TaskTable['P2'] = @{ name = 'Saída de ~1 MB (20 mil linhas): achar a marca e somar'; stratum = 'mcp'
  Prepare = { param($dir)
    if (-not $script:McpPsExe) {
      Write-McpSkip 'P2: sem pwsh/powershell, tarefa degradada (resposta trivial)'
      $script:P2 = @{ degr = $true }
      return 'O interpretador PowerShell não está disponível neste computador. Responda apenas: indisponivel'
    }
    # O número impresso em cada linha não é a posição: é um passeio determinista (passo 7919 primo
    # com 90000), então a soma não sai de uma fórmula de 1..20000 decorada. A marca só é achável
    # executando o script, porque a posição dela é que escolhe o número.
    $i0 = (Get-Random -Minimum 3000 -Maximum 17001)
    $k = (($i0 * 7919) % 90000) + 10000
    $nums = 1..20000 | ForEach-Object { (($_ * 7919) % 90000) + 10000 }
    if (@($nums | Where-Object { $_ -eq $k }).Count -ne 1) { throw "P2: marca $k não é única" }
    $soma = 0L; foreach ($x in $nums) { if ($x % 10 -eq 0) { $soma += $x } }
    $script:P2 = @{ degr = $false; k = $k; soma = $soma; i0 = $i0; linhas = 20000; min_bytes = 1048576 }
    Write-McpText (Join-Path $dir 'gerar.ps1') ($script:McpP2Tpl.Replace('__MARCA__', [string]$k))
    # Prova física da fixture: executa o gerador e confere tamanho, contagem e a linha da marca.
    $r = Invoke-McpProc $script:McpPsExe @('-NoProfile', '-File', (Join-Path $dir 'gerar.ps1'))
    $bytes = [Text.Encoding]::UTF8.GetByteCount($r.out)
    $ls = @($r.out -split "`r?`n" | Where-Object { $_ -ne '' })
    $comMarca = @($ls | Where-Object { $_ -match 'MARCA-7F3A' })
    if ($r.code -ne 0) { throw "P2: gerador saiu com código $($r.code): $($r.err)" }
    if ($bytes -lt $script:P2.min_bytes) { throw "P2: saída de $bytes bytes, esperava >= $($script:P2.min_bytes)" }
    if ($ls.Count -ne 20000) { throw "P2: gerador imprimiu $($ls.Count) linhas, esperava 20000" }
    if ($comMarca.Count -ne 1) { throw "P2: $($comMarca.Count) linhas com MARCA-7F3A, esperava 1" }
    if (-not ($comMarca[0] -match "^$k :: MARCA-7F3A$")) { throw "P2: linha da marca é '$($comMarca[0])', esperava '$k :: MARCA-7F3A'" }
    "Execute o script $dir\gerar.ps1 com PowerShell (pwsh -NoProfile -File): ele imprime cerca de 1 MB, em 20 mil linhas no formato ``número :: texto``. Uma das linhas tem o texto exatamente MARCA-7F3A. Responda em uma linha: o número dessa linha e a soma dos números de todas as linhas cujo número é divisível por 10. Formato: marca=...; soma=..." }
  Gold = { if ($script:P2.degr) { 'indisponivel' } else { "marca=$($script:P2.k); soma=$($script:P2.soma)" } }
  Score = { param($a)
    if ($script:P2.degr) { return (Score-Bool ($a -like '*indisponivel*')) }
    Score-Bool ((Test-McpNum $a 'marca' $script:P2.k) -and (Test-McpNum $a 'soma' $script:P2.soma)) } }

# ---------------------------------------------------------------------------
# P3 - job longo (~50 s) que imprime progresso: resultado final e tempo
# ---------------------------------------------------------------------------
$TaskTable['P3'] = @{ name = 'Job longo (~50 s) com progresso: resultado e tempo'; stratum = 'mcp'
  Prepare = { param($dir)
    if (-not $script:McpPsExe) {
      Write-McpSkip 'P3: sem pwsh/powershell, tarefa degradada (resposta trivial)'
      $script:P3 = @{ degr = $true }
      return 'O interpretador PowerShell não está disponível neste computador. Responda apenas: indisponivel'
    }
    # ~50 s de paredão com saída pequena: o custo não é o tamanho, é o tempo. Cada passo é de 4 s
    # para o progresso ser observável sem inundar a saída.
    $sec = if (Test-Path Env:BENCH_MCP_QUICK) { 2 } else { 50 }
    $tok = 'JOB-' + (New-McpToken 6)
    $body = @'
$fim = (Get-Date).AddSeconds(__SEC__)
$n = 0
while ((Get-Date) -lt $fim) {
  Start-Sleep -Milliseconds 500
  $n++
  if ($n % 8 -eq 0) {
    $pct = [int][math]::Round(100 * (1 - (($fim - (Get-Date)).TotalSeconds / __SEC__)))
    if ($pct -lt 0) { $pct = 0 }; if ($pct -gt 99) { $pct = 99 }
    Write-Output "progresso: $pct%"
  }
}
Write-Output "resultado final: __TOK__"
'@
    $body = $body.Replace('__SEC__', [string]$sec).Replace('__TOK__', $tok)
    Write-McpText (Join-Path $dir 'processar.ps1') $body
    $script:P3 = @{ degr = $false; tok = $tok; sec = $sec; script = (Join-Path $dir 'processar.ps1')
      min = [math]::Max(1, $sec - 12); max = $sec + 60 }
    "Execute o script $dir\processar.ps1 com PowerShell (pwsh -NoProfile -File). Ele demora cerca de $sec segundos e imprime o progresso enquanto roda. Responda em uma linha: o resultado final da última linha impressa e o tempo aproximado que o comando levou, em segundos. Formato: resultado=...; segundos=..." }
  Gold = { if ($script:P3.degr) { return 'indisponivel' }
    # O gabarito do tempo é o tempo REAL desta execução, não o nominal: o dry-run mede o script de verdade.
    $t0 = Get-Date
    $r = Invoke-McpProc $script:McpPsExe @('-NoProfile', '-File', $script:P3.script)
    $el = [int][math]::Round(((Get-Date) - $t0).TotalSeconds)
    if ($r.out -notmatch [regex]::Escape($script:P3.tok)) { throw "P3: execução de prova não imprimiu $($script:P3.tok)" }
    $script:P3.real = $el
    "resultado=resultado final: $($script:P3.tok); segundos=$el" }
  Score = { param($a)
    if ($script:P3.degr) { return (Score-Bool ($a -like '*indisponivel*')) }
    $w = $script:P3
    $ok = Test-McpField $a 'resultado' $w.tok
    $dentro = $false
    if ($a -match '(?i)segundos?\s*[=:]\s*"?\s*(\d+)') { $s = [int]$Matches[1]; $dentro = ($s -ge $w.min) -and ($s -le $w.max) }
    # "cerca de 1 minuto" é resposta legítima: a faixa em segundos não pega, mas o tempo nominal pega.
    if (-not $dentro -and $w.sec -ge 50 -and $a -match '(?i)\bminuto') { $dentro = $true }
    Score-Bool ($ok -and $dentro) } }

# ---------------------------------------------------------------------------
# P4 - job em segundo plano com estado: servidor HTTP na porta do arquivo
# ---------------------------------------------------------------------------
# Servidor de prova (só para o Gold/dry-run): TcpListener cru, sem HttpListener, que exige
# reserva de URL e falha sem elevação. Vai por -EncodedCommand, então não escreve arquivo.
$script:McpSrvTpl = @'
$porta = __PORT__
$l = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $porta)
$l.Start()
while ($true) {
  $c = $l.AcceptTcpClient()
  try {
    $s = $c.GetStream()
    $sr = New-Object IO.StreamReader($s)
    $req = $sr.ReadLine()
    while ($true) { $h = $sr.ReadLine(); if (-not $h) { break } }
    $rota = ($req -split ' ')[1]
    $corpo = 'erro=rota'
    if ($rota -eq '/status') { $corpo = 'estado=pronto' }
    elseif ($rota -like '/soma*') {
      $a = 0; $b = 0
      foreach ($kv in ((($rota -split '\?')[1]) -split '&')) {
        $par = $kv -split '='; if ($par[0] -eq 'a') { $a = [int]$par[1] }; if ($par[0] -eq 'b') { $b = [int]$par[1] }
      }
      $corpo = 'soma=' + ($a + $b)
    }
    elseif ($rota -like '/eco/*') { $corpo = 'eco=' + ($rota -split '/')[-1] }
    $body = [Text.Encoding]::UTF8.GetBytes($corpo)
    $head = [Text.Encoding]::ASCII.GetBytes("HTTP/1.1 200 OK`r`nContent-Type: text/plain; charset=utf-8`r`nContent-Length: " + $body.Length + "`r`nConnection: close`r`n`r`n")
    $s.Write($head, 0, $head.Length); $s.Write($body, 0, $body.Length); $s.Flush()
  } finally { $c.Close() }
}
'@

function Start-McpGoldServer {
  param([int]$Port)
  if (-not $script:McpPsExe) { return $null }
  $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($script:McpSrvTpl.Replace('__PORT__', [string]$Port)))
  $psi = New-Object Diagnostics.ProcessStartInfo
  $psi.FileName = $script:McpPsExe
  if ($psi.PSObject.Properties['ArgumentList']) { foreach ($a in @('-NoProfile', '-EncodedCommand', $enc)) { [void]$psi.ArgumentList.Add($a) } }
  else { $psi.Arguments = "-NoProfile -EncodedCommand $enc" }
  $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
  $psi.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
  return [Diagnostics.Process]::Start($psi) }

$TaskTable['P4'] = @{ name = 'Servidor em segundo plano: 3 rotas e limpeza do processo'; stratum = 'mcp'
  Prepare = { param($dir)
    $porta = Get-McpFreePort
    Write-McpText (Join-Path $dir 'porta.txt') "$porta`n"
    $a = Get-Random -Minimum 10 -Maximum 90; $b = Get-Random -Minimum 10 -Maximum 90
    $tok = 'ECO-' + (New-McpToken 6)
    $script:P4 = @{ dir = $dir; porta = $porta; a = $a; b = $b; tok = $tok
      status = 'estado=pronto'; soma = "soma=$($a + $b)"; eco = "eco=$tok" }
    "Crie um servidor HTTP mínimo que escute em 127.0.0.1 na porta registrada no arquivo $dir\porta.txt (use exatamente essa porta) e responda: GET /status com o corpo exato estado=pronto; GET /soma?a=X&b=Y com o corpo soma=<X mais Y>; e GET /eco/<texto> com o corpo eco=<texto>. Inicie o servidor em segundo plano, faça as três requisições — /status, /soma?a=$a&b=$b e /eco/$tok — e depois encerre o servidor. Responda em uma linha com os três corpos: status=...; soma=...; eco=..." }
  Gold = { $w = $script:P4
    if (-not $script:McpPsExe) { Write-McpSkip 'P4: sem pwsh/powershell, oráculo não sobe o servidor de prova'; return 'status=; soma=; eco=' }
    $p = Start-McpGoldServer $w.porta
    try {
      for ($i = 0; $i -lt 60 -and -not (Test-McpPort $w.porta); $i++) { Start-Sleep -Milliseconds 100 }
      if (-not (Test-McpPort $w.porta)) { throw 'P4: servidor de prova não subiu' }
      $st = Invoke-McpHttp "http://127.0.0.1:$($w.porta)/status"
      $so = Invoke-McpHttp "http://127.0.0.1:$($w.porta)/soma?a=$($w.a)&b=$($w.b)"
      $ec = Invoke-McpHttp "http://127.0.0.1:$($w.porta)/eco/$($w.tok)"
      "status=$st; soma=$so; eco=$ec"
    } finally { if ($p) { Stop-McpProc $p.Id }; Stop-McpPort $w.porta } }
  Score = { param($a)
    $w = $script:P4
    $ok = (Test-McpField $a 'status' $w.status) -and (Test-McpField $a 'soma' $w.soma) -and (Test-McpField $a 'eco' $w.eco)
    # Estado físico: sobrou servidor escutando = a tarefa não terminou. Confere ANTES de matar, e mata sempre.
    $sobrou = Test-McpPort $w.porta
    try { Score-Bool ($ok -and (-not $sobrou)) } finally { Stop-McpPort $w.porta } } }

# ---------------------------------------------------------------------------
# P5 - busca em árvore de 3000 arquivos
# ---------------------------------------------------------------------------
$TaskTable['P5'] = @{ name = 'Busca em árvore de 3000 arquivos'; stratum = 'mcp'
  Prepare = { param($dir)
    $rnd = New-Object System.Random 1138
    $raiz = Join-Path $dir 'arvore'
    $palavras = @('alpha', 'bravo', 'carga', 'delta', 'eco', 'farol', 'gama', 'hotel', 'item', 'julho', 'kilo', 'lima', 'mike', 'novo', 'onda', 'papa')
    for ($b = 0; $b -lt 10; $b++) { for ($s = 0; $s -lt 6; $s++) { New-Item -ItemType Directory -Force (Join-Path $raiz ("bucket$b\sub$s")) | Out-Null } }
    # 4 alvos e 6 iscas, escolhidos por sorteio semeado. As iscas usam ZX-1139 e ZX1138 (sem hífen):
    # NÃO usar ZX-11380, que contém ZX-1138 e viraria alvo.
    $alvos = @{}; while ($alvos.Count -lt 4) { $alvos[(Get-Random -Minimum 0 -Maximum 3000)] = $true }
    $iscas = @{}
    while ($iscas.Count -lt 6) { $x = Get-Random -Minimum 0 -Maximum 3000; if (-not $alvos.ContainsKey($x) -and -not $iscas.ContainsKey($x)) { $iscas[$x] = $true } }
    $isqueiros = @('ZX-1139', 'ZX1138', 'ZX-113B', 'ZX-1139', 'ZX1138', 'ZX-113B')
    $iscaDe = @{}; $j = 0
    foreach ($k in $iscas.Keys) { $iscaDe[$k] = $isqueiros[$j % 6]; $j++ }
    $hits = @()
    for ($i = 0; $i -lt 3000; $i++) {
      $p = Join-Path $raiz ("bucket$($i % 10)\sub$([math]::Floor($i / 10) % 6)")
      $nome = 'arquivo{0:D4}.txt' -f $i
      $full = Join-Path $p $nome
      $linha = if ($alvos.ContainsKey($i)) { Get-Random -Minimum 4 -Maximum 28 } else { 0 }
      $linhaIsca = if ($iscas.ContainsKey($i)) { Get-Random -Minimum 4 -Maximum 28 } else { 0 }
      $sb = New-Object Text.StringBuilder
      for ($l = 1; $l -le 32; $l++) {
        $w1 = $palavras[$rnd.Next(16)]; $w2 = $palavras[$rnd.Next(16)]; $num = $rnd.Next(1000, 9999)
        # Parênteses em volta do -f: dentro de chamada de método a vírgula separa argumentos,
        # então sem eles o `-f` recebe um só item e a formatação estoura.
        if ($l -eq $linha) { [void]$sb.AppendLine(("linha {0:D2} do arquivo {1:D4}: codigo ZX-1138 detectado" -f $l, $i)) }
        elseif ($l -eq $linhaIsca) { [void]$sb.AppendLine(("linha {0:D2} do arquivo {1:D4}: referencia {2} arquivada" -f $l, $i, $iscaDe[$i])) }
        else { [void]$sb.AppendLine(("linha {0:D2} do arquivo {1:D4}: {2} {3} {4}" -f $l, $i, $w1, $num, $w2)) } }
      Write-McpText $full $sb.ToString()
      if ($linha -gt 0) { $hits += @{ arquivo = $full; linha = $linha } }
    }
    # Prova física: varre a árvore no disco, independente da tabela acima.
    $achados = @()
    foreach ($f in (Get-ChildItem $raiz -Recurse -File -Filter *.txt)) {
      $linhas = [IO.File]::ReadAllLines($f.FullName)
      for ($l = 0; $l -lt $linhas.Count; $l++) { if ($linhas[$l].Contains('ZX-1138')) { $achados += @{ arquivo = $f.FullName; linha = $l + 1 } } } }
    if ($achados.Count -ne 4) { throw "P5: varredura achou $($achados.Count) arquivos com ZX-1138, esperava 4" }
    foreach ($h in $hits) {
      if (-not @($achados | Where-Object { $_.arquivo -eq $h.arquivo -and $_.linha -eq $h.linha }).Count) {
        throw "P5: fixture divergiu do oráculo em $($h.arquivo)" } }
    $script:P5 = @{ raiz = $raiz; hits = $hits }
    "Na árvore $raiz há 3000 arquivos .txt pequenos distribuídos em subpastas. Exatamente 4 deles contêm a sequência ZX-1138. Informe, uma ocorrência por linha, o caminho completo do arquivo e o número da linha em que a sequência aparece, no formato caminho=número_da_linha. Não altere nenhum arquivo." }
  Gold = { (($script:P5.hits | ForEach-Object { "$($_.arquivo)=$($_.linha)" }) -join "`n") }
  Score = { param($a)
    $ok = $true
    foreach ($h in $script:P5.hits) { $ok = $ok -and (Test-McpPathLine $a $h.arquivo $h.linha) }
    # Recall sozinho não basta: apontar 20 arquivos "achando" os 4 certos não é acerto.
    $linhas = @($a -split "`r?`n" | Where-Object { $_ -match '(?i)(arquivo\d{4}\.txt|\.txt).*[=:]\s*\d+' })
    $ok = $ok -and ($linhas.Count -eq 4)
    Score-Bool $ok } }

# ---------------------------------------------------------------------------
# P6 - pipeline de 3 etapas: CSV -> filtro -> agregação -> JSON
# ---------------------------------------------------------------------------
$TaskTable['P6'] = @{ name = 'Pipeline CSV -> filtro -> agregação -> JSON'; stratum = 'mcp'
  Prepare = { param($dir)
    $rnd = New-Object System.Random 6060
    $cats = @('pedidos', 'faturas', 'estoque', 'clientes', 'relatorios', 'alertas')
    $somas = @{}; foreach ($c in $cats) { $somas[$c] = 0L }
    $linhas = New-Object Collections.Generic.List[string]
    $linhas.Add('id,categoria,valor,ativo')
    for ($i = 1; $i -le 5000; $i++) {
      $c = $cats[$rnd.Next(0, 6)]
      # valor = 500 exato em algumas linhas: o critério é "maior que 500", então elas ficam de fora.
      $v = if ($i % 37 -eq 0) { 500 } else { $rnd.Next(50, 2000) }
      $at = if ($rnd.Next(0, 100) -lt 70) { 'sim' } else { 'nao' }
      $linhas.Add("$i,$c,$v,$at")
      if ($at -eq 'sim' -and $v -gt 500) { $somas[$c] += $v } }
    $csv = Join-Path $dir 'entrada.csv'
    [IO.File]::WriteAllLines($csv, $linhas, $script:McpUtf8)
    # Prova física: recalcula as somas lendo o CSV do disco, em vez de confiar no laço acima.
    $conf = @{}; foreach ($c in $cats) { $conf[$c] = 0L }
    foreach ($l in (Get-Content $csv | Select-Object -Skip 1)) {
      $p = $l -split ','
      if ($p[3] -eq 'sim' -and [int]$p[2] -gt 500) { $conf[$p[1]] += [int]$p[2] } }
    foreach ($c in $cats) { if ($conf[$c] -ne $somas[$c]) { throw "P6: oráculo divergiu da fixture em $c" } }
    if (@($somas.Values | Where-Object { $_ -eq 0 }).Count) { throw 'P6: categoria sem nenhuma linha aprovada (gabarito ambíguo)' }
    $script:P6 = @{ dir = $dir; somas = $somas }
    "O arquivo $csv tem 5000 linhas de dados, com a primeira linha de cabeçalho e as colunas id,categoria,valor,ativo. Considere apenas as linhas em que ativo é sim e valor é maior que 500. Some o valor por categoria e grave o resultado em $dir\saida.json como um único objeto JSON, em que cada chave é o nome da categoria e o valor é a soma correspondente. Responda apenas: feito" }
  Gold = { $w = $script:P6
    [IO.File]::WriteAllText((Join-Path $w.dir 'saida.json'), ($w.somas | ConvertTo-Json -Compress), $script:McpUtf8)
    'feito' }
  Score = { param($a)
    $w = $script:P6; $f = Join-Path $w.dir 'saida.json'
    if (-not (Test-Path $f)) { return (Score-Bool $false) }
    try { $o = (Get-Content $f -Raw -Encoding UTF8) | ConvertFrom-Json } catch { return (Score-Bool $false) }
    $got = @{}
    try { foreach ($p in $o.PSObject.Properties) { $got[$p.Name] = [long]$p.Value } } catch { return (Score-Bool $false) }
    $ok = ($got.Count -eq $w.somas.Count)
    foreach ($c in $w.somas.Keys) { $ok = $ok -and $got.ContainsKey($c) -and ($got[$c] -eq $w.somas[$c]) }
    Score-Bool $ok } }
