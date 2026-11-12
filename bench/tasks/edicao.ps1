# Suíte de edição e refatoração multi-arquivo (estrato 'edicao-multi').
# Compara apenas os braços native e fastexec. Tudo é auto-contido: fixture gerado
# em diretório temporário no Prepare, oráculo programático, sem rede e sem LLM.
# Ferramentas de linguagem (go/node/pytest) só entram no oráculo se realmente rodarem
# no host; quando não rodam, o oráculo estrutural decide sozinho e avisa no Prepare.

$script:EdUtf8 = New-Object Text.UTF8Encoding $false

function Test-EdRunnable {
  # 'Get-Command' não basta: em Windows o alias da Microsoft Store existe e falha
  # com exit 9009 ao executar. Só vale o que executa e termina com código 0.
  # O parâmetro NÃO pode se chamar $Args: esse nome é automático no PowerShell,
  # fica vazio e a ferramenta roda sem argumento nenhum (go imprime ajuda, exit 2).
  param([string]$Exe, [string[]]$ToolArgs = @())
  if (-not (Get-Command $Exe -ErrorAction SilentlyContinue)) { return $false }
  try { $null = & $Exe @ToolArgs 2>&1; return ($LASTEXITCODE -eq 0) } catch { return $false }
}

function Convert-EdNewline {
  # Normaliza para LF, garante uma quebra final e converte para CRLF se pedido.
  param([string]$Text, [string]$Newline = "`n")
  $b = $Text -replace "`r`n", "`n"
  if (-not $b.EndsWith("`n")) { $b += "`n" }
  if ($Newline -eq "CRLF" -or $Newline -eq "`r`n") { $b = $b -replace "`n", "`r`n" }
  return $b
}

