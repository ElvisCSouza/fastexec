# Relatorio: economia de tokens (foco) e, como extras, latencia, CPU e RAM. Compara os bracos por PARES (mesma tarefa e mesma
# repeticao) e so conta execucoes validas. Mostra n e taxa de acerto: media geral esconde regressao.
# Uso: pwsh -NoProfile -File .\Report.ps1 -Tag full1   (ou -File caminho.jsonl)
param([string]$Tag = '', [string]$File = '', [int]$Boot = 2000)
$ErrorActionPreference = 'Stop'
if (-not $File) { $File = Join-Path $PSScriptRoot "results\$Tag.jsonl" }
$rows = @(Get-Content $File | Where-Object { $_.Trim() } | ForEach-Object { $_ | ConvertFrom-Json })
if (-not $rows.Count) { throw "sem resultados em $File" }
$ArmN = 'native'; $ArmF = 'fastexec'

function Med([double[]]$x) { if (-not $x.Count) { return [double]::NaN }; $s = $x | Sort-Object; $n = $s.Count; if ($n % 2) { $s[[int](($n - 1) / 2)] } else { ($s[$n / 2 - 1] + $s[$n / 2]) / 2 } }
function Pct([double]$ratio) { if ([double]::IsNaN($ratio)) { 'n/d' } else { '{0:+0.0;-0.0;0.0}%' -f (($ratio - 1) * 100) } }
function Gmean([double[]]$r) { if (-not $r.Count) { return [double]::NaN }; [math]::Exp((($r | ForEach-Object { [math]::Log([math]::Max($_, 1e-9)) }) | Measure-Object -Average).Average) }
function BootCI([double[]]$r) {          # IC 95% da media geometrica das razoes (reamostragem dos pares, semente fixa)
  if ($r.Count -lt 3) { return @([double]::NaN, [double]::NaN) }
  $rnd = New-Object System.Random 1; $g = New-Object 'System.Collections.Generic.List[double]'
  for ($i = 0; $i -lt $Boot; $i++) { $s = for ($k = 0; $k -lt $r.Count; $k++) { $r[$rnd.Next(0, $r.Count)] }; $g.Add((Gmean $s)) }
  $g = $g | Sort-Object; @($g[[int]($Boot * 0.025)], $g[[int]($Boot * 0.975) - 1])
}

# pares validos: mesma tarefa+rep, os dois bracos validos
$valid = @($rows | Where-Object { $_.valid })
$pairs = New-Object Collections.Generic.List[object]
foreach ($g in ($valid | Group-Object { "$($_.task)|$($_.rep)" })) {
  $a = $g.Group | Where-Object { $_.arm -eq $ArmN } | Select-Object -First 1; $b = $g.Group | Where-Object { $_.arm -eq $ArmF } | Select-Object -First 1
  if ($a -and $b) { $pairs.Add([pscustomobject]@{ task = $a.task; stratum = $a.stratum; rep = $a.rep; a = $a; b = $b; both_ok = ($a.correct -and $b.correct) }) }
}

$sb = New-Object Text.StringBuilder
function Out([string]$s = '') { [void]$sb.AppendLine($s) }
$first = $rows[0]
Out "# Benchmark: Claude nativo vs Claude com FastExec"
Out ""
Out "- Modelo: ``$($first.model)`` ($(if ($first.provider) { $first.provider } else { 'Anthropic' })) | cache de prompt: $(if ($first.provider -and $first.provider -ne 'Anthropic') { 'automático do provedor' } elseif ($first.cache) { 'ligado' } else { 'desligado (tokens comparáveis)' }) | FastExec ``$($first.fx_sha)`` | $($first.claude)"
Out "- Execuções: $($rows.Count) ($($valid.Count) válidas), pares válidos: $($pairs.Count). Razão < 1,0 significa que o FastExec gastou menos. A média de economia exclui a linha de base T0."
Out ""

