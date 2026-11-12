# tools/docs/DocsOracle.ps1 — scorers do estrato 'docs'.
#
# O corpus e' sintetico e deterministico (tools/docs/New-DocsCorpus.ps1), entao o
# gabarito pode ser exato: regex de palavra inteira sobre a resposta. Nada aqui
# julga estilo, so' o FATO pedido.
#
# Contrato de retorno (igual ao resto do harness): @{ correct; precision; recall }.
#   correct  = todos os padroes de -Require casam E nenhum de -Forbid casa
#   precision/recall = 1.0 quando correct, 0.0 quando nao (o fato e' unico)
#
# -Forbid existe para punir a resposta que traz o valor DEFASADO junto do correto
# ("a doc diz 8312, mas a config diz 8512"): num gabarito por regex o numero errado
# na resposta e' erro, nao hesitacao.

function global:Score-DocsRegex {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Answer,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Require,
        [AllowEmptyCollection()][string[]]$Forbid = @()
    )
    $hitReq = 0
    foreach ($p in $Require) { if ([regex]::IsMatch($Answer, $p)) { $hitReq++ } }
    $hitFor = 0
    foreach ($p in $Forbid) { if ([regex]::IsMatch($Answer, $p)) { $hitFor++ } }
    $ok = ($hitReq -eq $Require.Count) -and ($hitFor -eq 0)
    if ($ok) { return @{ correct = $true; precision = 1.0; recall = 1.0 } }
    return @{ correct = $false; precision = 0.0; recall = 0.0 }
}

# Compara conjuntos de caminhos .md (tolera caminho absoluto e o inverso, como
# Score-CsFiles faz para .cs).
function global:Score-DocsFiles {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Answer,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Gold
    )
    $gold = @($Gold | ForEach-Object { ($_ -replace '\\', '/').TrimStart('./') } | Sort-Object -Unique)
    $items = New-Object System.Collections.Generic.List[string]
    foreach ($m in [regex]::Matches($Answer, '[A-Za-z0-9_\.\-/\\]+\.md')) {
        $tok = ($m.Value -replace '\\', '/').TrimStart('./')
        if ($tok) { $items.Add($tok) }
    }
    $seen = @{}; $tp = 0
    foreach ($it in $items) {
        if ($seen.ContainsKey($it)) { continue }
        $seen[$it] = $true
        if ($gold -contains $it) { $tp++; continue }
        $cands = @($gold | Where-Object { $_ -like "*/$it" -or $it -like "*/$_" })
        if ($cands.Count -eq 1) { $tp++ }
    }
    $n = $seen.Keys.Count
    $p = if ($n) { $tp / $n } else { 0.0 }
    $r = if ($gold.Count) { $tp / $gold.Count } else { 0.0 }
    return @{ correct = ($p -eq 1 -and $r -eq 1); precision = [math]::Round($p, 2); recall = [math]::Round($r, 2) }
}

# Combina resultados parciais: so' e' correto se TODOS forem.
function global:Score-DocsAll {
    param([Parameter(Mandatory)][AllowEmptyCollection()][object[]]$Results)
    $rs = @($Results | Where-Object { $_ })
    if ($rs.Count -eq 0) { return @{ correct = $false; precision = 0.0; recall = 0.0 } }
    $ok = $true; $p = 1.0; $r = 1.0
    foreach ($x in $rs) {
        if (-not $x.correct) { $ok = $false }
        $p = [math]::Min($p, [double]$x.precision); $r = [math]::Min($r, [double]$x.recall)
    }
    return @{ correct = $ok; precision = $p; recall = $r }
}
