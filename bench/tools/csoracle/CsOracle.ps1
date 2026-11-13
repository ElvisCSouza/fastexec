# tools/csoracle/CsOracle.ps1 — oraculo do estrato 'csharp'.
#
# Tudo aqui e' deterministico e offline. Decisoes (medidas na mao antes de codar):
#
#  * O SDK local (W:\Data\dotnet-sdk\sdk, 8.0.428) e' o unico instalado. Os repos
#    trazem global.json com pins antigos (serilog: 7.0.102), entao TODA copia de
#    trabalho tem o global.json reescrito para o SDK local. FV ja' e' 8.0.100.
#  * Copia de trabalho = arvore INTEIRA do repo (menos .git/obj/bin). Copiar so'
#    'src/' quebra o build: o LangVersion mora no Directory.Build.props da raiz
#    (serilog: 1092 erros CS8370 se faltar).
#  * Nunca se builda o fixture compartilhado: o oraculo copia para o diretorio da
#    tarefa e builda la'. O clone compartilhado fica intacto.
#  * Probe de comportamento (CS7): projeto net8.0 Exe com <Reference><HintPath>
#    para a DLL netstandard2.0 recem-buildada. <ProjectReference> para o csproj do
#    fixture NAO serve (MSBuild escolhe net7.0 e da' CS0246).
#  * Config vem de bench.config.json (bloco "dotnet"); BENCH_DOTNET_EXE/ROOT e
#    BENCH_NUGET_PACKAGES sobrepoem. Estado em $global: de proposito: as closures
#    GetNewClosure() das tarefas nao enxergam escopo de script (ver swelite.ps1).
#
# Superficie publica (usada por tasks/csharp.ps1):
#   Get-CsDotnetCfg                     -> @{ exe; root; sdk_version; nuget_packages; projects }
#   Copy-CsRepo  -Src -Dst              -> copia arvore + reescreve global.json; devolve $Dst
#   Invoke-CsDotnet -CsArgs -WorkDir [-TimeoutSec] -> @{ exit; out; timeout }
#   Invoke-CsBuild -Dir -Repo [-TimeoutSec]        -> @{ exit; out; dll; timeout }
#   Invoke-CsProbe -Dir -Name -LibDll -Program [-TimeoutSec] -> @{ exit; out; lines; timeout }
#   Get-CsRepoFiles  -Root [-Under] [-NoTests]     -> relpaths .cs (ordenados, '/')
#   Get-CsMentionFiles -Root -Regex [-Under] [-NoTests] -> relpaths que dao match
#   Get-CsMatchLines -Root -Regex [-Under]         -> "rel:linha"
#   Get-CsImplFiles  -Root -Interface [-Under] [-NoTests] -> relpaths com declaracao de tipo
#   Find-CsLocation  -Root -Regex [-Under]         -> "rel:linha" (1o em ordem)
#   Read-CsText      -Path                         -> texto normalizado (BOM fora)
#   Set-CsText       -Path -Text                   -> grava UTF8 sem BOM
#   Edit-CsText      -Path -From -To [-Once]       -> troca textual; lanca se a ancora faltar
#   Score-CsFiles    -Answer -Gold                 -> @{ correct; precision; recall }
#   Score-CsRegex    -Answer -Patterns             -> @{ correct; precision; recall }
#   Score-CsLocation -Answer -Gold [-Slack]        -> @{ correct; precision; recall }
#   Score-CsAll      -Results @(...)               -> @{ correct; precision; recall }
#   Score-CsBuildProbe -Dir -Repo -Name -Program -Expect -> @{ correct; precision; recall }

$global:CsOracleBench = if ($script:BenchRoot) { $script:BenchRoot } else { Split-Path (Split-Path $PSScriptRoot -Parent) -Parent }
$global:CsCfg = $null

