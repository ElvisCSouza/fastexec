# tools/docs/New-DocsCorpus.ps1 — corpus de Markdown do estrato 'docs'.
#
# Fixture SINTETICA e DETERMINISTICA (sem LLM, sem rede): 38 MDs de dominios
# ficticios + 2 arquivos de fato (config/codigo). Rodar duas vezes produz byte a
# byte o mesmo corpus, entao o gabarito pode ser derivado da MESMA tabela
# (Get-DocsFacts) sem risco de corrupcao da fixture.
#
# Por que existe: as outras suites medem recuperacao em codigo Go/Markdown real;
# faltava o caso mais comum do dia a dia (documentacao interna inchada, com
# snapshots datados e docs defasados).
#
# Formato do corpus (tudo relativo a raiz):
#   servicos/<svc>.md            8  doc canonico do servico (porta, dono, deps, simbolo)
#   historico/<svc>-<YYYY-MM>.md 16 snapshots datados, valores DEFASADOS de proposito
#   decisoes/adr-00N-<slug>.md   8  ADRs
#   enxuto/<topico>.md           6  docs curtos
#   infra/servicos.yaml             porta VERDADEIRA de cada servico (D5)
#   src/QuillOptions.cs             default VERDADEIRO do cache do quill-render (D6)
#
# ARMADILHAS deliberadas (as tarefas D5/D6 vivem delas):
#   * servicos/mesh-relay.md mostra a porta 8312, defasada; infra/servicos.yaml
#     traz 8512. O doc esta' errado, a config esta' certa.
#   * servicos/quill-render.md afirma que o cache vem habilitado; o default real
#     em src/QuillOptions.cs e' false.
#   * o simbolo 'FerriteAck' aparece em UM unico MD (enxuto/filas-retentativas.md),
#     e NAO no doc do servico ferrite-queue (que cita 'FerriteEnqueue').
#
# Superficie publica:
#   Get-DocsFacts              -> tabela de fatos (fonte unica do gerador e do gabarito)
#   New-DocsCorpus -Dest       -> (re)gera o corpus em $Dest; devolve @{files;mds}
#   Test-DocsFacts -Corpus     -> LANCA se o corpus em disco divergir da tabela

function global:Get-DocsFacts {
    return @{
        services = @(
            [pscustomobject]@{ name = 'beacon-api';    port = 7411; owner = 'time-plataforma'; deps = @('ledger-sync');   sym = 'BeaconRoute';    stale = @(7311, 7211) }
            [pscustomobject]@{ name = 'ledger-sync';   port = 7412; owner = 'time-dados';      deps = @('ferrite-queue'); sym = 'LedgerCursor';   stale = @(7312, 7212) }
            [pscustomobject]@{ name = 'vault-index';   port = 7413; owner = 'time-dados';      deps = @();                sym = 'VaultShard';     stale = @(7313, 7213) }
            [pscustomobject]@{ name = 'mesh-relay';    port = 8512; owner = 'time-rede';       deps = @('beacon-api');    sym = 'MeshHop';        stale = @(8312, 8212) }
            [pscustomobject]@{ name = 'quill-render';  port = 7415; owner = 'time-produto';    deps = @('vault-index');   sym = 'QuillCanvas';    stale = @(7315, 7215) }
            [pscustomobject]@{ name = 'orbit-cache';   port = 7416; owner = 'time-plataforma'; deps = @();                sym = 'OrbitSlot';      stale = @(7316, 7216) }
            [pscustomobject]@{ name = 'tinbox-auth';   port = 7417; owner = 'time-seguranca';  deps = @('vault-index');   sym = 'TinboxClaim';    stale = @(7317, 7217) }
            [pscustomobject]@{ name = 'ferrite-queue'; port = 7418; owner = 'time-dados';      deps = @();                sym = 'FerriteEnqueue'; stale = @(7318, 7218) }
        )
        # valor DEFASADO que o doc canonico do mesh-relay ainda exibe (o verdadeiro esta' em infra/servicos.yaml)
        meshDocPort       = 8312
        quillCacheDefault = $false
        symbolOnlyIn      = @{ symbol = 'FerriteAck'; doc = 'enxuto/filas-retentativas.md' }
        adrRetry          = @{ id = 'ADR-004'; slug = 'retentativas-na-fila'; attempts = 5 }
        staleMonths       = @('2023-04', '2023-11')
        topics            = @(
            [pscustomobject]@{ file = 'enxuto/filas-retentativas.md';  sym = 'FerriteAck';         title = 'Filas e retentativas' }
            [pscustomobject]@{ file = 'enxuto/cache-e-invalidacao.md'; sym = 'OrbitSlot';          title = 'Cache e invalidação' }
            [pscustomobject]@{ file = 'enxuto/observabilidade.md';     sym = 'BeaconRoute';        title = 'Observabilidade' }
            [pscustomobject]@{ file = 'enxuto/segredos.md';            sym = 'VaultSeal';          title = 'Segredos' }
            [pscustomobject]@{ file = 'enxuto/versionamento.md';       sym = 'LedgerRevision';     title = 'Versionamento de schemas' }
            [pscustomobject]@{ file = 'enxuto/limites.md';             sym = 'TinboxQuota';        title = 'Limites de carga' }
        )
        adrs              = @(
            [pscustomobject]@{ id = 'ADR-001'; slug = 'padronizacao-de-observabilidade'; title = 'Padronização de observabilidade'; owner = 'time-plataforma' }
            [pscustomobject]@{ id = 'ADR-002'; slug = 'versionamento-de-schemas';        title = 'Versionamento de schemas';        owner = 'time-dados' }
            [pscustomobject]@{ id = 'ADR-003'; slug = 'isolamento-de-tenant';            title = 'Isolamento de tenant';            owner = 'time-seguranca' }
            [pscustomobject]@{ id = 'ADR-004'; slug = 'retentativas-na-fila';            title = 'Retentativas na fila';            owner = 'time-dados' }
            [pscustomobject]@{ id = 'ADR-005'; slug = 'rotacao-de-segredos';             title = 'Rotação de segredos';             owner = 'time-seguranca' }
            [pscustomobject]@{ id = 'ADR-006'; slug = 'politica-de-tokens';              title = 'Política de tokens';              owner = 'time-seguranca' }
            [pscustomobject]@{ id = 'ADR-007'; slug = 'limites-de-carga';                title = 'Limites de carga';                owner = 'time-plataforma' }
            [pscustomobject]@{ id = 'ADR-008'; slug = 'retencao-de-logs';                title = 'Retenção de logs';                owner = 'time-plataforma' }
        )
    }
}