function Write-EdFile {
  param([string]$Dir, [string]$Rel, [string]$Text, [string]$Newline = "`n")
  $full = Join-Path $Dir ($Rel -replace '/', '\')
  $parent = Split-Path $full -Parent
  if ($parent -and -not (Test-Path $parent)) { New-Item -ItemType Directory -Force $parent | Out-Null }
  [IO.File]::WriteAllText($full, (Convert-EdNewline $Text $Newline), $script:EdUtf8)
  return $full
}

function Get-EdText {
  param([string]$Dir, [string]$Rel)
  $f = Join-Path $Dir ($Rel -replace '/', '\')
  if (Test-Path $f) { return [IO.File]::ReadAllText($f) }
  return ''
}

function Get-EdCount { param([string]$Text, [string]$Pattern) return @([regex]::Matches($Text, $Pattern)).Count }

function Get-EdProjectText {
  param([string]$Dir, [string]$Filter = '*')
  $sb = New-Object Text.StringBuilder
  foreach ($f in @(Get-ChildItem $Dir -Recurse -File -Filter $Filter -ErrorAction SilentlyContinue)) {
    [void]$sb.AppendLine([IO.File]::ReadAllText($f.FullName))
  }
  return $sb.ToString()
}

function Invoke-EdTool {
  # Roda uma ferramenta de linguagem dentro de $Dir sem rede (GOTOOLCHAIN=local).
  param([string]$Dir, [string]$Exe, [string[]]$ToolArgs)
  $had = Test-Path Env:GOTOOLCHAIN
  $old = $env:GOTOOLCHAIN
  $env:GOTOOLCHAIN = 'local'
  Push-Location $Dir
  try {
    $o = & $Exe @ToolArgs 2>&1
    return @{ ok = ($LASTEXITCODE -eq 0); out = ($o | Out-String) }
  } finally {
    Pop-Location
    if ($had) { $env:GOTOOLCHAIN = $old } else { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue }
  }
}

function Test-EdGoProject {
  param([string]$Dir, [string]$Cmd)
  if (-not $script:EdGoOk) { return $true }
  (Invoke-EdTool $Dir 'go' @($Cmd, './...')).ok
}

function Write-EdSkip {
  # Aviso de oráculo degradado. Cada tarefa é preparada duas vezes no dry-run;
  # o conjunto evita repetir a mesma linha.
  param([string]$Msg)
  if (Test-Path Env:BENCH_EDICAO_QUIET) { return }
  if (-not $script:EdSkipVistos) { $script:EdSkipVistos = @{} }
  if ($script:EdSkipVistos.ContainsKey($Msg)) { return }
  $script:EdSkipVistos[$Msg] = $true
  Write-Host "  [edicao-multi] $Msg" -ForegroundColor DarkYellow
}

$script:EdGoOk = Test-EdRunnable 'go' @('version')
$script:EdNodeOk = Test-EdRunnable 'node' @('--version')
$script:EdPytestOk = Test-EdRunnable 'python' @('-m', 'pytest', '--version')

# ---------------------------------------------------------------------------
# M1 - Go: renomear função exportada em todo o projeto
# ---------------------------------------------------------------------------
$script:M1Files = [ordered]@{}
$script:M1Files['go.mod'] = @'
module exemplo.local/leitor

go 1.21
'@
$script:M1Files['tipos.go'] = @'
package leitor

// Linha é o texto bruto aceito por ParseLine antes de virar um Registro.
type Linha string

// Registro é o resultado da interpretação de uma linha "chave=valor".
type Registro struct {
    Chave string
    Valor int
}

// ErroLeitura descreve por que uma linha foi descartada.
type ErroLeitura struct {
    Linha  Linha
    Motivo string
}

func (e ErroLeitura) Error() string {
    return "leitura inválida: " + string(e.Linha) + ": " + e.Motivo
}
'@
$script:M1Files['parse.go'] = @'
package leitor

import "strings"

// ParseLine converte uma linha "chave=valor" em um Registro.
// ok é false quando falta o separador ou a chave é vazia.
// Todo o pacote passa por ParseLine antes de usar qualquer outro tipo.
func ParseLine(linha Linha) (Registro, bool) {
    partes := strings.SplitN(strings.TrimSpace(string(linha)), "=", 2)
    if len(partes) != 2 || partes[0] == "" {
        return Registro{}, false
    }
    n, err := converterInteiro(partes[1])
    if err != nil {
        return Registro{Chave: partes[0], Valor: 0}, true
    }
    return Registro{Chave: partes[0], Valor: n}, true
}
'@
$script:M1Files['conversao.go'] = @'
package leitor

import (
    "errors"
    "strconv"
    "strings"
)

var errVazio = errors.New("valor vazio")

func converterInteiro(txt string) (int, error) {
    txt = strings.TrimSpace(txt)
    if txt == "" {
        return 0, errVazio
    }
    return strconv.Atoi(txt)
}

// ConverterLinha reaproveita ParseLine só para validar o formato.
func ConverterLinha(linha Linha) (int, bool) {
    r, ok := ParseLine(linha)
    if !ok {
        return 0, false
    }
    return r.Valor, true
}
'@
$script:M1Files['arquivo.go'] = @'
package leitor

import (
    "bufio"
    "os"
)

// LeArquivo lê um arquivo texto e separa registros válidos de erros.
func LeArquivo(caminho string) ([]Registro, []ErroLeitura) {
    f, err := os.Open(caminho)
    if err != nil {
        return nil, []ErroLeitura{{Linha: Linha(caminho), Motivo: err.Error()}}
    }
    defer f.Close()
    var out []Registro
    var erros []ErroLeitura
    sc := bufio.NewScanner(f)
    for sc.Scan() {
        bruto := Linha(sc.Text())
        r, ok := ParseLine(bruto)
        if !ok {
            erros = append(erros, ErroLeitura{Linha: bruto, Motivo: "formato inválido"})
            continue
        }
        out = append(out, r)
    }
    return out, erros
}
'@
$script:M1Files['lote.go'] = @'
package leitor

import "path/filepath"

// LeLote lê todos os arquivos .txt de um diretório, em ordem de nome.
func LeLote(dir string) ([]Registro, []ErroLeitura) {
    arquivos, err := filepath.Glob(filepath.Join(dir, "*.txt"))
    if err != nil {
        return nil, nil
    }
    var out []Registro
    var erros []ErroLeitura
    for _, a := range arquivos {
        rs, es := LeArquivo(a)
        out = append(out, rs...)
        erros = append(erros, es...)
    }
    return out, erros
}

// LeLoteMemoria lê linhas já em memória, sem tocar no disco.
func LeLoteMemoria(linhas []Linha) []Registro {
    var out []Registro
    for _, l := range linhas {
        if r, ok := ParseLine(l); ok {
            out = append(out, r)
        }
    }
    return out
}
'@
$script:M1Files['resumo.go'] = @'
package leitor

import "sort"

// Resumir agrupa os registros por chave e soma os valores.
func Resumir(linhas []Linha) map[string]int {
    soma := map[string]int{}
    for _, l := range linhas {
        r, ok := ParseLine(l)
        if !ok {
            continue
        }
        soma[r.Chave] += r.Valor
    }
    return soma
}

// ChavesOrdenadas devolve as chaves do resumo em ordem alfabética.
func ChavesOrdenadas(soma map[string]int) []string {
    ks := make([]string, 0, len(soma))
    for k := range soma {
        ks = append(ks, k)
    }
    sort.Strings(ks)
    return ks
}
'@
$script:M1Files['validacao.go'] = @'
package leitor

import "strings"

// Valida confere separador e chave não vazia, usando ParseLine como
// referência de formato. Serve para checar entrada antes de gravar.
func Valida(linha Linha) bool {
    if !strings.Contains(string(linha), "=") {
        return false
    }
    _, ok := ParseLine(linha)
    return ok
}

// MotivoDescartes resume os erros de leitura em uma linha só.
func MotivoDescartes(erros []ErroLeitura) string {
    if len(erros) == 0 {
        return ""
    }
    var sb strings.Builder
    for i, e := range erros {
        if i > 0 {
            sb.WriteString("; ")
        }
        sb.WriteString(e.Motivo)
    }
    return sb.String()
}
'@
$script:M1Files['parse_test.go'] = @'
package leitor

import "testing"

func TestParseLineSimples(t *testing.T) {
    r, ok := ParseLine("total=7")
    if !ok || r.Chave != "total" || r.Valor != 7 {
        t.Fatalf("ParseLine devolveu %+v ok=%v", r, ok)
    }
}

func TestParseLineInvalida(t *testing.T) {
    if _, ok := ParseLine("sem separador"); ok {
        t.Fatal("ParseLine deveria recusar linha sem separador")
    }
}

func TestResumir(t *testing.T) {
    soma := Resumir([]Linha{"a=1", "b=2", "a=3"})
    if soma["a"] != 4 || soma["b"] != 2 {
        t.Fatalf("resumo inesperado: %v", soma)
    }
}
'@

$TaskTable['M1'] = @{
  name = 'Go: renomear função exportada em 8 arquivos'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m1 = @{ dir = $dir; counts = [ordered]@{} }
    foreach ($rel in $script:M1Files.Keys) {
      Write-EdFile $dir $rel $script:M1Files[$rel] | Out-Null
      $script:m1.counts[$rel] = Get-EdCount $script:M1Files[$rel] '\bParseLine\b'
    }
    $total = 0; foreach ($v in $script:m1.counts.Values) { $total += $v }
    $script:m1.total = $total
    $script:m1.sites = @($script:m1.counts.Keys | Where-Object { $script:m1.counts[$_] -gt 0 })
    if ($total -lt 8) { throw "M1: fixture pobre, só $total ocorrências" }
    if (-not $script:EdGoOk) { Write-EdSkip 'M1: sem go no host, oráculo só textual' }
    "No projeto Go em $dir, renomeie a função exportada ParseLine para ParseRecord em todos os arquivos, incluindo comentários de documentação e testes. Nenhuma ocorrência de ParseLine pode sobrar e o projeto precisa continuar compilando e passando nos testes. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m1.dir
    foreach ($rel in $script:M1Files.Keys) {
      $t = Get-EdText $d $rel
      Write-EdFile $d $rel ($t -creplace '\bParseLine\b', 'ParseRecord') | Out-Null
    }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m1.dir
    $ok = (Get-EdCount (Get-EdProjectText $d '*.go') '\bParseLine\b') -eq 0   # só código: o stream.jsonl da execução (com o prompt) mora no mesmo diretório
    if ($ok) {
      foreach ($rel in $script:m1.sites) {
        if ((Get-EdText $d $rel).Length -eq 0) { $ok = $false; break }
        if ((Get-EdCount (Get-EdText $d $rel) '\bParseRecord\b') -ne $script:m1.counts[$rel]) { $ok = $false; break }
      }
    }
    if ($ok -and $script:EdGoOk) { $ok = (Test-EdGoProject $d 'build') -and (Test-EdGoProject $d 'test') }
    Score-Bool ([bool]$ok)
  }
}

# ---------------------------------------------------------------------------
# M2 - Go: threading de context.Context por vários arquivos
# ---------------------------------------------------------------------------
$script:M2Files = [ordered]@{}
$script:M2Files['go.mod'] = @'
module exemplo.local/rede

go 1.21
'@
$script:M2Files['fetch.go'] = @'
package rede

import (
    "io"
    "os"
)

// Fetch lê o conteúdo de um alvo local e devolve os bytes crus.
func Fetch(alvo string) ([]byte, error) {
    f, err := os.Open(alvo)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    return io.ReadAll(f)
}
'@
$script:M2Files['clientes.go'] = @'
package rede

import (
    "context"
    "fmt"
)

// Cliente busca alvos e guarda o tamanho do último alvo lido.
type Cliente struct {
    Ultimo int
}

// LerTabela busca o alvo padrão e devolve o tamanho.
func (c *Cliente) LerTabela(ctx context.Context) (int, error) {
    if err := ctx.Err(); err != nil {
        return 0, err
    }
    b, err := Fetch("/dados/tabela.txt")
    if err != nil {
        return 0, err
    }
    c.Ultimo = len(b)
    return c.Ultimo, nil
}

// LerIndice busca o índice e formata o resultado.
func (c *Cliente) LerIndice(ctx context.Context) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    b, err := Fetch("/dados/indice.txt")
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("indice com %d bytes", len(b)), nil
}
'@
$script:M2Files['lote.go'] = @'
package rede

