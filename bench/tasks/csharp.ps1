# tasks/csharp.ps1 — estrato 'csharp' (CS1..CS8): leitura de codigo C# real.
#
# Contrato igual ao das outras suites: $TaskTable[Id] = @{ name; stratum; Prepare($dir)
# -> prompt; Gold() -> resposta correta; Score($answer) -> @{correct;precision;recall} }.
# Aqui os campos extra 'max_usd' e 'timeout_s' sao consumidos pelo Run-Bench.ps1
# (orcamento por tarefa) e pelo oraculo (teto de tempo dos subprocessos dotnet).
#
# Fixtures: clones pristine de serilog v3.1.1 e FluentValidation 11.9.2 em
# <work_dir>\fixtures, baixados pelo Initialize-Bench.ps1 (bench.config.json.repos).
# O oraculo (tools/csoracle/CsOracle.ps1) NUNCA builda o clone compartilhado:
# Copy-CsRepo leva a arvore inteira para o $dir da tarefa (global.json reescrito
# para o SDK local) e o build acontece la'.
#
# ARMADILHA conhecida: scriptblock com .GetNewClosure() so' enxerga o escopo LOCAL
# de quem o criou — nao ve o escopo de script do Run-Bench.ps1 ($Fixtures, $All) nem
# o escopo das funcoes de lib/Bench.ps1. Por isso todas as factories daqui recebem
# caminhos/prompt como PARAMETRO (viram locais) e so' chamam funcoes globais
# (Get-Cs*/Score-Cs* de CsOracle.ps1, que sao 'function global:').
#
# Tarefas de escrita: o Prepare copia o fixture e (CS6/CS8) planta o defeito de
# forma deterministica; o Gold aplica a correcao de referencia e o Score verifica
# COMPORTAMENTO (dotnet build do projeto + sonda que imprime linhas 'CSOUT:...').
# Nada aqui depende de rede: o NuGet local (W:\nuget\packages) ja' tem as
# dependencias dos dois fixtures.

. (Join-Path (Split-Path $PSScriptRoot -Parent) 'tools\csoracle\CsOracle.ps1')

# ---- locais do arquivo (avaliados no load; capturados pelas closures) ---------
$csBench = if ($script:BenchRoot) { $script:BenchRoot } else { Split-Path $PSScriptRoot -Parent }
$csProbeDir = Join-Path $csBench 'tools\csoracle\probes'

$csFixtures = Get-Variable -Name Fixtures -ValueOnly -ErrorAction SilentlyContinue
$csWork = $null
try { $csWork = (Get-BenchConfig).work_dir } catch { $csWork = $null }
function Get-CsFixturePath([string]$Name) {
    if ($csFixtures -and $csFixtures[$Name]) { return $csFixtures[$Name] }
    if (-not $csWork) { throw "csharp: nao consegui resolver o diretorio de fixtures (sem bench.config.json?)" }
    return (Join-Path $csWork "fixtures\$Name")
}
$csSerilog = Get-CsFixturePath 'serilog'
$csFv = Get-CsFixturePath 'fluentvalidation'

# Dica de ambiente repetida nos prompts de escrita: nao ha 'dotnet' no PATH do
# modelo; o SDK local e' offline e o NuGet global ja' esta populado.
try {
    $csD = Get-CsDotnetCfg
    $csDotnetHint = "Compile com o SDK local (nao ha 'dotnet' no PATH): `$env:DOTNET_ROOT='$($csD.root)'; `$env:NUGET_PACKAGES='$($csD.nuget_packages)'; & '$($csD.exe)' build <projeto.csproj> -f netstandard2.0 -v:q --nologo. Tudo offline."
} catch {
    $csDotnetHint = "Use o SDK .NET local do ambiente (nao ha 'dotnet' no PATH); build offline."
}

# Sem 'repos' de proposito: o Initialize-BenchGraph so' entende os repos das outras
# suites (Go/Markdown). A ausencia do fixture e' detectada no Prepare (Copy-CsRepo lanca).

# ---- factories ----------------------------------------------------------------