# ---- 1) headline: tokens ----
$real = @($pairs | Where-Object { $_.stratum -ne 'baseline' })   # a linha de base (T0) mede só o imposto fixo: fica fora da média de economia
$rt = @($real | ForEach-Object { [double]$_.b.tokens_total / [double][math]::Max(1, $_.a.tokens_total) })
$ci = BootCI $rt
Out "## 1. Economia de tokens (foco)"
Out ""
if ($real.Count) {
  Out ("**Média geométrica da razão FastExec/nativo: {0:N3} ({1})**, IC95% [{2:N3}; {3:N3}], n = {4} pares." -f (Gmean $rt), (Pct (Gmean $rt)), $ci[0], $ci[1], $rt.Count)
  $ro = @($real | Where-Object { $_.both_ok } | ForEach-Object { [double]$_.b.tokens_total / [double][math]::Max(1, $_.a.tokens_total) })
  Out ("Só pares em que **os dois acertaram**: {0:N3} ({1}), n = {2}. (Economia que vem de errar mais cedo não conta.)" -f (Gmean $ro), (Pct (Gmean $ro)), $ro.Count)
}
Out ""
Out "| Tarefa | Estrato | n | tokens nativo | tokens FastExec | Δ tokens | turnos N→F | KB devolvidos N→F | acerto N vs F | erros ferr. N→F |"
Out "|---|---|---|---|---|---|---|---|---|---|"
foreach ($t in ($pairs | Group-Object task | Sort-Object Name)) {
  $p = $t.Group; $allT = @($rows | Where-Object { $_.task -eq $t.Name })
  $acc = { param($arm) $x = @($allT | Where-Object { $_.arm -eq $arm }); if ($x.Count) { '{0}/{1}' -f @($x | Where-Object { $_.correct }).Count, $x.Count } else { '-' } }
  $ma = Med @($p | ForEach-Object { [double]$_.a.tokens_total }); $mb = Med @($p | ForEach-Object { [double]$_.b.tokens_total })
  Out ("| {0} | {1} | {2} | {3:N0} | {4:N0} | {5} | {6:N1}→{7:N1} | {8:N1}→{9:N1} | {10} vs {11} | {12:N1}→{13:N1} |" -f $t.Name, $p[0].stratum, $p.Count, $ma, $mb, (Pct ($mb / $ma)),
    (Med @($p | ForEach-Object { [double]$_.a.turns })), (Med @($p | ForEach-Object { [double]$_.b.turns })),
    (Med @($p | ForEach-Object { [double]$_.a.result_kb })), (Med @($p | ForEach-Object { [double]$_.b.result_kb })),
    (& $acc $ArmN), (& $acc $ArmF), (Med @($p | ForEach-Object { [double]$_.a.tool_errors })), (Med @($p | ForEach-Object { [double]$_.b.tool_errors })))
}
Out ""
Out "### Por estrato"
Out ""
Out "| Estrato | n | razão tokens (média geom.) | Δ |"
Out "|---|---|---|---|"
foreach ($s in ($real | Group-Object stratum | Sort-Object Name)) {
  $r = @($s.Group | ForEach-Object { [double]$_.b.tokens_total / [double][math]::Max(1, $_.a.tokens_total) })
  Out ("| {0} | {1} | {2:N3} | {3} |" -f $s.Name, $r.Count, (Gmean $r), (Pct (Gmean $r)))
}

# ---- 2) decomposicao: imposto fixo x trabalho ----
Out ""
Out "## 2. De onde vem a diferença"
Out ""
$fixo = { param($arm) $x = @($valid | Where-Object { $_.arm -eq $arm -and $_.first_turn_in -gt 0 }); if ($x.Count) { Med @($x | ForEach-Object { [double]$_.first_turn_in }) } else { Med @($valid | Where-Object { $_.arm -eq $arm -and $_.task -eq 'T0' } | ForEach-Object { [double]$_.in_tokens }) } }
Out ("- **Imposto fixo por turno** (system prompt + schemas das ferramentas; 1º turno, ou a tarefa T0 quando o provedor não reporta uso por turno): nativo {0:N0} tokens, FastExec {1:N0} tokens." -f (& $fixo $ArmN), (& $fixo $ArmF))
Out "- Cada turno extra repete esse imposto (cache desligado). Por isso **número de turnos** e **bytes devolvidos ao modelo** explicam quase toda a diferença; veja as colunas da tabela acima."
Out ("- Chamadas de ferramenta por execução (mediana): nativo {0:N1}, FastExec {1:N1}." -f (Med @($valid | Where-Object { $_.arm -eq $ArmN } | ForEach-Object { [double]$_.tool_calls })), (Med @($valid | Where-Object { $_.arm -eq $ArmF } | ForEach-Object { [double]$_.tool_calls })))