import "context"

// Alvos é a lista fixa de alvos do lote diário.
var Alvos = []string{"/dados/dia.txt", "/dados/hora.txt"}

// ExecutarLote busca cada alvo do lote em sequência.
func ExecutarLote(ctx context.Context) (int, error) {
    total := 0
    if err := ctx.Err(); err != nil {
        return total, err
    }
    primeiro, err := Fetch("/dados/dia.txt")
    if err != nil {
        return 0, err
    }
    total += len(primeiro)
    if err := ctx.Err(); err != nil {
        return total, err
    }
    segundo, err := Fetch("/dados/hora.txt")
    if err != nil {
        return 0, err
    }
    total += len(segundo)
    return total, nil
}
'@
$script:M2Files['cache.go'] = @'
package rede

import "context"

// Aquecer preenche o cache com o conteúdo do alvo de aquecimento.
func Aquecer(ctx context.Context) ([]byte, error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    b, err := Fetch("/dados/aquecimento.txt")
    if err != nil {
        return nil, err
    }
    return b, nil
}
'@
$script:M2Files['sincroniza.go'] = @'
package rede

import "context"

// Sincronizar busca o espelho e devolve quantos bytes vieram.
func Sincronizar(ctx context.Context) (int, error) {
    if err := ctx.Err(); err != nil {
        return 0, err
    }
    b, err := Fetch("/dados/espelho.txt")
    if err != nil {
        return 0, err
    }
    return len(b), nil
}
'@
$script:M2Files['fetch_test.go'] = @'
package rede