# Tarefa de leitura que responde um CONJUNTO de arquivos .cs. -Find roda no fixture
# compartilhado (Gold tambem pode recalcular: nunca builda nem escreve la').
function New-CsFileSetTask {
    param($Id, $Name, $Prompt, $Fixture, $Find, $MinGold = 3, $MaxUsd = 0.60, $TimeoutS = 600)
    $st = @{ gold = @() }
    $find = $Find; $fx = $Fixture; $prompt = $Prompt; $id = $Id; $min = $MinGold
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'csharp'
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            $st.gold = @(& $find $fx)
            if ($st.gold.Count -lt $min) { throw "$id`: gabarito pequeno demais ($($st.gold.Count)) — fixture errado?" }
            $prompt
        }.GetNewClosure()
        Gold      = { $st.gold -join "`n" }.GetNewClosure()
        Score     = { param($a) Score-CsFiles -Answer $a -Gold $st.gold }.GetNewClosure()
    }
}

# Tarefa de leitura que responde UM endereco 'arquivo.cs:linha'.
function New-CsLocateTask {
    param($Id, $Name, $Prompt, $Fixture, $Regex, $Under = '', $Slack = 0, $MaxUsd = 0.50, $TimeoutS = 600)
    $st = @{ gold = '' }
    $fx = $Fixture; $re = $Regex; $un = $Under; $prompt = $Prompt; $id = $Id; $sl = $Slack
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'csharp'
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            $st.gold = Find-CsLocation -Root $fx -Regex $re -Under $un
            if (-not $st.gold) { throw "$id`: local nao encontrado no fixture ($re)" }
            $prompt
        }.GetNewClosure()
        Gold      = { $st.gold }.GetNewClosure()
        Score     = { param($a) Score-CsLocation -Answer $a -Gold $st.gold -Slack $sl }.GetNewClosure()
    }
}

# Tarefa de ESCRITA: Prepare copia o fixture (e planta o defeito, se houver),
# Gold aplica a correcao de referencia, Score roda build+sonda no $dir atual.
# -Extra (opcional) devolve um score adicional (ex.: grep de sobras) combinado
# com AND pelo Score-CsAll.
function New-CsWriteTask {
    param($Id, $Name, $Prompt, $Fixture, $Repo, $Plant, $Fix, $ProbeName, $ProbeFile, $Expect,
          $Extra = $null, $MaxUsd = 1.20, $TimeoutS = 900)
    $st = @{ dir = '' }
    $fx = $Fixture; $repo = $Repo; $plant = $Plant; $fix = $Fix; $prompt = $Prompt
    $pname = $ProbeName; $pf = Join-Path $csProbeDir $ProbeFile; $exp = $Expect
    $extra = $Extra; $tos = $TimeoutS
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'csharp'
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            $st.dir = $dir
            Copy-CsRepo -Src $fx -Dst $dir | Out-Null
            if ($plant) { & $plant $dir }
            $prompt
        }.GetNewClosure()
        Gold      = { if ($fix) { & $fix $st.dir }; 'ok' }.GetNewClosure()
        Score     = {
            param($a)
            $prog = Read-CsText -Path $pf
            $r = Score-CsBuildProbe -Dir $st.dir -Repo $repo -Name $pname -Program $prog -Expect $exp -TimeoutSec $tos
            if ($extra) { return (Score-CsAll -Results @($r, (& $extra $st.dir))) }
            return $r
        }.GetNewClosure()
    }
}

# ---- CS1: uso de um tipo (conjunto de arquivos) --------------------------------
New-CsFileSetTask -Id 'CS1' -Name 'C#: arquivos que usam LoggingLevelSwitch (Serilog)' -Fixture $csSerilog -MinGold 5 `
    -Find { param($root) Get-CsMentionFiles -Root $root -Regex '\bLoggingLevelSwitch\b' -Under 'src/Serilog' -NoTests } `
    -Prompt "No repositorio C# Serilog em $csSerilog, liste TODOS os arquivos .cs sob src/Serilog/ que mencionam o tipo ``LoggingLevelSwitch`` — declaracao da classe, campos, propriedades, parametros, uso ou comentario. Ignore arquivos de teste. Responda APENAS com os caminhos relativos a raiz do repositorio (comecando por src/), um por linha, sem comentarios, sem numeros de linha e sem texto extra."