# ---- escrita -------------------------------------------------------------------
$global:DocsCorpusUtf8 = New-Object Text.UTF8Encoding $false

function Write-DocsFile {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $d = Split-Path $Path -Parent
    if ($d -and -not (Test-Path -LiteralPath $d)) { New-Item -ItemType Directory -Force $d | Out-Null }
    [IO.File]::WriteAllText($Path, ($Text -replace "`r`n", "`n"), $global:DocsCorpusUtf8)
}

# Prosa de enchimento: infla os docs sem introduzir nenhum numero/identificador que
# possa colidir com o gabarito (so' texto).
function Get-DocsFiller([string]$Name, [int]$Seed) {
    $a = @(
        "O $Name foi extraído do monólito original durante a separação por domínio e desde então evolui de forma independente.",
        "A operação é feita por dois times em fusos diferentes, então qualquer mudança de contrato precisa de janela combinada.",
        "Os painéis de latência p95 ficam no dashboard compartilhado e são revisados na reunião semanal de plataforma.",
        "A fila de trabalho interna é drenada antes do deploy e o processo só é encerrado depois que o backlog zera."
    )
    $b = @(
        "Em caso de incidente, o primeiro passo é conferir o painel de saturação antes de tocar em qualquer configuração.",
        "O rollback é sempre para a versão anterior do pacote, nunca para um commit intermediário.",
        "Métricas de negócio e métricas técnicas vivem em namespaces separados para não misturar alerta de produto com alerta de infraestrutura.",
        "Todo alerta precisa de um runbook curto; alerta sem runbook é silenciado na revisão trimestral."
    )
    $c = @(
        "A configuração é carregada de um único arquivo versionado e sobreposta por variáveis de ambiente do orquestrador.",
        "Nenhum parâmetro sensível fica no repositório: segredos são injetados no momento do start.",
        "O health check responde em rota separada e não depende de dependência externa para não gerar falso positivo em cascata.",
        "Os testes de contrato rodam no pipeline e falham o build quando o schema publicado muda sem aviso."
    )
    return ($a[$Seed % $a.Count] + "`n`n" + $b[($Seed + 1) % $b.Count] + "`n`n" + $c[($Seed + 2) % $c.Count])
}