function global:Get-CsDotnetCfg {
    if ($global:CsCfg) { return $global:CsCfg }
    $j = $null
    $cfgPath = Join-Path $global:CsOracleBench 'bench.config.json'
    if (Test-Path $cfgPath) {
        try { $j = (Get-Content -LiteralPath $cfgPath -Raw | ConvertFrom-Json).dotnet } catch { $j = $null }
    }
    $exe = if ($env:BENCH_DOTNET_EXE) { $env:BENCH_DOTNET_EXE } elseif ($j) { $j.exe } else { $null }
    $root = if ($env:BENCH_DOTNET_ROOT) { $env:BENCH_DOTNET_ROOT } elseif ($j) { $j.root } else { $null }
    $pkgs = if ($env:BENCH_NUGET_PACKAGES) { $env:BENCH_NUGET_PACKAGES } elseif ($j) { $j.nuget_packages } else { $null }
    if (-not $exe) { throw "CsOracle: defina o bloco 'dotnet' em bench.config.json ou BENCH_DOTNET_EXE" }
    if (-not (Test-Path -LiteralPath $exe)) { throw "CsOracle: dotnet.exe nao encontrado: $exe" }
    if (-not $root) { $root = Split-Path -Parent (Split-Path -Parent $exe) }
    $global:CsCfg = @{
        exe            = $exe
        root           = $root
        sdk_version    = if ($j -and $j.sdk_version) { $j.sdk_version } else { '8.0.428' }
        nuget_packages = $pkgs
        projects       = if ($j) { $j.projects } else { $null }
    }
    return $global:CsCfg
}

function global:Invoke-CsDotnet {
    param(
        [Parameter(Mandatory)][string[]]$CsArgs,
        [Parameter(Mandatory)][string]$WorkDir,
        [int]$TimeoutSec = 300
    )
    $d = Get-CsDotnetCfg
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $d.exe
    $psi.WorkingDirectory = $WorkDir
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.StandardOutputEncoding = [Text.Encoding]::UTF8
    $psi.StandardErrorEncoding = [Text.Encoding]::UTF8
    $psi.CreateNoWindow = $true
    foreach ($a in $CsArgs) { [void]$psi.ArgumentList.Add($a) }
    $psi.EnvironmentVariables['DOTNET_ROOT'] = $d.root
    $psi.EnvironmentVariables['DOTNET_CLI_TELEMETRY_OPTOUT'] = '1'
    $psi.EnvironmentVariables['DOTNET_NOLOGO'] = '1'
    $psi.EnvironmentVariables['DOTNET_SKIP_FIRST_TIME_EXPERIENCE'] = '1'
    $psi.EnvironmentVariables['DOTNET_CLI_UI_LANGUAGE'] = 'en'
    if ($d.nuget_packages -and (Test-Path -LiteralPath $d.nuget_packages)) {
        $psi.EnvironmentVariables['NUGET_PACKAGES'] = $d.nuget_packages
    }
    $psi.EnvironmentVariables['PATH'] = "$($d.root);" + $psi.EnvironmentVariables['PATH']

    $p = [System.Diagnostics.Process]::Start($psi)
    $soTask = $p.StandardOutput.ReadToEndAsync()
    $seTask = $p.StandardError.ReadToEndAsync()
    $timeout = $false
    if (-not $p.WaitForExit($TimeoutSec * 1000)) {
        $timeout = $true
        try { $p.Kill($true) } catch { }
    }
    $out = "$($soTask.Result)`n$($seTask.Result)"
    return @{ exit = $p.ExitCode; out = $out; timeout = $timeout }
}

function global:Read-CsText {
    param([Parameter(Mandatory)][string]$Path)
    return [string](Get-Content -LiteralPath $Path -Raw -Encoding UTF8)
}

function global:Set-CsText {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][AllowEmptyString()][string]$Text
    )
    [IO.File]::WriteAllText($Path, $Text, (New-Object Text.UTF8Encoding $false))
}

