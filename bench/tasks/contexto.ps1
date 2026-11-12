# Tarefas de recuperacao de contexto (estrato 'contexto'): achar o conjunto exato de testes, arquivos ou simbolos
# que a pergunta pede, sem editar nada (so leitura). Cobre os quatro tipos do Agent Retrieval Bench:
#   code2test     C1, C2   dado o codigo, achar os testes que exercitam uma funcao
#   trace2code    C3       dado o trace de um panic, achar a linha de origem
#   edit2ripple   C4, C5   dado o alvo de uma edicao, achar os arquivos que precisam mudar junto
#   comment2context C6     dado um comentario, achar os simbolos do mesmo arquivo que ele cita
# O gabarito sai sempre de programa independente (tools/contexto, go/ast), nunca escrito a mao, e o alvo de cada
# tarefa e escolhido por regra determinista (primeira funcao exportada, em ordem de arquivo/linha, que cai na faixa
# documentada abaixo) sobre fixtures de ref fixa (bench.config.json): o gabarito e estavel entre execucoes.
#
# Alvos congelados (regra -> alvo):
#   C1: 1a funcao exportada do cobra com 3..8 testes que a alcancam            -> AppendActiveHelp (5 testes)
#   C2: 1a funcao exportada do hugo  com 5..15 testes que a alcancam           -> GetBuffer (7 testes)
#   C3: programa minimo que provoca panic dentro do cobra; 1o frame do trace sob a raiz
#   C4: 1a funcao exportada do cobra com 4..8 arquivos nao-teste que a chamam  -> WriteStringAndCheck (7 arquivos)
#   C5: 1a funcao exportada do hugo com 10..30 sitios de chamada nao-teste (>=3 arquivos, fora do pacote do alvo de
#       C2, para nao repetir a mesma vizinhanca)                               -> Delete (6 arquivos, 11 sitios)
#   C6: 1o comentario marcado (TODO/Deprecated) do hugo, em arquivo nao-teste, que cita entre 3 e 8 simbolos
#       exportados do proprio arquivo; sem nenhum marcado na faixa, cai para qualquer comentario
#                                                                              -> hugolib/integrationtest_builder.go:118

$script:CtxOracleDir = Join-Path $script:BenchRoot 'tools\contexto'
$script:CtxGoOk = $null -ne (Get-Command go -ErrorAction SilentlyContinue)

# Roda um subcomando de tools/contexto e devolve as linhas normalizadas (barras '/'), sem vazias.
function global:Invoke-CtxOracle([string[]]$CtxArgs) {
  Push-Location $script:CtxOracleDir
  try { $out = @(& go run . @CtxArgs 2>&1); $rc = $LASTEXITCODE } finally { Pop-Location }
  if ($rc -ne 0) { throw "oraculo 'contexto $($CtxArgs[0])' falhou (rc=$rc): $($out -join ' | ')" }
  @($out | Where-Object { "$_".Trim() } | ForEach-Object { ("$_" -replace '\\', '/').Trim() })
}

function global:Assert-CtxGo([string]$Id) { if (-not $script:CtxGoOk) { throw "${Id}: precisa do 'go' no PATH (oraculo por go/ast)" } }

# Le uma linha de 'contexto picktargets': arquivo:linha Funcao tests=N files=N sites=N
function global:Parse-CtxTarget([string]$Line) {
  $m = [regex]::Match($Line, '^(?<file>[\w./-]+\.go):(?<ln>\d+)\s+(?<fn>[A-Za-z_]\w*)\s+tests=(?<t>\d+)\s+files=(?<f>\d+)\s+sites=(?<s>\d+)$')
  if (-not $m.Success) { throw "linha de alvo inesperada: $Line" }
  @{ file = $m.Groups['file'].Value; line = [int]$m.Groups['ln'].Value; fn = $m.Groups['fn'].Value
     tests = [int]$m.Groups['t'].Value; files = [int]$m.Groups['f'].Value; sites = [int]$m.Groups['s'].Value }
}