# ---- 3) extras: latencia, CPU, RAM, custo ----
Out ""
Out "## 3. Extras: latência, CPU, RAM e custo"
Out ""
Out "Medianas dos pares; CPU e RAM são da árvore inteira de processos (claude + proxy MCP + daemon do FastExec). Razão < 1,0 = FastExec melhor."
Out ""
Out "| Métrica | nativo | FastExec | razão | Δ |"
Out "|---|---|---|---|---|"
foreach ($m in @(@('wall_s', 'Tempo total (s)'), @('api_s', 'Tempo no modelo (s)'), @('tool_s', 'Tempo fora do modelo: ferramentas (s)'), @('cpu_s', 'CPU (s)'), @('ram_mb', 'RAM de pico (MB)'), @('cost', 'Custo (US$, sem cache)'))) {
  $x = @($pairs | ForEach-Object { [double]$_.a.($m[0]) }); $y = @($pairs | ForEach-Object { [double]$_.b.($m[0]) })
  $ma = Med $x; $mb = Med $y; $r = if ($ma -gt 0) { $mb / $ma } else { [double]::NaN }
  Out ("| {0} | {1:N2} | {2:N2} | {3:N2} | {4} |" -f $m[1], $ma, $mb, $r, (Pct $r))
}

# ---- 4) qualidade ----
Out ""
Out "## 4. Qualidade (não vale economizar errando)"
Out ""
Out "| Braço | execuções | válidas | acertos | erros de ferramenta (média) |"
Out "|---|---|---|---|---|"
foreach ($arm in @($ArmN, $ArmF)) {
  $x = @($rows | Where-Object { $_.arm -eq $arm }); if (-not $x.Count) { continue }
  Out ("| {0} | {1} | {2} | {3} ({4:P0}) | {5:N2} |" -f $arm, $x.Count, @($x | Where-Object { $_.valid }).Count, @($x | Where-Object { $_.correct }).Count, (@($x | Where-Object { $_.correct }).Count / $x.Count), (($x | Measure-Object tool_errors -Average).Average))
}
# ---- 5) hibrido: nativas + FastExec juntas (o uso real) ----
$hy = @($valid | Where-Object { $_.arm -eq 'hybrid' })
if ($hy.Count) {
  Out ""
  Out "## 5. Híbrido: ferramentas nativas + FastExec juntas"
  Out ""
  Out "O uso real: o modelo tem as duas famílias de ferramentas e escolhe. Compara com o braço nativo (mesma tarefa e repetição). `% FastExec` = parte das chamadas de ferramenta que foi para o FastExec."
  Out ""
  Out "| Tarefa | n | tokens nativo | tokens híbrido | Δ | % chamadas no FastExec | acerto híbrido |"
  Out "|---|---|---|---|---|---|---|"
  $hp = New-Object Collections.Generic.List[object]
  foreach ($t in ($hy | Group-Object task | Sort-Object Name)) {
    $nat = @($valid | Where-Object { $_.arm -eq $ArmN -and $_.task -eq $t.Name }); $prs = @()
    foreach ($h in $t.Group) { $n = $nat | Where-Object { $_.rep -eq $h.rep } | Select-Object -First 1; if ($n) { $prs += [pscustomobject]@{ n = $n; h = $h } } }
    if (-not $prs.Count) { continue }
    $mn = Med @($prs | ForEach-Object { [double]$_.n.tokens_total }); $mh = Med @($prs | ForEach-Object { [double]$_.h.tokens_total })
    $calls = ($t.Group | Measure-Object tool_calls -Sum).Sum; $fxc = ($t.Group | Measure-Object fx_calls -Sum).Sum
    $share = if ($calls) { $fxc / $calls } else { 0 }
    Out ("| {0} | {1} | {2:N0} | {3:N0} | {4} | {5:P0} | {6}/{7} |" -f $t.Name, $prs.Count, $mn, $mh, (Pct ($mh / $mn)), $share, @($t.Group | Where-Object { $_.correct }).Count, $t.Count)
    foreach ($p in $prs) { if ($t.Name -ne 'T0') { $hp.Add([double]$p.h.tokens_total / [math]::Max(1.0, [double]$p.n.tokens_total)) } }
  }
  if ($hp.Count) { $gm = [double](Gmean ([double[]]$hp.ToArray())); Out ""; Out ('**Média geométrica híbrido/nativo: ' + $gm.ToString('N3') + ' (' + (Pct $gm) + '), n = ' + $hp.Count + ' pares.**') }
}

