# Tarefas que exercitam a proposta de valor do FastExec: saida gigante e falha que se resolve tentando de novo.

$TaskTable['X1'] = @{ name = 'Saída gigante com erro no final (20 mil linhas)'; stratum = 'saida'
  Prepare = { param($dir)
    $script:x1 = @{ lib = 'libxyz-' + (Get-Random -Minimum 1000 -Maximum 9999); code = 42 }
    $body = '1..20000 | ForEach-Object { "compilando modulo $_ ... ok (0 avisos)" }' + "`n" +
      "[Console]::Error.WriteLine(""ERRO FATAL: dependencia '$($script:x1.lib)' ausente (codigo 42)"")`nexit 42"
    Set-Content -Encoding utf8 "$dir\build.ps1" -Value $body
    "Execute o script $dir\build.ps1 com PowerShell (pwsh -NoProfile -File). Ele falha. Responda em uma linha: o nome da dependência que está ausente; o código de saída." }
  Gold = { "$($script:x1.lib); $($script:x1.code)" }
  Score = { param($a) Score-Bool (($a -like "*$($script:x1.lib)*") -and ($a -match "\b$($script:x1.code)\b")) } }

$TaskTable['X2'] = @{ name = 'Falha transitória: funciona na 3ª tentativa'; stratum = 'saida'
  Prepare = { param($dir)
    $script:x2 = 'OK-' + (Get-Random -Minimum 100000 -Maximum 999999)
    $body = '$f = Join-Path $PSScriptRoot ''tentativas.txt''; $n = 1 + [int](Get-Content $f -ErrorAction SilentlyContinue); Set-Content $f $n' + "`n" +
      'if ($n -lt 3) { [Console]::Error.WriteLine("falha transitoria: servico indisponivel (tentativa $n)"); exit 1 }' + "`n" +
      "Write-Output 'sucesso: $($script:x2)'"
    Set-Content -Encoding utf8 "$dir\flaky.ps1" -Value $body
    "Execute o script $dir\flaky.ps1 com PowerShell (pwsh -NoProfile -File). Ele pode falhar por instabilidade; se falhar, tente de novo até funcionar. Responda apenas com a mensagem de sucesso que ele imprime." }
  Gold = { "sucesso: $($script:x2)" }
  Score = { param($a) Score-Bool ($a -like "*$($script:x2)*") } }