# ---- CS2: implementacoes de uma interface (conjunto de arquivos) ---------------
New-CsFileSetTask -Id 'CS2' -Name 'C#: implementacoes declaradas de ILogEventSink (Serilog)' -Fixture $csSerilog -MinGold 5 `
    -Find { param($root) Get-CsImplFiles -Root $root -Interface 'ILogEventSink' -Under 'src/Serilog' -NoTests } `
    -Prompt "No repositorio C# Serilog em $csSerilog, liste TODOS os arquivos .cs sob src/Serilog/ que declaram uma classe, struct ou record cujo cabecalho cita ``ILogEventSink`` na lista de bases/interfaces implementadas. Vale a declaracao do proprio tipo (inclusive quando ``ILogEventSink`` nao e' a primeira base da lista) — nao valem mencoes em comentario, atributo ou uso. Nao inclua interfaces nem arquivos de teste. Responda APENAS com os caminhos relativos a raiz do repositorio, um por linha, sem texto extra."

# ---- CS3: assinatura publica + comportamento por chamador ---------------------
New-CsWriteTask -Id 'CS3' -Name 'C#: novo parametro em IsValidName e ajuste dos chamadores' `
    -Fixture $csSerilog -Repo 'serilog' -ProbeName 'cs3' -ProbeFile 'cs3-isvalidname.cs' `
    -Expect @('valid=True', 'blank=False', 'throws=True', 'constrthrows=True', 'forcontext=True', 'bindproperty=True', 'name=Ok') `
    -Plant { param($dir) } `
    -Fix {
        param($dir)
        $f = Join-Path $dir 'src\Serilog\Events\LogEventProperty.cs'
        # Serilog trata CS1573 como ERRO (doc file + warnings-as-errors): sem a tag do
        # parametro novo o build de referencia nao compila.
        $tagFrom = '/// <param name="name">The name to check.</param>' + "`n"
        $tagTo = '/// <param name="name">The name to check.</param>' + "`n" +
                 '    /// <param name="throwOnInvalid">Whether to throw when the name is invalid.</param>' + "`n"
        Edit-CsText -Path $f -Once -From $tagFrom -To $tagTo
        Edit-CsText -Path $f -Once `
            -From 'public static bool IsValidName([NotNullWhen(true)] string? name) => !string.IsNullOrWhiteSpace(name);' `
            -To @'
public static bool IsValidName([NotNullWhen(true)] string? name, bool throwOnInvalid)
    {
        var valid = !string.IsNullOrWhiteSpace(name);
        if (!valid && throwOnInvalid) throw new ArgumentException($"Property {nameof(name)} must not be empty or whitespace.", nameof(name));
        return valid;
    }
'@
        Edit-CsText -Path $f -From 'if (!IsValidName(name)) throw new ArgumentException' -To 'if (!IsValidName(name, true)) throw new ArgumentException'
        Edit-CsText -Path (Join-Path $dir 'src\Serilog\Core\Logger.cs') `
            -From 'if (!LogEventProperty.IsValidName(propertyName))' -To 'if (!LogEventProperty.IsValidName(propertyName, false))'
    } `
    -Prompt "No projeto C# (Serilog v3.1.1) copiado em $dir, mude a assinatura do metodo publico ``LogEventProperty.IsValidName(string? name)`` para receber um segundo parametro bool ``throwOnInvalid``: nome invalido + throwOnInvalid=true lanca ArgumentException; nome invalido + throwOnInvalid=false devolve false sem lancar; nome valido devolve true nos dois casos. Depois ajuste TODOS os chamadores em src/Serilog para que o comportamento observavel de hoje continue identico (leia o que cada chamador faz com o resultado antes de escolher o valor que ele passa). $csDotnetHint Nao mexa em nada fora de src/Serilog e nao altere mensagens de excecao existentes."

# ---- CS4: renomear metodo publico e atualizar todos os call sites -------------
New-CsWriteTask -Id 'CS4' -Name 'C#: renomear SelfLog.WriteLine e atualizar os call sites' `
    -Fixture $csSerilog -Repo 'serilog' -ProbeName 'cs4' -ProbeFile 'cs4-selflogwrite.cs' `
    -Expect @('hasmsg=True', 'newline=True', 'lines=1') `
    -Plant { param($dir) } `
    -Fix {
        param($dir)
        $self = Join-Path $dir 'src\Serilog\Debugging\SelfLog.cs'
        Edit-CsText -Path $self -Once -From 'public static void WriteLine(string format,' -To 'public static void Write(string format,'
        foreach ($rel in (Get-CsRepoFiles -Root $dir)) {
            $p = Join-Path $dir ($rel -replace '/', '\')
            $t = Read-CsText -Path $p
            if ($t.IndexOf('SelfLog.WriteLine(') -ge 0) { Set-CsText -Path $p -Text $t.Replace('SelfLog.WriteLine(', 'SelfLog.Write(') }
        }
    } `
    -Extra {
        param($dir)
        $rem = @(Get-CsMentionFiles -Root $dir -Regex 'SelfLog\.WriteLine')
        if ($rem.Count -eq 0) { return @{ correct = $true; precision = 1.0; recall = 1.0 } }
        return @{ correct = $false; precision = 0.0; recall = 0.0 }
    } `
    -Prompt "No projeto C# (Serilog v3.1.1) copiado em $dir, renomeie o metodo publico estatico ``Serilog.Debugging.SelfLog.WriteLine(string format, object? arg0 = null, object? arg1 = null, object? arg2 = null)`` para ``SelfLog.Write`` — mesma classe, mesma assinatura, mesmo comportamento (a observacao no XML doc dizendo que o sufixo 'Line' e' historico deve ser ignorada: a renomeacao e' intencional). Atualize TODOS os call sites desse metodo no repositorio, inclusive nos arquivos de test/ (os projetos de teste NAO compilam neste ambiente, sem xunit offline — nao tente builda-los, apenas atualize as chamadas). Cuidado: existem outros ``WriteLine`` no repositorio (por exemplo o ``output.WriteLine(m)`` dentro de ``SelfLog.Enable(TextWriter)``) que NAO sao esse metodo e nao devem mudar de nome nem perder o fim de linha. Ao final deve restar ZERO ocorrencias de ``SelfLog.WriteLine`` no repositorio e o projeto deve compilar. $csDotnetHint"

# ---- CS5: localizar uma comparacao (endereco exato) ---------------------------
New-CsLocateTask -Id 'CS5' -Name 'C#: onde o nivel minimo do switch e comparado (Serilog)' -Fixture $csSerilog `
    -Regex 'level >= _levelSwitch\.MinimumLevel' -Under 'src/Serilog' `
    -Prompt "No repositorio C# Serilog em $csSerilog, no caminho de ``Logger.IsEnabled(LogEventLevel)``: em qual arquivo e linha esta' a comparacao que usa ``_levelSwitch.MinimumLevel`` para decidir se o evento passa? Responda APENAS com um endereco no formato ``arquivo.cs:linha``, com caminho relativo a raiz do repositorio, sem mais nada."

# ---- CS6: bug plantado em validacao de comprimento (FluentValidation) ---------
New-CsWriteTask -Id 'CS6' -Name 'C#: bug de fronteira nos validadores de comprimento (FluentValidation)' `
    -Fixture $csFv -Repo 'fluentvalidation' -ProbeName 'cs6' -ProbeFile 'cs6-length.cs' `
    -Expect @('min_ok=True', 'min_curto=False', 'min_null=True', 'max_ok=True', 'max_longo=False', 'exato_ok=True', 'exato_curto=False', 'faixa_min=True', 'faixa_max=True', 'faixa_fora=False') `
    -Plant { param($dir) Edit-CsText -Path (Join-Path $dir 'src\FluentValidation\Validators\LengthValidator.cs') -Once -From 'if (length < min ||' -To 'if (length <= min ||' } `
    -Fix { param($dir) Edit-CsText -Path (Join-Path $dir 'src\FluentValidation\Validators\LengthValidator.cs') -Once -From 'if (length <= min ||' -To 'if (length < min ||' } `
    -Prompt "Projeto C# (FluentValidation 11.9.2) copiado em $dir. Bug relatado por um usuario: ``RuleFor(x => x.Nome).MinimumLength(3)`` passou a REJEITAR um valor com exatamente 3 caracteres, e ``Length(4)`` (comprimento exato) rejeita um valor com exatamente 4. Valores mais longos continuam validos e ``MaximumLength`` parece certo. Encontre a causa nos validadores de comprimento de src/FluentValidation e corrija a comparacao, sem afrouxar os limites (nao vale remover a regra nem mudar a API publica): ``MinimumLength(n)`` deve aceitar exatamente n, ``Length(n)`` (exato) deve aceitar exatamente n e rejeitar n-1, e ``Length(min,max)`` deve aceitar as duas pontas. $csDotnetHint"

# ---- CS7: pergunta de arquitetura (nomes + endereco) --------------------------
# -GoldText recebe o local encontrado e monta a resposta de referencia.
function New-CsQaTask {
    param($Id, $Name, $Prompt, $Fixture, $FindRegex, $Under, $Patterns, $GoldText, $MaxUsd = 0.60, $TimeoutS = 600)
    $st = @{ loc = '' }
    $fx = $Fixture; $re = $FindRegex; $un = $Under; $prompt = $Prompt; $id = $Id
    $pats = $Patterns; $gold = $GoldText
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'csharp'
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            $st.loc = Find-CsLocation -Root $fx -Regex $re -Under $un
            if (-not $st.loc) { throw "$id`: local nao encontrado no fixture ($re)" }
            $prompt
        }.GetNewClosure()
        Gold      = { & $gold $st.loc }.GetNewClosure()
        Score     = {
            param($a)
            Score-CsAll -Results @(
                (Score-CsRegex -Answer $a -Patterns $pats),
                (Score-CsLocation -Answer $a -Gold $st.loc)
            )
        }.GetNewClosure()
    }
}