# Troca textual com ancora obrigatoria: se $From nao existir, LANCA (falha rapida
# do gabarito em vez de gabarito silenciosamente errado). -Once troca so' a 1a.
function global:Edit-CsText {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$From,
        [Parameter(Mandatory)][AllowEmptyString()][string]$To,
        [switch]$Once
    )
    $t = Read-CsText -Path $Path
    # Os repositorios usam CRLF: aceita a ancora escrita com "\n" e converte para o
    # estilo do arquivo, senao toda ancora multilinha falharia por causa do \r.
    if ($t.Contains("`r`n")) {
        $From = $From -replace "`r?`n", "`r`n"
        $To = $To -replace "`r?`n", "`r`n"
    }
    $i = $t.IndexOf($From)
    if ($i -lt 0) { throw "Edit-CsText: ancora ausente em $Path :: '$From'" }
    if ($Once) { $t = $t.Substring(0, $i) + $To + $t.Substring($i + $From.Length) }
    else { $t = $t.Replace($From, $To) }
    Set-CsText -Path $Path -Text $t
}

function global:Copy-CsRepo {
    param(
        [Parameter(Mandatory)][string]$Src,
        [Parameter(Mandatory)][string]$Dst
    )
    if (-not (Test-Path -LiteralPath $Src)) { throw "CsOracle: fixture ausente: $Src" }
    $rc = (robocopy $Src $Dst /E /XD .git obj bin /NFL /NDL /NJH /NJS /NP /R:1 /W:1 2>&1 | Out-String)
    $code = $LASTEXITCODE
    if ($code -ge 8) { throw "CsOracle: robocopy falhou (rc=$code) $Src -> $Dst :: $rc" }
    # SDK local: os pins antigos dos repos nao existem nesta maquina.
    $d = Get-CsDotnetCfg
    $gj = Join-Path $Dst 'global.json'
    if (Test-Path -LiteralPath $gj) {
        $json = "{`n  `"sdk`": {`n    `"version`": `"$($d.sdk_version)`",`n    `"rollForward`": `"latestFeature`"`n  }`n}`n"
        Set-Content -LiteralPath $gj -Value $json -Encoding UTF8
    }
    # Copia restaurada, como a de quem trabalha no repo: sem obj/project.assets.json o csharp-ls nao carrega
    # ("restore needed") e o modo exato nunca e' exercitado. Restore offline (cache NuGet) e so' escreve obj/.
    $sln = Get-ChildItem -LiteralPath $Dst -Filter *.sln -File -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($sln) { $null = Invoke-CsDotnet -CsArgs @('restore', $sln.FullName) -WorkDir $Dst -TimeoutSec 300 }
    return $Dst
}

function global:Get-CsProjectCfg {
    param([Parameter(Mandatory)][string]$Repo)
    $d = Get-CsDotnetCfg
    if (-not $d.projects -or -not $d.projects.$Repo) { throw "CsOracle: bench.config.json sem dotnet.projects.$Repo" }
    return $d.projects.$Repo
}

function global:Get-CsLibDll {
    param(
        [Parameter(Mandatory)][string]$Dir,
        [Parameter(Mandatory)][string]$Repo
    )
    $p = Get-CsProjectCfg $Repo
    $projDir = Join-Path $Dir (Split-Path $p.project -Parent)
    $dll = Join-Path $projDir "bin\Debug\$($p.tfm)\$($p.assembly).dll"
    return $dll
}

# Build do projeto alvo dentro de $Dir (copia de trabalho). Devolve rc + dll.
function global:Invoke-CsBuild {
    param(
        [Parameter(Mandatory)][string]$Dir,
        [Parameter(Mandatory)][string]$Repo,
        [int]$TimeoutSec = 420
    )
    $p = Get-CsProjectCfg $Repo
    $r = Invoke-CsDotnet -CsArgs @('build', $p.project, '-f', $p.tfm, '-v:q', '--nologo', '-nodereuse:false') -WorkDir $Dir -TimeoutSec $TimeoutSec
    $dll = Get-CsLibDll -Dir $Dir -Repo $Repo
    $ok = ($r.exit -eq 0 -and -not $r.timeout -and (Test-Path -LiteralPath $dll))
    return @{ exit = $r.exit; out = $r.out; dll = $(if ($ok) { $dll } else { $null }); timeout = $r.timeout }
}