# ---- gerador -------------------------------------------------------------------
function global:New-DocsCorpus {
    param([Parameter(Mandatory)][string]$Dest)

    $f = Get-DocsFacts
    if (Test-Path -LiteralPath $Dest) { Remove-Item -LiteralPath $Dest -Recurse -Force }
    New-Item -ItemType Directory -Force $Dest | Out-Null

    $i = 0
    foreach ($s in $f.services) {
        # doc canonico: correto, exceto o mesh-relay (porta defasada de proposito)
        $shownPort = if ($s.name -eq 'mesh-relay') { $f.meshDocPort } else { $s.port }
        $depsTxt = if ($s.deps.Count) { ($s.deps -join ', ') } else { '—' }
        $extra = ''
        if ($s.name -eq 'quill-render') {
            $extra = "`n- **Cache de renderização:** habilitado por padrão (``sim``)`n"
        }
        $body = @"
# Serviço $($s.name)

**Status:** ativo
**Dono:** $($s.owner)
**Porta:** $shownPort
**Depende de:** $depsTxt
**Símbolo principal:** ``$($s.sym)``
$extra
## Visão geral

$(Get-DocsFiller $s.name $i)

## Configuração

| Parâmetro | Valor | Observação |
|---|---|---|
| ``port`` | $shownPort | porta de escuta em produção |
| ``log_level`` | ``info`` | |
| ``shutdown_grace_s`` | 20 | drena a fila interna antes de encerrar |

$(Get-DocsFiller $s.name ($i + 1))

## Histórico de alterações

- 2024-01-18 — migração de runtime; painéis atualizados.
- 2024-03-02 — revisão do contrato de health check.
- 2024-06-11 — ajuste de limites de conexão.
"@
        Write-DocsFile (Join-Path $Dest "servicos\$($s.name).md") $body

        # snapshots datados: valores DEFASADOS (porta antiga, dono antigo)
        for ($k = 0; $k -lt $f.staleMonths.Count; $k++) {
            $m = $f.staleMonths[$k]
            $body = @"
# Snapshot $($s.name) — $m

> Documento histórico, mantido só para rastreabilidade. Os valores abaixo refletem o
> estado do serviço em $m e **podem estar defasados**.

- **Dono (à época):** $($s.owner)-antigo
- **Porta (à época):** $($s.stale[$k])
- **Runtime:** .NET 6
- **Símbolo citado:** ``$($s.name)Legacy``
- **Depende de (à época):** $depsTxt

## Mudanças desde o snapshot anterior

$(Get-DocsFiller $s.name ($i + $k + 2))

## Pendências registradas na época

- revisar limite de conexões;
- padronizar nome dos painéis;
- conferir se o ``$($s.name)Legacy`` ainda é usado por algum consumidor.
"@
            Write-DocsFile (Join-Path $Dest "historico\$($s.name)-$m.md") $body
        }
        $i++
    }

    # ADRs
    foreach ($a in $f.adrs) {
        $decision = "Aprovado o padrão descrito nesta ADR para todos os serviços do domínio $($a.owner)."
        $conseq = @"
- vale para todo serviço novo do domínio $($a.owner);
- revisão obrigatória a cada doze meses.
"@
        if ($a.id -eq $f.adrRetry.id) {
            $decision = @"
Os consumidores da fila ``ferrite-queue`` devem tentar novamente após falha, no máximo
**$($f.adrRetry.attempts)** tentativas por mensagem, com recuo exponencial e sem reenfileirar
a mensagem antes do fim da janela de recuo. Depois da última tentativa a mensagem vai para a
fila morta e precisa de intervenção humana.
"@
            $conseq = @"
- um pico de falha transitória pode custar até $($f.adrRetry.attempts) vezes o custo normal da mensagem;
- a fila morta precisa de painel e de dono explícito;
- qualquer consumidor novo da ``ferrite-queue`` herda esta decisão.
"@
        }
        $body = @"
# $($a.id) — $($a.title)

- **Status:** aceita
- **Data:** 2024-0$(([int]($a.id -replace '\D', '')) % 9 + 1)-1$(([int]($a.id -replace '\D', '')) % 9)
- **Proponente:** $($a.owner)

## Contexto

$(Get-DocsFiller $a.title ([int]($a.id -replace '\D', '')))

## Decisão

$decision

## Consequências

$conseq
## Alternativas descartadas

- manter o comportamento anterior e resolver caso a caso;
- empurrar a responsabilidade para o time consumidor.
"@
        Write-DocsFile (Join-Path $Dest "decisoes\$($a.id.ToLower())-$($a.slug).md") $body
    }

    # docs enxutos
    foreach ($t in $f.topics) {
        $body = @"
# $($t.title)

Doc curto de referência. Sem histórico, sem snapshot: se algo aqui divergir do código, o código vence.

- **Símbolo de referência:** ``$($t.sym)``
- **Última revisão:** 2024-09-30

## Regras

1. Toda mudança de contrato entra com versão nova.
2. Configuração vem do arquivo versionado, nunca de valor embutido no binário.
3. Alerta novo só entra com runbook.
4. Este documento é revisado a cada trimestre e encurtado quando cresce.
"@
        Write-DocsFile (Join-Path $Dest ($t.file -replace '/', '\')) $body
    }

    # ---- arquivos de FATO (nao-MD): a verdade sobre o que os docs defasados erram
    $yaml = New-Object System.Collections.Generic.List[string]
    $yaml.Add('# infra/servicos.yaml — registro de servicos do cluster (fonte da verdade da porta de escuta)')
    $yaml.Add('servicos:')
    foreach ($s in $f.services) { $yaml.Add("  $($s.name):"); $yaml.Add("    porta: $($s.port)"); $yaml.Add("    dono: $($s.owner)") }
    Write-DocsFile (Join-Path $Dest 'infra\servicos.yaml') (($yaml -join "`n") + "`n")

    $cs = @'
// src/QuillOptions.cs — opcoes de runtime do quill-render.
namespace Quill.Render;

public sealed class QuillOptions
{
    /// <summary>Porta de escuta. Sobrescrita por infra/servicos.yaml no deploy.</summary>
    public int Port { get; set; } = 7415;

    /// <summary>Cache de renderizacao. Desligado por padrao: so' liga por configuracao explicita.</summary>
    public bool EnableRenderCache { get; set; } = false;

    /// <summary>Teto de entradas do cache quando ligado.</summary>
    public int CacheEntries { get; set; } = 512;
}
'@
    Write-DocsFile (Join-Path $Dest 'src\QuillOptions.cs') ($cs + "`n")

    $mds = @(Get-ChildItem -LiteralPath $Dest -Recurse -File -Filter *.md)
    return @{ files = @(Get-ChildItem -LiteralPath $Dest -Recurse -File).Count; mds = $mds.Count }
}

# ---- auto-teste: garante que o gabarito (Get-DocsFacts) casa com o corpus em disco
function global:Read-DocsText {
    param([Parameter(Mandatory)][string]$Path)
    return [string](Get-Content -LiteralPath $Path -Raw -Encoding UTF8)
}

function global:Test-DocsFacts {
    param([Parameter(Mandatory)][string]$Corpus)
    $f = Get-DocsFacts
    $fail = New-Object System.Collections.Generic.List[string]

    $read = { param($rel) Read-DocsText -Path (Join-Path $Corpus ($rel -replace '/', '\')) }
    $need = {
        param($rel, $pat, $want)
        $t = & $read $rel
        $has = [regex]::IsMatch($t, $pat)
        if ($has -ne $want) { $fail.Add("$rel : esperava '$pat' presente=$want, obtive $has") }
    }

    # D1/D4: docs canonicos corretos
    & $need 'servicos/beacon-api.md' '\b7411\b' $true
    & $need 'servicos/ledger-sync.md' '\b7412\b' $true
    & $need 'servicos/quill-render.md' '\b7415\b' $true
    & $need 'servicos/tinbox-auth.md' '\b7417\b' $true
    & $need 'servicos/ledger-sync.md' 'ferrite-queue' $true

    # D5: doc defasado x config verdadeira
    & $need 'servicos/mesh-relay.md' '\b8312\b' $true
    & $need 'servicos/mesh-relay.md' '\b8512\b' $false
    & $need 'infra/servicos.yaml' 'mesh-relay:\s*\n\s*porta:\s*8512' $true
    & $need 'historico/mesh-relay-2023-04.md' '\b8312\b' $true

    # D6: doc afirma cache ligado; codigo diz false
    & $need 'servicos/quill-render.md' 'Cache de renderiza..o:\*\*[^\n]*sim' $true
    & $need 'src/QuillOptions.cs' 'EnableRenderCache \{ get; set; \} = false;' $true

    # D2: ADR de retentativas existe e e' a unica com o limite
    & $need 'decisoes/adr-004-retentativas-na-fila.md' 'ADR-004' $true
    & $need 'decisoes/adr-004-retentativas-na-fila.md' '\*\*5\*\* tentativas' $true

    # D3: o simbolo aparece em exatamente UM MD
    $sym = $f.symbolOnlyIn.symbol
    $hit = @(Get-ChildItem -LiteralPath $Corpus -Recurse -File -Filter *.md |
        Where-Object { [regex]::IsMatch((Read-DocsText -Path $_.FullName), "\b$sym\b") } |
        ForEach-Object { $_.FullName.Substring($Corpus.Length).TrimStart('\', '/') -replace '\\', '/' })
    if ($hit.Count -ne 1 -or $hit[0] -ne $f.symbolOnlyIn.doc) {
        $fail.Add("simbolo '$sym': esperava exatamente [$($f.symbolOnlyIn.doc)], obtive [$($hit -join ', ')]")
    }

    # tamanho do corpus
    $n = @(Get-ChildItem -LiteralPath $Corpus -Recurse -File -Filter *.md).Count
    if ($n -lt 35) { $fail.Add("corpus pequeno demais: $n MDs (< 35)") }

    if ($fail.Count) { throw ("Test-DocsFacts: corpus divergiu do gabarito:`n - " + ($fail -join "`n - ")) }
    return $true
}