New-CsQaTask -Id 'CS7' -Name 'C#: cadeia de override de nivel por SourceContext (Serilog)' -Fixture $csSerilog `
    -FindRegex '_overrideMap\.GetEffectiveLevel' -Under 'src/Serilog' `
    -Patterns @('LevelOverrideMap', 'GetEffectiveLevel', 'LoggingLevelSwitch') `
    -GoldText { param($loc) "classe=LevelOverrideMap`nmetodo=GetEffectiveLevel`nout=LoggingLevelSwitch`nlocal=$loc" } `
    -Prompt "No repositorio C# Serilog em ${csSerilog}: quando a configuracao declara overrides de nivel minimo por prefixo de SourceContext (``MinimumLevel.Override(...)``), responda com nomes exatos (a) qual classe guarda os overrides e calcula o nivel efetivo para um contexto; (b) qual metodo publico dessa classe faz esse calculo; (c) em que arquivo.cs:linha o caminho de escrita do ``Logger`` consulta essa classe; (d) qual classe essa consulta devolve por parametro ``out`` junto do nivel efetivo quando existe um switch. Responda em no maximo 4 linhas, uma por item, no formato ``classe=Nome`` / ``metodo=Nome`` / ``local=arquivo.cs:linha`` / ``out=Nome``."