import (
    "context"
    "testing"
)

func TestExecutarLoteSemArquivos(t *testing.T) {
    if _, err := ExecutarLote(context.Background()); err == nil {
        t.Fatal("esperava erro ao buscar alvos inexistentes")
    }
}

func TestAquecerSemArquivo(t *testing.T) {
    if _, err := Aquecer(context.Background()); err == nil {
        t.Fatal("esperava erro ao aquecer alvo inexistente")
    }
}

func TestSincronizarSemArquivo(t *testing.T) {
    if _, err := Sincronizar(context.Background()); err == nil {
        t.Fatal("esperava erro ao sincronizar alvo inexistente")
    }
}

func TestClienteLerTabela(t *testing.T) {
    c := Cliente{}
    if _, err := c.LerTabela(context.Background()); err == nil {
        t.Fatal("esperava erro ao ler tabela inexistente")
    }
    if _, err := c.LerIndice(context.Background()); err == nil {
        t.Fatal("esperava erro ao ler índice inexistente")
    }
}
'@

$TaskTable['M2'] = @{
  name = 'Go: adicionar context.Context a Fetch e 6 chamadas'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m2 = @{ dir = $dir; sites = @('clientes.go', 'lote.go', 'cache.go', 'sincroniza.go'); counts = [ordered]@{} }
    foreach ($rel in $script:M2Files.Keys) {
      Write-EdFile $dir $rel $script:M2Files[$rel] | Out-Null
    }
    $total = 0
    foreach ($rel in $script:m2.sites) {
      $n = Get-EdCount (Get-EdText $dir $rel) '\bFetch\('
      $script:m2.counts[$rel] = $n
      $total += $n
    }
    if ($total -ne 6) { throw "M2: esperava 6 sítios de chamada, achei $total" }
    if (-not $script:EdGoOk) { Write-EdSkip 'M2: sem go no host, oráculo só textual' }
    "No projeto Go em $dir, adicione um primeiro parâmetro ctx context.Context à função Fetch declarada em $dir\fetch.go e atualize os 6 pontos de chamada (espalhados por 4 arquivos) para repassar o contexto. O projeto precisa continuar compilando e passando nos testes. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m2.dir
    $t = Get-EdText $d 'fetch.go'
    $t = $t -replace 'import \(', "import (`n    `"context`""
    $t = $t -replace 'func Fetch\(alvo string\)', 'func Fetch(ctx context.Context, alvo string)'
    Write-EdFile $d 'fetch.go' $t | Out-Null
    foreach ($rel in $script:m2.sites) {
      $x = Get-EdText $d $rel
      Write-EdFile $d $rel ($x -creplace 'Fetch\("', 'Fetch(ctx, "') | Out-Null
    }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m2.dir
    $ok = (Get-EdCount (Get-EdText $d 'fetch.go') 'func Fetch\(\s*ctx\s+context\.Context\s*,\s*alvo\s+string\s*\)') -eq 1
    if ($ok) {
      foreach ($rel in $script:m2.sites) {
        if ((Get-EdCount (Get-EdText $d $rel) '\bFetch\(') -ne $script:m2.counts[$rel]) { $ok = $false; break }
      }
    }
    if ($ok) { $ok = (Get-EdCount (Get-EdProjectText $d) '\bFetch\(\s*"') -eq 0 }
    if ($ok -and $script:EdGoOk) { $ok = (Test-EdGoProject $d 'vet') -and (Test-EdGoProject $d 'test') }
    Score-Bool ([bool]$ok)
  }
}

# ---------------------------------------------------------------------------
# M3 - Python: parâmetro keyword-only e atualização dos usos marcados
# ---------------------------------------------------------------------------
$script:M3Files = [ordered]@{}
$script:M3Files['pacote/__init__.py'] = @'
"""Pacote de cálculos do relatório."""

from .calculo import calcular

__all__ = ["calcular"]
'@
$script:M3Files['pacote/calculo.py'] = @'
"""Regras de cálculo do relatório."""


def calcular(a, b):
    """Soma a e b. Por padrão devolve o valor já arredondado."""
    bruto = a + b
    return round(bruto, 2)
'@
$script:M3Files['pacote/relatorio.py'] = @'
"""Montagem do relatório de totais."""

from .calculo import calcular


def total_bruto(valores):
    total = 0
    for v in valores:
        total = calcular(total, v)  # sem arredondar
    return total


def total_com_taxa(valores, taxa):
    total = 0
    for v in valores:
        total = calcular(total, v * taxa)  # sem arredondar
    return total
'@
$script:M3Files['pacote/planilha.py'] = @'
"""Exportação de totais para planilha."""

from .calculo import calcular


def linha_de_total(a, b):
    total = calcular(a, b)  # sem arredondar
    return "total;{}".format(total)


def cabecalho():
    return "campo;valor"
'@
$script:M3Files['pacote/cli.py'] = @'
"""Interface de linha de comando."""

from .calculo import calcular
from .relatorio import total_bruto


def exibir(a, b):
    print("soma:", calcular(a, b))
    print("bruto:", total_bruto([a, b]))


def exibir_bruto(a, b):
    total = calcular(a, b)  # sem arredondar
    return total
'@
$script:M3Files['tests/test_calculo.py'] = @'
"""Testes do pacote de cálculos."""

from pacote.calculo import calcular
from pacote.planilha import linha_de_total
from pacote.relatorio import total_bruto


def test_soma_simples():
    assert calcular(2, 3) == 5


def test_bruto_maior_ou_igual():
    assert total_bruto([2, 3]) >= 5


def test_linha_de_total():
    assert linha_de_total(2, 3).startswith("total;")
'@

$TaskTable['M3'] = @{
  name = 'Python: parâmetro keyword-only e usos marcados'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m3 = @{ dir = $dir; arquivos = @($script:M3Files.Keys) }
    foreach ($rel in $script:M3Files.Keys) { Write-EdFile $dir $rel $script:M3Files[$rel] | Out-Null }
    $marcas = @(Get-EdText $dir 'pacote/relatorio.py') + @(Get-EdText $dir 'pacote/planilha.py') + @(Get-EdText $dir 'pacote/cli.py')
    $n = 0
    foreach ($txt in $marcas) { $n += Get-EdCount $txt '# sem arredondar' }
    if ($n -lt 4) { throw "M3: esperava 4 sítios marcados, achei $n" }
    $script:m3.marcas = $n
    if (-not $script:EdPytestOk) { Write-EdSkip 'M3: sem python/pytest no host, oráculo só estrutural' }
    "No pacote Python em $dir\pacote, mude a assinatura de calcular (definida em $dir\pacote\calculo.py) para calcular(a, b, *, arredondar=True). Nas chamadas, toda linha que termina com o comentário '# sem arredondar' deve passar arredondar=False. Não altere nem remova nenhum comentário. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m3.dir
    $t = Get-EdText $d 'pacote/calculo.py'
    Write-EdFile $d 'pacote/calculo.py' ($t.Replace('def calcular(a, b):', 'def calcular(a, b, *, arredondar=True):')) | Out-Null
    foreach ($rel in @('pacote/relatorio.py', 'pacote/planilha.py', 'pacote/cli.py')) {
      $x = Get-EdText $d $rel
      # [^)]* (e não .*?) para não atravessar o parêntese da chamada externa
      $x = $x -replace 'calcular\(([^)]*)\)(\s*# sem arredondar)', 'calcular($1, arredondar=False)$2'
      Write-EdFile $d $rel $x | Out-Null
    }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m3.dir
    $ok = (Get-EdCount (Get-EdText $d 'pacote/calculo.py') 'def calcular\(\s*a\s*,\s*b\s*,\s*\*\s*,\s*arredondar\s*=\s*True\s*\)') -eq 1
    $marcas = 0
    if ($ok) {
      foreach ($rel in @('pacote/relatorio.py', 'pacote/planilha.py', 'pacote/cli.py')) {
        $linhas = @(Get-EdText $d $rel) -split "`n"
        $marcas += @($linhas | Where-Object { $_ -match '# sem arredondar' }).Count
        foreach ($l in $linhas) {
          if ($l -notmatch '# sem arredondar') { continue }
          # a keyword tem de estar DENTRO da chamada marcada, não em qualquer lugar da linha
          if ($l -notmatch 'calcular\([^)]*arredondar\s*=\s*False\s*\)') { $ok = $false }
        }
      }
      if ($marcas -ne $script:m3.marcas) { $ok = $false }
    }
    if ($ok -and $script:EdPytestOk) { $ok = (Invoke-EdTool $d 'python' @('-m', 'pytest', '-q')).ok }
    Score-Bool ([bool]$ok)
  }
}