# Extrai da resposta as linhas que casam com o padrao (tira marcador de lista, crase e espaco em branco).
function global:Get-CtxItems([string]$Answer, [string]$Pattern) {
  @(($Answer -split "`n") | ForEach-Object { ("$_" -replace '\\', '/').Trim().Trim('`').TrimStart('-', '*', ' ') } |
    ForEach-Object { $_ -replace '\s.*$', '' } | Where-Object { $_.Trim() -and $_ -match $Pattern })
}

# ---- code2test (C1, C2) ----
function New-CtxTestTask($id, $name, $repo, [int]$minT, [int]$maxT) {
  $st = @{ gold = @(); target = '' }
  $repoDir = $Fixtures.$repo; $scoreSet = ${function:Score-Set}; $root = ($repoDir -replace '\\', '/') + '/'
  $TaskTable[$id] = @{ name = $name; stratum = 'contexto'; repos = @($repo)
    Prepare = { param($dir)
      Assert-CtxGo $id
      $linhas = @(Invoke-CtxOracle @('picktargets', $repoDir, "$minT", "$maxT"))
      if (-not $linhas.Count) { throw "${id}: nenhum alvo com ${minT}..${maxT} testes" }
      $t = Parse-CtxTarget $linhas[0]
      $st.target = $t.fn
      $st.gold = @(Invoke-CtxOracle @('testcallers', $repoDir, $t.fn) | ForEach-Object { $_.Replace($root, '') })
      if ($st.gold.Count -lt $minT) { throw "${id}: gabarito pequeno demais ($($st.gold.Count))" }
      "Repositório Go em $repoDir. A função exportada '$($t.fn)' é exercitada por testes, de forma direta ou por um único nível indireto (um teste pode alcançá-la chamando outra função, inclusive de arquivo sem _test.go). Liste os testes que alcançam '$($t.fn)': um por linha no formato arquivo:TestNome, com o caminho do arquivo relativo à raiz do repositório (ex.: caminho/arquivo_test.go:TestAlgo), sem mais nada. Não inclua testes que não alcançam essa função."
    }.GetNewClosure()
    Gold = { $st.gold -join "`n" }.GetNewClosure()
    Score = { param($a) $got = @(Get-CtxItems $a '^[\w./-]+\.go:Test\w+$' | ForEach-Object { $_.Replace($root, '') }); & $scoreSet $got $st.gold }.GetNewClosure() }
}
New-CtxTestTask 'C1' 'Testes de uma função (Cobra, ~5 testes)' 'cobra' 3 8
New-CtxTestTask 'C2' 'Testes de uma função (Hugo, ~7 testes)' 'hugo' 5 15

# ---- trace2code (C3) ----
# Programa minimo: cobra importado por 'replace' do proprio fixture e um comando ausente no registro (receptor nil),
# que faz o cobra dereferenciar nil no fundo da varredura de flags persistentes. Compilado com -gcflags=all=-l para
# o trace nao depender de inlining: o gabarito e lido do trace gerado na hora, entao continua valido se a toolchain mudar.
$script:CtxPanicMain = @'
package main

import (
	"os"

	"github.com/spf13/cobra"
)

var registry = map[string]*cobra.Command{}

func run() {
	root := &cobra.Command{Use: "app"}
	_ = root
	sub := registry["deploy"]
	_, _, err := sub.Find([]string{"deploy", "--force"})
	if err != nil {
		os.Stderr.WriteString("sem panic\n")
	}
}

func main() { run() }
'@