# Probe de comportamento: projeto net8.0 Exe que referencia a DLL buildada e
# imprime linhas 'CSOUT:...'. $Program vai literal em Program.cs.
function global:Invoke-CsProbe {
    param(
        [Parameter(Mandatory)][string]$Dir,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$LibDll,
        [Parameter(Mandatory)][string]$Program,
        [int]$TimeoutSec = 420
    )
    $pdir = Join-Path $Dir "oracle-probe-$Name"
    New-Item -ItemType Directory -Force -Path $pdir | Out-Null
    # HintPath absoluto: o probe pode ficar em qualquer lugar da arvore da tarefa.
    $csproj = @"
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>disable</Nullable>
    <ImplicitUsings>disable</ImplicitUsings>
    <AssemblyName>csprobe</AssemblyName>
    <RootNamespace>csprobe</RootNamespace>
    <GenerateDocumentationFile>false</GenerateDocumentationFile>
  </PropertyGroup>
  <ItemGroup>
    <Reference Include="Lib">
      <HintPath>$LibDll</HintPath>
    </Reference>
  </ItemGroup>
</Project>
"@
    Set-Content -LiteralPath (Join-Path $pdir 'probe.csproj') -Value $csproj -Encoding UTF8
    Set-Content -LiteralPath (Join-Path $pdir 'Program.cs') -Value $Program -Encoding UTF8
    $r = Invoke-CsDotnet -CsArgs @('run', '--project', (Join-Path $pdir 'probe.csproj'), '-v:q', '--nologo', '-nodereuse:false') -WorkDir $pdir -TimeoutSec $TimeoutSec
    $lines = @($r.out -split "`r?`n" | Where-Object { $_ -like 'CSOUT:*' } | ForEach-Object { $_.Substring(6).Trim() })
    return @{ exit = $r.exit; out = $r.out; lines = $lines; timeout = $r.timeout }
}

# ---- helpers textuais (leitura pura; nada e' escrito) -------------------------