# ---------------------------------------------------------------------------
# M4 - JavaScript: var -> const/let e require -> import
# ---------------------------------------------------------------------------
$script:M4Files = [ordered]@{}
$script:M4Files['util.js'] = @'
const LIMITE = 40;

function normalizar(v) {
  var t = String(v).trim();
  if (t.length > LIMITE) {
    t = t.slice(0, LIMITE);
  }
  return t;
}

function somarLista(itens) {
  var total = 0;
  for (var i = 0; i < itens.length; i++) {
    total += itens[i];
  }
  return total;
}

module.exports = { normalizar, somarLista, LIMITE };
'@
$script:M4Files['formato.js'] = @'
var util = require('./util.js');

function formatarPar(chave, valor) {
  var k = util.normalizar(chave);
  var v = util.normalizar(valor);
  return k + '=' + v;
}

function formatarLista(pares) {
  var linhas = [];
  for (var i = 0; i < pares.length; i++) {
    linhas.push(formatarPar(pares[i][0], pares[i][1]));
  }
  return linhas.join('\n');
}

module.exports = { formatarPar, formatarLista };
'@
$script:M4Files['calc.js'] = @'
var util = require('./util.js');

function media(itens) {
  if (itens.length === 0) {
    return 0;
  }
  return util.somarLista(itens) / itens.length;
}

