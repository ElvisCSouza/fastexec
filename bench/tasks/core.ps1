# Tarefas sinteticas (nenhum dado real): geradas com semente fixa, entao sao identicas em toda rodada.
# Contrato: $TaskTable[Id] = @{ name; stratum; Prepare($dir) -> prompt ; Gold() -> resposta correta (pode ter efeito colateral) ;
#                            Score($answer) -> @{correct;precision;recall} }.  Oraculo = resposta/hash, nunca autodiagnostico.

$TaskTable['T0'] = @{ name = 'Linha de base (so responde ok)'; stratum = 'baseline'
  Prepare = { param($dir) 'Responda apenas com a palavra: ok' }
  Gold = { 'ok' }
  Score = { param($a) Score-Bool ($a.Trim().Trim('.', '!', '"', "'").ToLower() -eq 'ok') } }

$TaskTable['T2'] = @{ name = 'JSON de 1 linha (700 pedidos)'; stratum = 'dados'
  Prepare = { param($dir)
    $rnd = New-Object System.Random 42
    $orders = 1..700 | ForEach-Object { $n = $rnd.Next(1, 6)
      [ordered]@{ id = ('ORD-{0:D4}' -f $_); customer = [ordered]@{ name = "Cliente $_"; email = "cliente$_@exemplo.com.br"; city = "Cidade $($rnd.Next(1, 90))" }
        status = @('paid', 'shipped', 'pending', 'refunded')[$rnd.Next(0, 4)]
        items = @(1..$n | ForEach-Object { [ordered]@{ sku = "SKU-$($rnd.Next(1000, 9999))"; qty = $rnd.Next(1, 9); price = [math]::Round($rnd.NextDouble() * 300, 2) } })
        notes = "Pedido gerado para benchmark sem significado real $_" } }
    $script:t2 = @{ email = $orders[417].customer.email; items = @($orders[417].items).Count }
    [IO.File]::WriteAllText("$dir\pedidos.json", ($orders | ConvertTo-Json -Compress -Depth 6))
    "O arquivo $dir\pedidos.json contém pedidos em JSON. Qual o e-mail do cliente do pedido ORD-0418 e quantos itens ele tem? Responda em uma linha: email; quantidade." }
  Gold = { "$($script:t2.email); $($script:t2.items)" }
  Score = { param($a) Score-Bool (($a -like "*$($script:t2.email)*") -and ($a -match "\b$($script:t2.items)\b")) } }

$TaskTable['T8'] = @{ name = 'Log repetitivo (30 mil linhas)'; stratum = 'dados'
  Prepare = { param($dir)
    $rnd = New-Object Random 8; $lines = New-Object Collections.Generic.List[string]; $t0 = [datetime]'2026-11-10T00:00:00'
    $kinds = @('DB timeout ao consultar Operacoes', 'Falha ao validar token do parceiro', 'Fila de notificacoes cheia', 'Cache miss em CalculoService')
    $w = @(7, 3, 2, 1); $first = $null; $cnt = @{}
    for ($i = 0; $i -lt 30000; $i++) {
      $ts = $t0.AddSeconds($i * 2).ToString('yyyy-MM-dd HH:mm:ss')
      if ($rnd.Next(0, 100) -lt 6) { $pool = for ($k = 0; $k -lt 4; $k++) { 1..$w[$k] | ForEach-Object { $k } }; $k = $pool[$rnd.Next(0, $pool.Count)]
        $lines.Add("$ts ERROR $($kinds[$k])"); $cnt[$k] = 1 + [int]$cnt[$k]; if ($k -eq 0 -and -not $first) { $first = $ts } }
      else { $lines.Add("$ts INFO heartbeat ok worker=$($rnd.Next(1, 3))") } }
    [IO.File]::WriteAllLines("$dir\app.log", $lines)
    $top = ($cnt.GetEnumerator() | Sort-Object Value -Descending | Select-Object -First 1)
    $script:t8 = @{ msg = $kinds[[int]$top.Key]; n = [int]$top.Value; first = $first }
    "O arquivo $dir\app.log é um log de aplicação. Qual é a mensagem de ERROR mais frequente, quantas vezes ela aparece, e qual o timestamp da primeira ocorrência de 'DB timeout'? Responda em uma linha: mensagem; quantidade; timestamp." }
  Gold = { "$($script:t8.msg); $($script:t8.n); $($script:t8.first)" }
  Score = { param($a) Score-Bool (($a -like "*$($script:t8.msg)*") -and ($a -match "\b$($script:t8.n)\b") -and ($a -like "*$($script:t8.first)*")) } }

