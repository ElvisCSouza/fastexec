# Bench.ps1 - nucleo do benchmark "Claude nativo vs Claude com FastExec".
# Uma funcao (Invoke-Arm) executa UM braco de UMA tarefa e devolve todas as dimensoes:
#   Economia (foco) : in_tokens, out_tokens, tokens_total, first_turn_in (imposto fixo por turno), result_kb (o que as
#                     ferramentas devolveram ao modelo), turns, tool_calls, cost
#   Desempenho      : cpu_s e ram_mb de TODA a arvore de processos (Job Object): claude + proxy MCP + daemon
#   Latencia        : wall_s, api_s, tool_s (= wall - api: tempo fora do modelo)
#   Qualidade       : tool_errors, correct (oraculo objetivo), valid
$script:BenchRoot = Split-Path $PSScriptRoot -Parent

function Get-BenchConfig {
  $c = Get-Content (Join-Path $script:BenchRoot 'bench.config.json') -Raw | ConvertFrom-Json
  if ($env:BENCH_WORK) { $c.work_dir = $env:BENCH_WORK }
  return $c
}
function Get-RunJobPath { Join-Path $script:BenchRoot 'tools\runjob\runjob.exe' }

function Test-TcpOpen([string]$Addr) {
  $h, $p = $Addr.Split(':'); $c = New-Object System.Net.Sockets.TcpClient
  try { $c.Connect($h, [int]$p); return $true } catch { return $false } finally { $c.Close() }
}

function Start-BenchDaemon($Cfg, [string]$Fx, [string]$MetricsFile) {
  $data = Join-Path $Cfg.work_dir 'fx-data'; New-Item -ItemType Directory -Force $data | Out-Null
  $env:FASTEXEC_DAEMON_ADDR = $Cfg.daemon_addr; $env:FASTEXEC_DATA_DIR = $data; $env:FASTEXEC_HOME = (Join-Path $Cfg.work_dir 'fx-home'); Remove-Item Env:FASTEXEC_CONFIG -ErrorAction SilentlyContinue
  # O LSP (Roslyn/csharp-ls, gopls) vive no daemon: sem estes dois o bench nunca exerce o modo exato (ciclo6: 0 respostas exatas em C#).
  if ($Cfg.lsp_path) { $env:FASTEXEC_LSP_PATH = $Cfg.lsp_path }; if ($Cfg.dotnet -and $Cfg.dotnet.root) { $env:FASTEXEC_DOTNET_SDK = $Cfg.dotnet.root }
  try { $p = Start-Process -FilePath (Get-RunJobPath) -ArgumentList @('-m', $MetricsFile, '--', $Fx, 'daemon', 'run') -PassThru -WindowStyle Hidden }
  finally { Remove-Item Env:FASTEXEC_DAEMON_ADDR, Env:FASTEXEC_DATA_DIR, Env:FASTEXEC_HOME, Env:FASTEXEC_LSP_PATH, Env:FASTEXEC_DOTNET_SDK -ErrorAction SilentlyContinue }
  for ($i = 0; $i -lt 60 -and -not (Test-TcpOpen $Cfg.daemon_addr); $i++) { Start-Sleep -Milliseconds 100 }
  if (-not (Test-TcpOpen $Cfg.daemon_addr)) { throw 'daemon de benchmark nao subiu' }
  return $p
}
function Stop-BenchDaemon($Cfg, [string]$Fx, $P) {
  $env:FASTEXEC_DAEMON_ADDR = $Cfg.daemon_addr; $env:FASTEXEC_DATA_DIR = (Join-Path $Cfg.work_dir 'fx-data'); $env:FASTEXEC_HOME = (Join-Path $Cfg.work_dir 'fx-home')
  try { & $Fx daemon stop *> $null } finally { Remove-Item Env:FASTEXEC_DAEMON_ADDR, Env:FASTEXEC_DATA_DIR, Env:FASTEXEC_HOME -ErrorAction SilentlyContinue }
  if ($P) { [void]$P.WaitForExit(8000) }
}