$gr = @($valid | Where-Object { $_.stratum -eq 'grafo' -or [int]$_.graph_calls -gt 0 -or [int]$_.outline_calls -gt 0 })
if ($gr.Count) {
  Out ""
  Out "## 6. Uso do grafo (code_graph / code_outline)"
  Out ""
  Out "Por tarefa e braço: média de chamadas ao grafo e ao outline, chamadas a Grep/Glob nativos, turnos, tokens medianos e acerto. Mostra se o modelo usa o grafo quando ele existe e se isso poupa token."
  Out ""
  Out "| Tarefa | Braço | n | graph | outline | grep/glob | turnos | tokens (mediana) | acerto |"
  Out "|---|---|---|---|---|---|---|---|---|"
  foreach ($t in ($gr | Group-Object task | Sort-Object Name)) {
    foreach ($ar in ($valid | Where-Object { $_.task -eq $t.Name } | Group-Object arm | Sort-Object Name)) {
      $g = $ar.Group
      Out ("| {0} | {1} | {2} | {3:N1} | {4:N1} | {5:N1} | {6:N1} | {7:N0} | {8}/{2} |" -f $t.Name, $ar.Name, $g.Count, (($g | Measure-Object graph_calls -Average).Average), (($g | Measure-Object outline_calls -Average).Average), (($g | Measure-Object grep_calls -Average).Average), (($g | Measure-Object turns -Average).Average), (Med @($g | ForEach-Object { [double]$_.tokens_total })), @($g | Where-Object { $_.correct }).Count)
    }
  }
}

$lspTag = if ($Tag) { $Tag } else { [IO.Path]::GetFileNameWithoutExtension($File) }
$lspLines = @(& (Join-Path $PSScriptRoot 'Lsp-Usage.ps1') -Tag $lspTag 2>$null)
if ($lspLines.Count) {
  Out ""
  Out "## 7. Roslyn e servidores de linguagem: usado e exato?"
  Out ""
  Out "Conta as chamadas reais a ``code_graph`` (action callers) do braço FastExec e de onde veio cada resposta: **exata** (servidor de linguagem), **por nome** (índice) ou **LSP indisponível** (o servidor foi pedido e não respondeu; o motivo aparece abaixo). Sem isto, uma resposta por nome parece exata."
  Out ""
  Out '```'
  foreach ($l in $lspLines) { Out ([string]$l) }
  Out '```'
}

Out ""
Out "## Como ler"
Out ""
Out "- Pares: mesma tarefa e repetição nos dois braços, ordem aleatória, modelo e prompt idênticos. Só execuções válidas (o braço FastExec precisa ter chamado o FastExec)."
Out "- IC95% por reamostragem dos pares (semente fixa). Com poucos pares o intervalo é largo: é para isso que ele existe."
Out "- Tokens = entrada + saída da conta (cache desligado), a unidade em que o custo escala. Com cache ligado os números mudam; rode com ``-Cache`` para ver."

$out = Join-Path $PSScriptRoot ("results\report-" + [IO.Path]::GetFileNameWithoutExtension($File) + '.md')
Set-Content $out $sb.ToString() -Encoding UTF8
$sb.ToString(); "`n(salvo em $out)"
