# tasks/docs.ps1 — estrato 'docs' (D1..D6): recuperacao de fato em documentacao Markdown.
#
# Contrato igual ao das outras suites: $TaskTable[Id] = @{ name; stratum; repos; Prepare($dir)
# -> prompt; Gold() -> resposta correta; Score($answer) -> @{correct;precision;recall} },
# mais 'max_usd'/'timeout_s' (orcamento por tarefa, lidos pelo Run-Bench.ps1).
#
# Fixture: corpus SINTETICO e DETERMINISTICO de 38 MDs (80% inchados: snapshots datados,
# historico, secoes antigas) + 2 arquivos de fato, gerado por tools/docs/New-DocsCorpus.ps1
# em <work_dir>\fixtures\docs pelo Initialize-Bench.ps1. Tarefas so' de LEITURA: o corpus e'
# compartilhado e nunca e' escrito aqui (mesmo modelo das suites cobra/hugo).
#
# Diferente das outras suites, o gabarito NAO e' derivado do texto em disco: e' a propria
# tabela que gerou o corpus (Get-DocsFacts). Para nao virar gabarito-de-borracha, o Prepare
# roda Test-DocsFacts, que LANCA se o corpus tiver divergido da tabela — fixture corrompida
# falha a tarefa em vez de aceitar uma resposta errada em silencio.
#
# ARMADILHA (igual a csharp.ps1): scriptblock com .GetNewClosure() so' enxerga o escopo LOCAL
# de quem o criou. Por isso as factories recebem caminhos/prompt como PARAMETRO e so' chamam
# funcoes globais (Get-Docs*/Test-Docs*/Score-Docs*).

$docsBench = if ($script:BenchRoot) { $script:BenchRoot } else { Split-Path $PSScriptRoot -Parent }
. (Join-Path $docsBench 'tools\docs\New-DocsCorpus.ps1')
. (Join-Path $docsBench 'tools\docs\DocsOracle.ps1')

$docsFixtures = Get-Variable -Name Fixtures -ValueOnly -ErrorAction SilentlyContinue
$docsWork = $null
try { $docsWork = (Get-BenchConfig).work_dir } catch { $docsWork = $null }
function Get-DocsFixturePath([string]$Name) {
    if ($docsFixtures -and $docsFixtures[$Name]) { return $docsFixtures[$Name] }
    if (-not $docsWork) { throw "docs: nao consegui resolver o diretorio de fixtures (sem bench.config.json?)" }
    return (Join-Path $docsWork "fixtures\$Name")
}
$docsCorpus = Get-DocsFixturePath 'docs'
$docsF = Get-DocsFacts

# Tarefa de leitura: cada fato vira um par Require/Forbid sobre a resposta textual.
# -GoldText monta a resposta de referencia (usada pelo -DryRun e pelos relatorios).
function New-DocsQaTask {
    param($Id, $Name, $Prompt, $Require, $Forbid = @(), $GoldText, $MaxUsd = 0.50, $TimeoutS = 600)
    $st = @{ ok = $false }
    $corpus = $docsCorpus; $prompt = $Prompt; $req = $Require; $for = $Forbid
    $gt = $GoldText; $id = $Id
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'docs'
        repos     = @('docs')
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            # fixture corrompida => falha ruidosa, nunca gabarito silenciosamente errado
            $st.ok = Test-DocsFacts -Corpus $corpus
            $prompt
        }.GetNewClosure()
        Gold      = { & $gt }.GetNewClosure()
        Score     = { param($a) Score-DocsRegex -Answer $a -Require $req -Forbid $for }.GetNewClosure()
    }
}

# Tarefa de leitura que responde um CONJUNTO de caminhos .md.
function New-DocsFileSetTask {
    param($Id, $Name, $Prompt, $Gold, $MaxUsd = 0.50, $TimeoutS = 600)
    $st = @{ ok = $false }
    $corpus = $docsCorpus; $prompt = $Prompt; $gold = $Gold; $id = $Id
    $TaskTable[$Id] = @{
        name      = $Name
        stratum   = 'docs'
        repos     = @('docs')
        max_usd   = $MaxUsd
        timeout_s = $TimeoutS
        Prepare   = {
            param($dir)
            $st.ok = Test-DocsFacts -Corpus $corpus
            $prompt
        }.GetNewClosure()
        Gold      = { $gold -join "`n" }.GetNewClosure()
        Score     = { param($a) Score-DocsFiles -Answer $a -Gold $gold }.GetNewClosure()
    }
}