# ---- CS8: bug plantado no casing do elemento {Level} (Serilog) ----------------
New-CsWriteTask -Id 'CS8' -Name 'C#: casing trocado em {Level:wN}/{Level:uN} (Serilog)' `
    -Fixture $csSerilog -Repo 'serilog' -ProbeName 'cs8' -ProbeFile 'cs8-levelcasing.cs' `
    -Expect @('w3=wrn', 'u3=WRN', 't3=Wrn', 'w2=wn', 'w5=warni', 'full=Warning') `
    -Plant {
        param($dir)
        Edit-CsText -Path (Join-Path $dir 'src\Serilog\Formatting\Display\LevelOutputFormat.cs') -Once `
            -From "'w' => GetLevelMoniker(_lowerCaseLevelMap, value, width),`n            'u' => GetLevelMoniker(_upperCaseLevelMap, value, width)," `
            -To   "'w' => GetLevelMoniker(_upperCaseLevelMap, value, width),`n            'u' => GetLevelMoniker(_lowerCaseLevelMap, value, width),"
    } `
    -Fix {
        param($dir)
        Edit-CsText -Path (Join-Path $dir 'src\Serilog\Formatting\Display\LevelOutputFormat.cs') -Once `
            -From "'w' => GetLevelMoniker(_upperCaseLevelMap, value, width),`n            'u' => GetLevelMoniker(_lowerCaseLevelMap, value, width)," `
            -To   "'w' => GetLevelMoniker(_lowerCaseLevelMap, value, width),`n            'u' => GetLevelMoniker(_upperCaseLevelMap, value, width),"
    } `
    -Prompt "Projeto C# (Serilog v3.1.1) copiado em $dir. Bug relatado: no template de saida, ``{Level:w3}`` esta' saindo em MAIUSCULAS e ``{Level:u3}`` esta' saindo em minusculas; ``{Level:t3}`` e o ``{Level}`` sem sufixo continuam certos. Encontre a causa em src/Serilog e corrija: as formas ``w``/``u``/``t`` devem produzir, respectivamente, nivel em minusculas, MAIUSCULAS e Capitalizado, mantendo o truncamento pela largura (w2, w3, w5...) e sem mexer na API publica. $csDotnetHint"