function global:Get-CsRepoFiles {
    param(
        [Parameter(Mandatory)][string]$Root,
        [string]$Under = '',
        [switch]$NoTests
    )
    $base = if ($Under) { Join-Path $Root $Under } else { $Root }
    $files = Get-ChildItem -LiteralPath $base -Recurse -File -Filter *.cs -ErrorAction SilentlyContinue
    $out = New-Object System.Collections.Generic.List[string]
    foreach ($f in $files) {
        $rel = $f.FullName.Substring($Root.Length).TrimStart('\', '/') -replace '\\', '/'
        if ($rel -match '(^|/)(obj|bin|\.git)/') { continue }
        # oracle-probe-<id>/ e' artefato do harness (Invoke-CsProbe), nao conteudo do repo:
        # nunca pode contar como arquivo do repositorio em tarefas com verificacao por grep.
        if ($rel -match '^oracle-probe') { continue }
        if ($NoTests -and $rel -match '(^|/)(test|tests|Test|Tests)/') { continue }
        $out.Add($rel)
    }
    return @($out | Sort-Object)
}

function global:Get-CsMentionFiles {
    param(
        [Parameter(Mandatory)][string]$Root,
        [Parameter(Mandatory)][string]$Regex,
        [string]$Under = '',
        [switch]$NoTests
    )
    $res = New-Object System.Collections.Generic.List[string]
    foreach ($rel in (Get-CsRepoFiles -Root $Root -Under $Under -NoTests:$NoTests)) {
        $t = Read-CsText -Path (Join-Path $Root ($rel -replace '/', '\'))
        if ($t -match $Regex) { $res.Add($rel) }
    }
    return @($res | Sort-Object)
}

function global:Get-CsMatchLines {
    param(
        [Parameter(Mandatory)][string]$Root,
        [Parameter(Mandatory)][string]$Regex,
        [string]$Under = ''
    )
    $res = New-Object System.Collections.Generic.List[string]
    foreach ($rel in (Get-CsRepoFiles -Root $Root -Under $Under)) {
        $t = Read-CsText -Path (Join-Path $Root ($rel -replace '/', '\'))
        $n = 0
        foreach ($ln in ($t -split "`r?`n")) {
            $n++
            if ($ln -match $Regex) { $res.Add("${rel}:$n") }
        }
    }
    return @($res)
}

# Arquivos com uma DECLARACAO de tipo que lista a interface como base (nao pega
# mencoes em comentario/uso: exige 'class|struct|record <Nome> ... : ... <Interface>').
function global:Get-CsImplFiles {
    param(
        [Parameter(Mandatory)][string]$Root,
        [Parameter(Mandatory)][string]$Interface,
        [string]$Under = '',
        [switch]$NoTests
    )
    $re = '(?s)\b(?:class|struct|record)\s+\w+\s*(?::[^;{]*?)?\b' + [regex]::Escape($Interface) + '\b'
    $res = New-Object System.Collections.Generic.List[string]
    foreach ($rel in (Get-CsRepoFiles -Root $Root -Under $Under -NoTests:$NoTests)) {
        if ([regex]::IsMatch((Read-CsText -Path (Join-Path $Root ($rel -replace '/', '\'))), $re)) { $res.Add($rel) }
    }
    return @($res | Sort-Object)
}

# 1a ocorrencia em ordem de caminho+linha (endereco estavel para o gabarito).
function global:Find-CsLocation {
    param(
        [Parameter(Mandatory)][string]$Root,
        [Parameter(Mandatory)][string]$Regex,
        [string]$Under = ''
    )
    # @(...) e' obrigatorio: com UMA ocorrencia o PowerShell desembrulha o array e
    # $l[0] passaria a indexar os CARACTERES da string (devolvia 's' de 'src/...').
    $l = @(Get-CsMatchLines -Root $Root -Regex $Regex -Under $Under)
    if ($l.Count -eq 0) { return $null }
    return $l[0]
}

# ---- scorers -----------------------------------------------------------------

# Compara conjuntos de arquivos .cs. Um item da resposta casa com um arquivo do
# gabarito se for o relpath (ou o caminho terminando nele) ou o basename unico.
function global:Score-CsFiles {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Answer,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Gold
    )
    $gold = @($Gold | ForEach-Object { $_.TrimStart('./') } | Sort-Object -Unique)
    $items = New-Object System.Collections.Generic.List[string]
    foreach ($m in [regex]::Matches($Answer, '[A-Za-z0-9_\.\-/\\]+\.cs')) {
        $tok = $m.Value -replace '\\', '/'
        $tok = $tok.TrimStart('./')
        if ($tok) { $items.Add($tok) }
    }
    $seen = @{}
    $hit = 0
    foreach ($it in $items) {
        if ($seen.ContainsKey($it)) { continue }
        $seen[$it] = $true
        $match = $null
        if ($gold -contains $it) { $match = $it }
        else {
            # tolera caminho absoluto na resposta (gold e' sufixo do item) e o
            # inverso (item e' sufixo unico do gold), desde que sem ambiguidade.
            $cands = @($gold | Where-Object { $_ -like "*/$it" -or $it -like "*/$_" })
            if ($cands.Count -eq 1) { $match = $cands[0] }
        }
        if ($match) { $hit++ }
    }
    $total = [Math]::Max($gold.Count, 1)
    $got = [Math]::Max($items.Count, 1)
    $recall = [Math]::Round($hit / $total, 4)
    $precision = [Math]::Round($hit / $got, 4)
    $correct = ($recall -ge 1.0 -and $precision -ge 1.0)
    return @{ correct = $correct; precision = $precision; recall = $recall }
}

# Endereco "arquivo.cs:linha". Casa pelo BASENAME + numero da linha (tolerancia
# -Slack), entao resposta com caminho absoluto, com prefixo ou so' o nome do
# arquivo valem igual. Citacoes extras na resposta nao penalizam: a tarefa pede
# UM endereco, nao uma lista.
function global:Score-CsLocation {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Answer,
        [Parameter(Mandatory)][string]$Gold,
        [int]$Slack = 0
    )
    if ($Gold -notmatch '^(?<f>.+):(?<l>\d+)$') { throw "Score-CsLocation: gabarito invalido '$Gold' (esperado 'arq.cs:linha')" }
    $gf = Split-Path $Matches['f'] -Leaf
    $gl = [int]$Matches['l']
    foreach ($m in [regex]::Matches($Answer, '(?<f>[A-Za-z0-9_\.\-/\\]+\.cs)\s*[:#]\s*(?<l>\d+)')) {
        $f = Split-Path ($m.Groups['f'].Value -replace '\\', '/') -Leaf
        if ($f -ieq $gf -and [Math]::Abs([int]$m.Groups['l'].Value - $gl) -le $Slack) {
            return @{ correct = $true; precision = 1.0; recall = 1.0 }
        }
    }
    return @{ correct = $false; precision = 0.0; recall = 0.0 }
}

# Combina varios resultados parciais (build, sonda, regex, arquivos): so' e'
# correto se TODOS forem; precision/recall = pior caso.
function global:Score-CsAll {
    param([Parameter(Mandatory)][AllowEmptyCollection()][object[]]$Results)
    $rs = @($Results | Where-Object { $_ })
    if ($rs.Count -eq 0) { return @{ correct = $false; precision = 0.0; recall = 0.0 } }
    $ok = -not (@($rs | Where-Object { -not $_.correct }).Count)
    $p = ($rs | Measure-Object -Property precision -Minimum).Minimum
    $r = ($rs | Measure-Object -Property recall -Minimum).Minimum
    return @{ correct = [bool]$ok; precision = [double]$p; recall = [double]$r }
}

# Resposta textual: correct so' se TODOS os regex casarem (multiline).
function global:Score-CsRegex {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Answer,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Patterns
    )
    if ($Patterns.Count -eq 0) { return @{ correct = $false; precision = 0; recall = 0 } }
    $ok = 0
    foreach ($p in $Patterns) { if ([regex]::IsMatch($Answer, $p, 'Multiline')) { $ok++ } }
    $recall = [Math]::Round($ok / $Patterns.Count, 4)
    $correct = ($ok -eq $Patterns.Count)
    return @{ correct = $correct; precision = $(if ($correct) { 1.0 } else { $recall }); recall = $recall }
}

# Score de tarefa de escrita por COMPORTAMENTO: builda o projeto do repo dentro de
# $Dir e roda a sonda; so' e' correto se TODAS as linhas CSOUT esperadas casarem.
# Build quebrado/probe que nao compila => lines vazio => recall 0.
function global:Score-CsBuildProbe {
    param(
        [Parameter(Mandatory)][string]$Dir,
        [Parameter(Mandatory)][string]$Repo,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Program,
        [Parameter(Mandatory)][AllowEmptyCollection()][string[]]$Expect,
        [int]$TimeoutSec = 420
    )
    $b = Invoke-CsBuild -Dir $Dir -Repo $Repo -TimeoutSec $TimeoutSec
    if (-not $b.dll) {
        return @{ correct = $false; precision = 0.0; recall = 0.0; why = "build falhou (rc=$($b.exit) timeout=$($b.timeout))" }
    }
    $p = Invoke-CsProbe -Dir $Dir -Name $Name -LibDll $b.dll -Program $Program -TimeoutSec $TimeoutSec
    $s = Score-CsRegex -Answer ($p.lines -join "`n") -Patterns $Expect
    $s.why = $(if ($s.correct) { 'ok' } else { "probe: $($p.lines -join ',')" })
    return $s
}