# ---- D1: fato simples num doc canonico (com snapshots defasados como distrator) -------
$svcBeacon = ($docsF.services | Where-Object { $_.name -eq 'beacon-api' })
New-DocsQaTask -Id 'D1' -Name 'Docs: porta de escuta do beacon-api' `
    -Require @('\b7411\b') -Forbid @('\b7311\b', '\b7211\b') `
    -GoldText { '7411' } `
    -Prompt "Corpus de documentação Markdown em $docsCorpus (domínio fictício; há docs canônicos, snapshots datados e docs curtos). Em qual porta o serviço ``beacon-api`` está configurado para escutar? Cuidado: parte da documentação é histórica e traz valores antigos. Responda em uma linha, apenas com o número da porta."

# ---- D2: decisão (ADR) que restringe um comportamento + o limite que ela impõe ---------
New-DocsQaTask -Id 'D2' -Name 'Docs: ADR que restringe as retentativas da ferrite-queue' `
    -Require @('ADR[- ]?0*4\b', '\b5\b') -Forbid @('ADR[- ]?0*[1235678]\b') `
    -GoldText { 'ADR-004; 5 tentativas' } `
    -Prompt "No corpus em $docsCorpus, qual decisão de arquitetura (ADR) restringe como os consumidores da fila ``ferrite-queue`` devem tentar novamente depois de uma falha, e quantas tentativas ela permite por mensagem? Responda em uma linha no formato ``ADR-00N; N tentativas``."

# ---- D3: qual doc cita um símbolo (busca por conteúdo, nome do arquivo não ajuda) ------
New-DocsFileSetTask -Id 'D3' -Name 'Docs: único MD que cita o símbolo FerriteAck' `
    -Gold @($docsF.symbolOnlyIn.doc) `
    -Prompt "No corpus em $docsCorpus, exatamente UM arquivo Markdown cita o símbolo ``$($docsF.symbolOnlyIn.symbol)``. Qual é? O nome do arquivo não corresponde ao nome do símbolo — procure pelo conteúdo. Responda apenas com o caminho relativo à raiz do corpus, terminando em .md, sem mais nada."

# ---- D4: agregação em 2+ docs (quem depende de vault-index e em que porta) -------------
New-DocsQaTask -Id 'D4' -Name 'Docs: serviços que dependem de vault-index e suas portas' `
    -Require @('quill-render', '\b7415\b', 'tinbox-auth', '\b7417\b') `
    -Forbid @('\b7411\b', '\b7412\b', '\b7413\b', '\b7416\b', '\b7418\b', '\b8512\b') `
    -GoldText { 'quill-render (7415); tinbox-auth (7417)' } `
    -Prompt "No corpus em $docsCorpus, quais serviços declaram depender de ``vault-index`` e em que porta cada um deles escuta (valor ATUAL, não histórico)? Responda em no máximo duas linhas, uma por serviço, no formato ``nome; porta``."

# ---- D5: doc velho #1 — o MD canônico diz uma porta, a config diz outra ----------------
New-DocsQaTask -Id 'D5' -Name 'Docs: porta real do mesh-relay (doc defasado)' `
    -Require @('\b8512\b') -Forbid @('\b8312\b', '\b8212\b') `
    -GoldText { '8512' } `
    -Prompt "No corpus em $docsCorpus, o documento do serviço ``mesh-relay`` pode estar defasado. Qual é a porta em que o ``mesh-relay`` está realmente configurado para escutar NESTE corpus? A documentação em prosa não é a fonte da verdade para configuração — confira a fonte de configuração versionada antes de responder. Responda em uma linha, apenas com o número da porta."

# ---- D6: doc velho #2 — o MD afirma um default que o código contradiz ------------------
New-DocsQaTask -Id 'D6' -Name 'Docs: default real do cache do quill-render (doc defasado)' `
    -Require @('(?i)\b(false|n[aã]o|desabilitad\w*|desligad\w*|disabled)\b') -Forbid @('(?i)\btrue\b', '\bsim\b') `
    -GoldText { 'não (false): EnableRenderCache vem desabilitado por padrão' } `
    -Prompt "No corpus em $docsCorpus, o cache de renderização do serviço ``quill-render`` vem habilitado por padrão? A documentação do serviço pode estar defasada — o código da pasta ``src/`` é a fonte da verdade. Responda em uma linha começando por ``sim`` ou ``não``, e cite o nome exato da opção que define esse default."