$TaskTable['T9'] = @{ name = 'Edição em arquivo CRLF (400 linhas)'; stratum = 'edicao'
  Prepare = { param($dir)
    $sb = New-Object Text.StringBuilder
    [void]$sb.Append("namespace Demo`r`n{`r`n    public static class Config`r`n    {`r`n")
    1..300 | ForEach-Object { [void]$sb.Append("        public const int Limite$_ = $($_ * 3);`r`n") }
    [void]$sb.Append("        public const int MaxRetries = 3;`r`n")
    1..100 | ForEach-Object { [void]$sb.Append("        public const string Nome$_ = `"valor $_`";`r`n") }
    [void]$sb.Append("    }`r`n}`r`n")
    $script:t9 = @{ file = "$dir\Config.cs"; orig = $sb.ToString() }
    [IO.File]::WriteAllText($script:t9.file, $script:t9.orig, (New-Object Text.UTF8Encoding $false))
    "No arquivo $dir\Config.cs, altere o valor da constante MaxRetries de 3 para 5, mantendo todo o resto do arquivo exatamente igual (inclusive as quebras de linha). Responda apenas 'feito'." }
  Gold = { [IO.File]::WriteAllText($script:t9.file, $script:t9.orig.Replace('MaxRetries = 3;', 'MaxRetries = 5;'), (New-Object Text.UTF8Encoding $false)); 'feito' }
  Score = { param($a) Score-Bool ([IO.File]::ReadAllText($script:t9.file) -ceq $script:t9.orig.Replace('MaxRetries = 3;', 'MaxRetries = 5;')) } }

$TaskTable['T10'] = @{ name = 'Comando longo (~65 s)'; stratum = 'processo'
  Prepare = { param($dir)
    $script:t10 = 'FIM-' + (Get-Random -Minimum 100000 -Maximum 999999)
    Set-Content -Encoding utf8 "$dir\lento.ps1" -Value "`$end=(Get-Date).AddSeconds(65); while((Get-Date) -lt `$end){ Start-Sleep -Milliseconds 200 }; Write-Output 'concluido: $($script:t10)'"
    "Execute o script $dir\lento.ps1 com PowerShell (pwsh -NoProfile -File). Ele demora cerca de 1 minuto. Responda apenas com a mensagem final que ele imprime." }
  Gold = { "concluido: $($script:t10)" }
  Score = { param($a) Score-Bool ($a -like "*$($script:t10)*") } }

# ---- Falhas conhecidas (aspas, shell, encoding): o oraculo e o conteudo gravado / a resposta ----
$script:E1text = @('Relatório de São João — "aspas duplas" e ''simples''', 'Preço: R$ 1.234,56 ($valor) `crase` {chaves} [colchetes]',
  'Caminho: C:\Dados\Pasta Com Espaço\arquivo.txt', 'Regex: ^\d{3}-\d{2}$ | pipe & e-comercial; ponto-e-vírgula', 'Linha final: 100% ok!') -join "`n"
$TaskTable['E1'] = @{ name = 'Escrita com caracteres hostis'; stratum = 'falhas'
  Prepare = { param($dir); $script:e1 = "$dir\saida.txt"
    "Crie o arquivo $dir\saida.txt (UTF-8, sem BOM) com exatamente as 5 linhas abaixo, sem alterar nenhum caractere, e responda apenas 'feito'.`n`n$($script:E1text)" }
  Gold = { [IO.File]::WriteAllText($script:e1, $script:E1text, (New-Object Text.UTF8Encoding $false)); 'feito' }
  Score = { param($a) $ok = $false; if (Test-Path $script:e1) { $ok = (([IO.File]::ReadAllText($script:e1)).Replace("`r`n", "`n").TrimEnd("`n") -ceq $script:E1text) }; Score-Bool $ok } }

$TaskTable['E2'] = @{ name = 'JSON com aspas em argumento (PowerShell)'; stratum = 'falhas'
  Prepare = { param($dir)
    $j = '{"itens":[{"nome":"Maçã","qtd":3},{"nome":"Pão d''água","qtd":2},{"nome":"Café \"forte\"","qtd":7}],"obs":"valor: $10 e `x`"}'
    "Use o PowerShell (ConvertFrom-Json) para processar o JSON abaixo, passando-o como texto no comando. Responda em uma linha: soma das qtd; nome do item de maior qtd; valor do campo obs.`n`n$j" }
  Gold = { 'soma 12; Café "forte"; valor: $10 e `x`' }
  Score = { param($a) Score-Bool (($a -match '\b12\b') -and $a.Contains('Café "forte"') -and $a.Contains('valor: $10 e `x`')) } }

$TaskTable['E3'] = @{ name = 'Arquivo em Windows-1252'; stratum = 'falhas'
  Prepare = { param($dir)
    $lines = 1..40 | ForEach-Object { "Registro $_ sem acentuação relevante" }; $lines[22] = 'Informação básica: coração não é peça'; $lines[31] = 'Ação concluída: situação já resolvida'
    [IO.File]::WriteAllText("$dir\dados.txt", ($lines -join "`r`n"), [Text.Encoding]::GetEncoding(1252))
    "O arquivo $dir\dados.txt está codificado em Windows-1252. Responda em uma linha: o texto exato da linha que começa com 'Informação'; e quantas linhas contêm a letra 'ç'." }
  Gold = { 'Informação básica: coração não é peça; 40' }
  Score = { param($a) Score-Bool (($a -like '*Informação básica: coração não é peça*') -and ($a -match '\b40\b')) } }

$TaskTable['E4'] = @{ name = 'Controle sem armadilha (ASCII, 250 linhas)'; stratum = 'falhas'
  Prepare = { param($dir)
    [IO.File]::WriteAllLines("$dir\simples.txt", (1..250 | ForEach-Object { "linha $_ conteudo simples" }))
    "Quantas linhas tem o arquivo $dir\simples.txt e qual é a linha 137? Responda em uma linha: quantidade; texto." }
  Gold = { '250; linha 137 conteudo simples' }
  Score = { param($a) Score-Bool (($a -match '\b250\b') -and ($a -like '*linha 137 conteudo simples*')) } }