function maior(itens) {
  var m = itens[0];
  for (var i = 1; i < itens.length; i++) {
    if (itens[i] > m) {
      m = itens[i];
    }
  }
  return m;
}

module.exports = { media, maior };
'@
$script:M4Files['relatorio.js'] = @'
var calc = require('./calc.js');
var fmt = require('./formato.js');

function resumo(itens) {
  var partes = [];
  partes.push(fmt.formatarPar('media', calc.media(itens)));
  partes.push(fmt.formatarPar('maior', calc.maior(itens)));
  return partes.join('; ');
}

module.exports = { resumo };
'@
$script:M4Files['principal.js'] = @'
var relatorio = require('./relatorio.js');

function executar(itens) {
  console.log(relatorio.resumo(itens));
  return 0;
}

executar([3, 9, 4]);
'@

$TaskTable['M4'] = @{
  name = 'JS: var para const/let e require para import'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m4 = @{ dir = $dir; arquivos = @($script:M4Files.Keys); comRequire = @(); vars = 0 }
    foreach ($rel in $script:M4Files.Keys) {
      Write-EdFile $dir $rel $script:M4Files[$rel] | Out-Null
      $script:m4.vars += Get-EdCount $script:M4Files[$rel] '\bvar\s'
      if ((Get-EdCount $script:M4Files[$rel] 'require\(') -gt 0) { $script:m4.comRequire += $rel }
    }
    if ($script:m4.vars -lt 10 -or $script:m4.comRequire.Count -lt 4) { throw 'M4: fixture pobre' }
    if (-not $script:EdNodeOk) { Write-EdSkip 'M4: sem node no host, oráculo só textual' }
    "Nos 5 arquivos .js dentro de $dir, troque toda declaração var por const ou let, escolhendo a forma segura em cada caso, e converta todas as chamadas require('...') para a forma import ... from '...'. Os arquivos precisam continuar sintaticamente válidos para o node. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m4.dir
    foreach ($rel in $script:M4Files.Keys) {
      $t = Get-EdText $d $rel
      $t = $t -replace "var (\w+) = require\('\./([\w.]+)'\);", "import $1 from './$2';"
      # 'let' em tudo é sempre seguro; o oráculo exige apenas que 'var' desapareça.
      $t = $t -replace '\bvar\s+', 'let '
      Write-EdFile $d $rel $t | Out-Null
    }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m4.dir
    $ok = $true
    foreach ($rel in $script:m4.arquivos) {
      $t = Get-EdText $d $rel
      if ($t.Length -eq 0) { $ok = $false; break }
      if ((Get-EdCount $t 'require\(') -ne 0) { $ok = $false; break }
      if ((Get-EdCount $t '\bvar\s') -ne 0) { $ok = $false; break }
    }
    if ($ok) {
      foreach ($rel in $script:m4.comRequire) {
        if ((Get-EdCount (Get-EdText $d $rel) '\bimport\b') -lt 1) { $ok = $false; break }
      }
    }
    if ($ok -and $script:EdNodeOk) {
      foreach ($rel in $script:m4.arquivos) {
        if (-not (Invoke-EdTool $d 'node' @('--check', $rel)).ok) { $ok = $false; break }
      }
    }
    Score-Bool ([bool]$ok)
  }
}