$st3 = @{ gold = @(); trace = '' }; $scoreSet3 = ${function:Score-Set}
$TaskTable['C3'] = @{ name = 'Origem de um panic pelo trace (Cobra)'; stratum = 'contexto'; repos = @('cobra')
  Prepare = { param($dir)
    Assert-CtxGo 'C3'
    $repoDir = $Fixtures.cobra; $repoSlash = $repoDir -replace '\\', '/'
    $probe = Join-Path $dir 'panicprobe'; New-Item -ItemType Directory -Force $probe | Out-Null
    $utf8 = New-Object Text.UTF8Encoding $false
    [IO.File]::WriteAllText((Join-Path $probe 'go.mod'), "module panicprobe`n`ngo 1.21`n`nrequire github.com/spf13/cobra v1.8.1`n`nreplace github.com/spf13/cobra => $repoSlash`n", $utf8)
    [IO.File]::WriteAllText((Join-Path $probe 'main.go'), $script:CtxPanicMain, $utf8)
    $saved = @{}; foreach ($k in 'GOPROXY', 'GOSUMDB', 'GOFLAGS', 'GOTOOLCHAIN') { $saved[$k] = [Environment]::GetEnvironmentVariable($k) }
    [Environment]::SetEnvironmentVariable('GOPROXY', 'off'); [Environment]::SetEnvironmentVariable('GOSUMDB', 'off')
    [Environment]::SetEnvironmentVariable('GOFLAGS', '-mod=mod'); [Environment]::SetEnvironmentVariable('GOTOOLCHAIN', 'local')
    $trace = Join-Path $probe 'trace.txt'
    Push-Location $probe
    try {
      $tidy = & go mod tidy 2>&1
      if ($LASTEXITCODE -ne 0) {   # sem cache de modulos local o 'off' nao resolve pflag/mousetrap: tenta a rede uma vez
        [Environment]::SetEnvironmentVariable('GOPROXY', $null); [Environment]::SetEnvironmentVariable('GOSUMDB', $null)
        $tidy = & go mod tidy 2>&1
        if ($LASTEXITCODE -ne 0) { throw "C3: go mod tidy falhou no probe de panic: $($tidy -join ' | ')" }
      }
      $null = & go build -gcflags=all=-l -o probe.exe . 2>&1
      if ($LASTEXITCODE -ne 0) { throw 'C3: build do probe de panic falhou' }
      $exe = Join-Path $probe 'probe.exe'
      if (-not (Test-Path $exe)) { throw 'C3: probe.exe não foi gerado' }
      & $exe 2> $trace
    } finally {
      Pop-Location
      foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
    $st3.trace = $trace
    $st3.gold = @(Invoke-CtxOracle @('traceorigin', $repoDir, $trace))
    if ($st3.gold.Count -ne 1 -or $st3.gold[0] -notmatch '^[\w.-]+\.go:\d+$') { throw "C3: gabarito inesperado: $($st3.gold -join ',')" }
    "Um programa Go em $probe importa o cobra do repositório $repoDir e provoca um panic dentro dele. A pilha de chamadas do panic foi salva em $trace. Em que ponto do repositório $repoDir o panic nasceu, ou seja, qual é o arquivo e a linha da função mais interna desse repositório que estava em execução nesse instante? Responda uma única linha no formato arquivo:linha, com o caminho do arquivo relativo à raiz do repositório e o número da linha (ex.: algum/arquivo.go:123), sem mais nada."
  }.GetNewClosure()
  Gold = { $st3.gold -join "`n" }.GetNewClosure()
  Score = { param($a) $got = @(Get-CtxItems $a '^[\w./-]+\.go:\d+$' | ForEach-Object { @($_ -split '/')[-1] }); & $scoreSet3 $got $st3.gold }.GetNewClosure() }

# ---- edit2ripple (C4, C5) ----
function New-CtxRippleTask($id, $name, $repo, [int]$minF, [int]$maxF, [int]$minS, [int]$maxS, [string]$skipDir = '') {
  $st = @{ gold = @(); target = '' }
  $repoDir = $Fixtures.$repo; $scoreSet = ${function:Score-Set}; $root = ($repoDir -replace '\\', '/') + '/'
  $TaskTable[$id] = @{ name = $name; stratum = 'contexto'; repos = @($repo)
    Prepare = { param($dir)
      Assert-CtxGo $id
      $t = $null
      foreach ($l in @(Invoke-CtxOracle @('picktargets', $repoDir, '0', '100'))) {
        $c = Parse-CtxTarget $l
        if ($c.files -lt $minF -or $c.files -gt $maxF -or $c.sites -lt $minS -or $c.sites -gt $maxS) { continue }
        if ($skipDir -and $c.file.StartsWith($skipDir)) { continue }
        $t = $c; break
      }
      if (-not $t) { throw "${id}: nenhum alvo com ${minF}..${maxF} arquivos e ${minS}..${maxS} sítios" }
      $st.target = $t.fn
      $st.gold = @(Invoke-CtxOracle @('ripple', $repoDir, $t.fn))
      if ($st.gold.Count -lt 3) { throw "${id}: gabarito pequeno demais ($($st.gold.Count))" }
      "Repositório Go em $repoDir. Vou mudar a assinatura da função exportada '$($t.fn)'. Liste os arquivos .go que contêm pelo menos uma chamada direta a essa função e que não são arquivos de teste: um caminho por linha, relativo à raiz do repositório (ex.: caminho/arquivo.go), sem mais nada. Não inclua arquivos _test.go nem arquivos que apenas definem a função sem chamá-la."
    }.GetNewClosure()
    Gold = { $st.gold -join "`n" }.GetNewClosure()
    Score = { param($a) $got = @(Get-CtxItems $a '^[\w./-]+\.go$' | ForEach-Object { $_.Replace($root, '') }); & $scoreSet $got $st.gold }.GetNewClosure() }
}
New-CtxRippleTask 'C4' 'Impacto de mudar a assinatura (Cobra, ~7 arquivos)' 'cobra' 4 8 0 1000
New-CtxRippleTask 'C5' 'Impacto de mudar a assinatura (Hugo, ~6 arquivos)' 'hugo' 3 1000 10 30 'bufferpool/'

# ---- comment2context (C6) ----
$st6 = @{ gold = @(); rel = ''; line = 0 }; $scoreSet6 = ${function:Score-Set}
$TaskTable['C6'] = @{ name = 'Símbolos citados em um comentário (Hugo)'; stratum = 'contexto'; repos = @('hugo')
  Prepare = { param($dir)
    Assert-CtxGo 'C6'
    $repoDir = $Fixtures.hugo
    $linhas = @(Invoke-CtxOracle @('pickcomment', $repoDir, '3', '8'))
    $m = [regex]::Match($linhas[0], '^(?<rel>[\w./-]+\.go):(?<ln>\d+) n=(?<n>\d+) (?<syms>.+)$')
    if (-not $m.Success) { throw "C6: linha de comentário inesperada: $($linhas[0])" }
    $st6.rel = $m.Groups['rel'].Value; $st6.line = [int]$m.Groups['ln'].Value
    $st6.gold = @(Invoke-CtxOracle @('commentrefs', $repoDir, $st6.rel, "$($st6.line)"))
    if ($st6.gold.Count -lt 3) { throw "C6: gabarito pequeno demais ($($st6.gold.Count))" }
    $abs = Join-Path $repoDir ($st6.rel -replace '/', '\')
    "No repositório Go em $repoDir, o comentário que começa na linha $($st6.line) do arquivo $abs cita alguns símbolos. Liste apenas os símbolos exportados (função, tipo, constante ou variável) declarados nesse mesmo arquivo que são citados nesse comentário: um nome por linha, apenas o identificador, sem repetir. Não inclua símbolos de outros arquivos nem símbolos desse arquivo que o comentário não cita."
  }.GetNewClosure()
  Gold = { $st6.gold -join "`n" }.GetNewClosure()
  Score = { param($a) $got = @(Get-CtxItems $a '^[A-Z][A-Za-z0-9_]*$'); & $scoreSet6 $got $st6.gold }.GetNewClosure() }