# Envia um tools/call direto ao daemon de benchmark (aquece o indice do code_graph fora da medicao).
function Invoke-BenchTool($Cfg, [string]$Name, [hashtable]$Arguments) {
  $h, $port = $Cfg.daemon_addr.Split(':'); $c = New-Object System.Net.Sockets.TcpClient($h, [int]$port)
  try {
    $s = $c.GetStream(); $c.ReceiveTimeout = 55000
    $req = @{ jsonrpc = '2.0'; id = 1; method = 'tools/call'; params = @{ name = $Name; arguments = $Arguments } } | ConvertTo-Json -Compress -Depth 8
    $b = [Text.Encoding]::UTF8.GetBytes($req + "`n"); $s.Write($b, 0, $b.Length)
    return (New-Object IO.StreamReader($s, [Text.Encoding]::UTF8)).ReadLine()
  } finally { $c.Close() }
}
function Initialize-BenchGraph($Cfg, [string]$Fx, [string[]]$Repos) {
  $p = Start-BenchDaemon $Cfg $Fx (Join-Path $env:TEMP 'bench-warm-daemon.json')
  try { foreach ($r in $Repos) { $t = Get-Date; [void](Invoke-BenchTool $Cfg 'code_graph' @{ action = 'index'; repo_path = $r }); "indexado $r em $([math]::Round(((Get-Date) - $t).TotalSeconds, 1)) s" } }
  finally { Stop-BenchDaemon $Cfg $Fx $p }
}

function Get-ContentBytes($Content) {
  if ($null -eq $Content) { return 0 }
  if ($Content -is [string]) { return [Text.Encoding]::UTF8.GetByteCount($Content) }
  $n = 0; foreach ($x in $Content) { if ($x.text) { $n += [Text.Encoding]::UTF8.GetByteCount([string]$x.text) } }
  return $n
}

# Le o stream-json do claude e extrai o que importa. Tokens por turno: uma mensagem pode aparecer em varios eventos
# (um por bloco de conteudo), por isso deduplica pelo id da mensagem e fica com o ultimo uso.
function Read-ClaudeStream([string[]]$Lines) {
  $turns = [ordered]@{}; $tc = @{}; $errs = 0; $bytes = 0; $final = $null; $calls = 0
  foreach ($l in $Lines) {
    $s = [string]$l; if (-not $s.StartsWith('{')) { continue }
    try { $o = $s | ConvertFrom-Json -Depth 30 } catch { continue }
    if ($o.type -eq 'result') { $final = $o }
    elseif ($o.type -eq 'assistant') {
      $id = [string]$o.message.id
      if ($id -and $o.message.usage) { $turns[$id] = $o.message.usage }
      foreach ($c in @($o.message.content)) { if ($c.type -eq 'tool_use') { $tc[$c.name] = 1 + [int]$tc[$c.name]; $calls++ } }
    }
    elseif ($o.type -eq 'user') {
      foreach ($c in @($o.message.content)) { if ($c.type -eq 'tool_result') { if ($c.is_error) { $errs++ }; $bytes += Get-ContentBytes $c.content } }
    }
  }
  $first = 0
  if ($turns.Count) { $u = @($turns.Values)[0]; $first = [int]$u.input_tokens + [int]$u.cache_creation_input_tokens + [int]$u.cache_read_input_tokens }
  [pscustomobject]@{ final = $final; tool_counts = $tc; tool_errors = $errs; result_bytes = $bytes; tool_calls = $calls; first_turn_in = $first; turn_count = $turns.Count }
}