# ---------------------------------------------------------------------------
# M5 - três constantes em arquivos diferentes, preservando bytes e CRLF
# ---------------------------------------------------------------------------
$script:M5Arquivos = [ordered]@{}
$script:M5Arquivos['Config.cs'] = @'
namespace Demo.Configuracao
{
    public static class Config
    {
        // Quantas vezes repetir a operacao antes de desistir.
        public const int MaxRetries = 3;
        public const int JanelaMs = 500;
        public const string Ambiente = "homologacao";
    }
}
'@
$script:M5Arquivos['Rede.cs'] = @'
namespace Demo.Configuracao
{
    public static class Rede
    {
        // Tempo maximo de espera de uma resposta, em milissegundos.
        public const int TimeoutMs = 2000;
        public const int Porta = 8080;
        public const bool UsaTls = false;
    }
}
'@
$script:M5Arquivos['Pool.cs'] = @'
namespace Demo.Configuracao
{
    public static class Pool
    {
        // Quantas conexoes ficam abertas ao mesmo tempo.
        public const int PoolSize = 8;
        public const int OciosasMs = 30000;
    }
}
'@
$script:M5Arquivos['Limites.cs'] = @'
namespace Demo.Configuracao
{
    public static class Limites
    {
        public const int MaxItens = 500;
        public const int MaxBytes = 1048576;
        public const int MaxLotes = 12;
    }
}
'@
$script:M5Arquivos['Parametros.cs'] = @'
namespace Demo.Configuracao
{
    public static class Parametros
    {
        // Rotulo usado nos logs de auditoria.
        public const string Rotulo = "coletor";
        public const int Nivel = 2;
    }
}
'@
$script:M5Arquivos['Ajustes.cs'] = @'
namespace Demo.Configuracao
{
    public static class Ajustes
    {
        public const int Lote = 250;
        public const int PausaMs = 150;
        public const int Fuso = -3;
    }
}
'@
$script:M5Arquivos['Servico.cs'] = @'
namespace Demo.Configuracao
{
    public static class Servico
    {
        // Nome curto exibido no painel.
        public const string Nome = "coletor";
        public const int Versao = 4;
    }
}
'@
$script:M5Arquivos['Cliente.cs'] = @'
namespace Demo.Configuracao
{
    public static class Cliente
    {
        public const string Apelido = "padrao";
        public const int Prioridade = 1;
    }
}
'@
$script:M5Arquivos['Tarefas.cs'] = @'
namespace Demo.Configuracao
{
    public static class Tarefas
    {
        public const int Diarias = 6;
        public const int Semanais = 2;
    }
}
'@
$script:M5Arquivos['Fila.cs'] = @'
namespace Demo.Configuracao
{
    public static class Fila
    {
        public const int Profundidade = 64;
        public const int EsperaMs = 100;
    }
}
'@
$script:M5Arquivos['cache.txt'] = @'
# Cache do coletor. Os valores abaixo sao copiados para Config.cs.
chave=coletor
janela_ms=500
ambiente=homologacao
'@
$script:M5Arquivos['notas.txt'] = @'
Notas de operacao do coletor.

- Rodar o coletor sempre depois da meia-noite.
- Conferir o painel antes de mudar o ambiente.
- O espelho e atualizado uma vez por dia.
'@

$script:M5Troca = [ordered]@{
  'Config.cs'  = @{ de = 'MaxRetries = 3;';  para = 'MaxRetries = 5;' }
  'Rede.cs'    = @{ de = 'TimeoutMs = 2000;'; para = 'TimeoutMs = 4500;' }
  'Pool.cs'    = @{ de = 'PoolSize = 8;';    para = 'PoolSize = 16;' }
}