function Invoke-Arm {
  param($Cfg, [string]$Arm, [string]$Prompt, [string]$Dir, [string]$Fx, [double]$MaxUsd = 1.0, [switch]$Cache, $Provider = $null)
  New-Item -ItemType Directory -Force $Dir | Out-Null
  if ($Cache) { Remove-Item Env:DISABLE_PROMPT_CACHING -ErrorAction SilentlyContinue } else { $env:DISABLE_PROMPT_CACHING = '1' }
  $a = $Cfg.arms.$Arm; $daemon = $null
  if ($a.mcp) {
    $cfgMcp = @{ mcpServers = @{ fastexec = @{ command = $Fx; args = @('mcp'); env = @{ FASTEXEC_DAEMON_ADDR = $Cfg.daemon_addr; FASTEXEC_DATA_DIR = (Join-Path $Cfg.work_dir 'fx-data'); FASTEXEC_HOME = (Join-Path $Cfg.work_dir 'fx-home') } } } }
    $mcp = Join-Path $Dir 'mcp.json'; ($cfgMcp | ConvertTo-Json -Depth 6) | Set-Content $mcp -Encoding UTF8
    $tools = ($a.tools -join ','); $allow = (@('mcp__fastexec') + @($a.tools) | Where-Object { $_ }) -join ','   # hybrid: nativas + FastExec juntas
    $daemon = Start-BenchDaemon $Cfg $Fx (Join-Path $Dir 'daemon-metrics.json')
  } else { $mcp = '{"mcpServers":{}}'; $tools = ($a.tools -join ','); $allow = $tools }
  $cm = Join-Path $Dir 'claude-metrics.json'; Remove-Item $cm -ErrorAction SilentlyContinue
  $model = if ($Provider) { $Provider.model } else { $Cfg.model }
  $provEnv = @('ANTHROPIC_BASE_URL', 'ANTHROPIC_API_KEY', 'ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
  if ($Provider) {    # outro provedor (API compativel com Anthropic): a chave vem de variavel de ambiente, nunca e impressa nem gravada
    $env:ANTHROPIC_BASE_URL = $Provider.base_url
    $env:ANTHROPIC_API_KEY = [Environment]::GetEnvironmentVariable($Provider.api_key_env, 'User'); if (-not $env:ANTHROPIC_API_KEY) { $env:ANTHROPIC_API_KEY = [Environment]::GetEnvironmentVariable($Provider.api_key_env, 'Process') }
    foreach ($v in 'ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL') { Set-Item "Env:$v" $model }
    $env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = '1'
  }
  Push-Location $Dir
  try {
    $raw = & (Get-RunJobPath) -m $cm -- claude -p $Prompt --model $model --output-format stream-json --verbose --no-session-persistence `
      --max-budget-usd $MaxUsd --setting-sources project --strict-mcp-config --mcp-config $mcp --tools $tools --allowedTools $allow 2>&1
  } finally { Pop-Location; foreach ($v in $provEnv) { Remove-Item "Env:$v" -ErrorAction SilentlyContinue }; if ($daemon) { Stop-BenchDaemon $Cfg $Fx $daemon } }
  Set-Content (Join-Path $Dir 'stream.jsonl') ($raw | Out-String) -Encoding UTF8

  $st = Read-ClaudeStream @($raw); $f = $st.final; $u = $f.usage
  $m = if (Test-Path $cm) { Get-Content $cm -Raw | ConvertFrom-Json } else { $null }
  $dmf = Join-Path $Dir 'daemon-metrics.json'; $dm = if (Test-Path $dmf) { Get-Content $dmf -Raw | ConvertFrom-Json } else { $null }
  $inT = [int]$u.input_tokens + [int]$u.cache_creation_input_tokens + [int]$u.cache_read_input_tokens; $outT = [int]$u.output_tokens
  $costOut = [double]$f.total_cost_usd
  if ($Provider) {    # o custo do Claude Code usa a tabela de precos da Anthropic: para outro modelo recalcula pelos precos do provedor
    $pm = $Provider.usd_per_m; $cr = [int]$u.cache_read_input_tokens; $fresh = [math]::Max(0, $inT - $cr)
    $costOut = ($fresh * [double]$pm.input + $cr * [double]$pm.cached_input + $outT * [double]$pm.output) / 1e6
  }
  $wall = if ($m) { [double]$m.wall_s } else { 0 }; $api = [double]$f.duration_api_ms / 1000
  $gCalls = [int]$st.tool_counts['mcp__fastexec__code_graph']; $oCalls = [int]$st.tool_counts['mcp__fastexec__code_outline']; $nGrep = [int]$st.tool_counts['Grep'] + [int]$st.tool_counts['Glob']
  $fxCalls = 0; foreach ($k in $st.tool_counts.Keys) { if ($k -like 'mcp__fastexec__*') { $fxCalls += $st.tool_counts[$k] } }
  [pscustomobject]@{
    arm = $Arm; answer = [string]$f.result; stop = [string]$f.subtype; is_error = [bool]$f.is_error
    in_tokens = $inT; out_tokens = $outT; tokens_total = $inT + $outT; first_turn_in = $st.first_turn_in
    result_kb = [math]::Round($st.result_bytes / 1024, 2); turns = [int]$f.num_turns; tool_calls = $st.tool_calls; fx_calls = $fxCalls; graph_calls = $gCalls; outline_calls = $oCalls; grep_calls = $nGrep
    tool_errors = $st.tool_errors; cost = $costOut; cost_reported = [double]$f.total_cost_usd; cache_read_tokens = [int]$u.cache_read_input_tokens; input_raw = [int]$u.input_tokens
    cache_used = (([int]$u.cache_creation_input_tokens + [int]$u.cache_read_input_tokens) -gt 0)
    wall_s = [math]::Round($wall, 1); api_s = [math]::Round($api, 1); tool_s = [math]::Round([math]::Max(0, $wall - $api), 1)
    cpu_s = [math]::Round([double]$m.cpu_total_s + [double]$dm.cpu_total_s, 2)
    ram_mb = [math]::Round([double]$m.peak_job_mem_mb + [double]$dm.peak_job_mem_mb)
    procs = [int]$m.processes + [int]$dm.processes
    tools = (($st.tool_counts.GetEnumerator() | Sort-Object Name | ForEach-Object { "$($_.Key.Replace('mcp__fastexec__', ''))=$($_.Value)" }) -join ', ')
  }
}

function Score-Set([string[]]$Got, [string[]]$Gab) {
  $g = @($Got | Select-Object -Unique); $tp = @($g | Where-Object { $Gab -contains $_ }).Count
  $p = if ($g.Count) { $tp / $g.Count } else { 0 }; $r = if ($Gab.Count) { $tp / $Gab.Count } else { 0 }
  @{ correct = ($p -eq 1 -and $r -eq 1); precision = [math]::Round($p, 2); recall = [math]::Round($r, 2) }
}
function Score-Bool([bool]$Ok) { @{ correct = $Ok; precision = [int]$Ok; recall = [int]$Ok } }

# Checagens antes de gastar: ferramentas, contaminacao de contexto, fixtures.
function Test-BenchPreflight($Cfg) {
  $p = @()
  if (-not (Get-Command claude -ErrorAction SilentlyContinue)) { $p += 'claude nao esta no PATH' }
  if (-not (Test-Path (Get-RunJobPath))) { $p += 'runjob.exe ausente: rode .\Initialize-Bench.ps1' }
  $d = $Cfg.work_dir
  while ($d) { if (Test-Path (Join-Path $d 'CLAUDE.md')) { $p += "CLAUDE.md em $d entra no contexto e contamina a medicao" }; $up = Split-Path $d -Parent; if (-not $up -or $up -eq $d) { break }; $d = $up }
  if (Test-TcpOpen $Cfg.daemon_addr) { $p += "ja existe algo escutando em $($Cfg.daemon_addr)" }
  return $p
}