$TaskTable['M5'] = @{
  name = 'CRLF: três constantes em 3 de 12 arquivos'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m5 = @{ dir = $dir; esperado = [ordered]@{}; arquivos = @($script:M5Arquivos.Keys) }
    foreach ($rel in $script:M5Arquivos.Keys) {
      $orig = $script:M5Arquivos[$rel]
      Write-EdFile $dir $rel $orig 'CRLF' | Out-Null
      $exp = $orig
      if ($script:M5Troca.Contains($rel)) {
        $t = $script:M5Troca[$rel]
        if ((Get-EdCount $exp ([regex]::Escape($t.de))) -ne 1) { throw "M5: '$($t.de)' não é único em $rel" }
        $exp = $exp.Replace($t.de, $t.para)
      }
      $script:m5.esperado[$rel] = Convert-EdNewline $exp 'CRLF'
    }
    if ($script:M5Arquivos.Count -ne 12) { throw "M5: esperava 12 arquivos" }
    "No diretório $dir existem 12 arquivos. Troque apenas o valor de três constantes, cada uma em seu próprio arquivo: em Config.cs, MaxRetries = 3; passa a MaxRetries = 5; em Rede.cs, TimeoutMs = 2000; passa a TimeoutMs = 4500; em Pool.cs, PoolSize = 8; passa a PoolSize = 16;. Todo o resto dos 12 arquivos, inclusive a quebra de linha CRLF, tem de ficar byte a byte igual. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m5.dir
    foreach ($rel in $script:m5.arquivos) {
      [IO.File]::WriteAllText((Join-Path $d $rel), $script:m5.esperado[$rel], $script:EdUtf8)
    }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m5.dir
    $ok = $true
    foreach ($rel in $script:m5.arquivos) {
      $f = Join-Path $d $rel
      if (-not (Test-Path $f)) { $ok = $false; break }
      $got = [Convert]::ToBase64String([IO.File]::ReadAllBytes($f))
      $exp = [Convert]::ToBase64String($script:EdUtf8.GetBytes($script:m5.esperado[$rel]))
      if ($got -cne $exp) { $ok = $false; break }
    }
    Score-Bool ([bool]$ok)
  }
}

# ---------------------------------------------------------------------------
# M6 - mesma chave em três formatos de configuração
# ---------------------------------------------------------------------------
$script:M6Files = [ordered]@{}
$script:M6Files['config.json'] = @'
{
  "API_TIMEOUT": 30,
  "API_RETRIES": 4,
  "servico": "relatorios",
  "destino": "saida/local"
}
'@
$script:M6Files['config.yaml'] = @'
# Configuracao do coletor de relatorios
servico: relatorios
API_TIMEOUT: 30   # segundos
API_RETRIES: 4
destino: saida/local
'@
$script:M6Files['.env'] = @'
# Tempo maximo de espera da API, em segundos
API_TIMEOUT=30
# Numero de tentativas antes de desistir
API_RETRIES=4
'@

$TaskTable['M6'] = @{
  name = 'Config: API_TIMEOUT de 30 para 60 em json, yaml e .env'
  stratum = 'edicao-multi'
  Prepare = {
    param($dir)
    $script:m6 = @{ dir = $dir; esperado = [ordered]@{}; arquivos = @($script:M6Files.Keys) }
    foreach ($rel in $script:M6Files.Keys) {
      $orig = $script:M6Files[$rel]
      Write-EdFile $dir $rel $orig | Out-Null
      if ((Get-EdCount $orig 'API_TIMEOUT\D*30') -ne 1) { throw "M6: API_TIMEOUT=30 não é único em $rel" }
      $script:m6.esperado[$rel] = Convert-EdNewline ($orig -replace '(API_TIMEOUT"?\s*[:=]\s*)30', '${1}60')
    }
    "Em $dir existem três arquivos de configuração (config.json, config.yaml e .env) e todos têm a chave API_TIMEOUT com o valor 30. Troque esse valor para 60 nos três, preservando a formatação e os comentários de cada arquivo. Nada mais pode mudar. Responda apenas 'feito'."
  }
  Gold = {
    $d = $script:m6.dir
    foreach ($rel in $script:m6.arquivos) { Write-EdFile $d $rel $script:m6.esperado[$rel] | Out-Null }
    'feito'
  }
  Score = {
    param($a)
    $d = $script:m6.dir
    $ok = $true
    foreach ($rel in $script:m6.arquivos) {
      if ((Get-EdText $d $rel) -cne $script:m6.esperado[$rel]) { $ok = $false; break }
    }
    if ($ok) {
      try {
        $j = (Get-EdText $d 'config.json') | ConvertFrom-Json
        if ([int]$j.API_TIMEOUT -ne 60) { $ok = $false }
      } catch { $ok = $false }
    }
    Score-Bool ([bool]$ok)
  }
}
