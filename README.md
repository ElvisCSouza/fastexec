# FastExec

FastExec gives AI assistants a fast, token-frugal way to run commands and work with files, on your computer and on
remote machines. It is an [MCP](https://modelcontextprotocol.io) server that works with Claude Desktop, Claude Code,
Cursor and Antigravity.

It is one self-contained program. No runtime to install, no account, nothing leaves your machine.

- [What it is](#what-it-is)
- [How it works](#how-it-works)
- [Install](#install): [let Claude Desktop do it](#paste-this-prompt-into-claude-desktop), or [run the installer](#install-with-the-installer-about-one-minute)
- [See what it saves you](#see-what-fastexec-saves-you), [the benchmark](#does-it-really-save-tokens-the-benchmark)
- [Tools](#tools), [privacy](#privacy), [uninstall](#uninstall)

## What it is

An AI assistant that works on your computer needs hands: something that runs commands and reads and edits files.
The built-in tools were not designed with the model's context window in mind. They return everything a command prints,
let long commands hang until the client gives up, and make the model discover by trial and error that `cat` on a big
file is a bad idea, that `sleep` wastes time, or that a quoted string needs different escaping in PowerShell than in
cmd.exe. Every one of those mistakes costs tokens (money), time and, in long sessions, quality.

FastExec replaces those hands with a small set of tools that are built around three ideas:

1. **Say less.** The model only sees what it needs: cleaned output, slices instead of whole files, outlines instead of
   walls of code.
2. **Fail less.** Common mistakes are fixed on the spot, or answered with a short hint that teaches the cheaper call, so
   the model does not have to fail first.
3. **Never hang.** Calls always come back, with partial output if needed, and long work moves to background jobs.

## How it works

FastExec is a single static binary written in Go. The techniques below are the whole product.

**A resident local process.** Each AI client starts a tiny `fastexec` process that speaks MCP over stdio and forwards
calls to one long-lived local daemon (listening on `127.0.0.1` only). Calls do not pay for a new shell start-up, and the
code index and caches stay warm between calls and between clients.

**Output shaping.** Before the model reads anything, output is cleaned: ANSI colors and progress bars are stripped,
repeated lines are folded and JSON is compacted. Output above a soft cap (about 10 KB) is cut to its
head and tail, because errors and summaries are usually at the end. The full text is saved to a file, and the cut
message tells the model the exact cheap call to read the part it wants (`read_file` with a pattern or a line range).

**Time boxing.** `exec` returns after 45 seconds with whatever the command has printed so far, instead of being killed
by the client's own 60-second cutoff. Work that takes longer belongs to `job`, which keeps running in the background and
can be waited on, tailed and stopped.

**Auto-remediation and guards.** FastExec recognizes the usual bad habits and answers them directly:

- `cat`, `type`, and scripts that only read files are answered with `read_file`; `ls`/`dir` with a compact native listing;
- a redundant `cd <default folder> &&` is stripped; a fixed `sleep` is refused with a better plan (poll the port, the
  file or the job); a very broad search is capped and the model is told how to narrow it;
- a PowerShell line with escaped quotes goes straight to PowerShell instead of through cmd.exe, which would mangle it.

**Arguments are never silently dropped.** Before a tool runs, its arguments are checked against what the tool accepts:
common alternate spellings (`file_path`, `cmd`, `StartLine`...) are rewritten and reported, and anything unknown is
refused with the list of valid names, so the model never acts on a call that did not do what it asked.

**Code intelligence instead of reading code.** `code_outline` lists the declarations of a file with line numbers, or
finds where a symbol is declared across a folder. `code_graph` answers "who calls this?" and "what does changing this
affect?" from an incremental index (Go through `go/ast`, other languages through line scanners). The first read of a
large file can be answered with its outline; reading the same thing again and again gets a hint instead of the same text.

**Safe, precise editing.** `edit_file` replaces exact text, applies several edits all or nothing, and can replace a
whole function by name using the syntax tree. It keeps line endings and the file's original encoding, and files are
written atomically. `read_file` decodes Windows-1252 and UTF-16 text itself.

**Tools that load on demand.** Only a small core is listed to the model. Specialized modules (file operations, session
notes, SQL, hosts, Docker) are loaded with `tool_manage` when needed, so their schemas are not paid for on every turn.

**Hints that teach.** When a cheaper call exists, the result carries one short line saying which one. Models follow
these, and the next call is better.

**Measured, locally.** Every call is recorded in a local SQLite database (tool, duration, sizes, outcome), and FastExec
also records how much each technique removed. [The savings window](#see-what-fastexec-saves-you) shows it.

## Install

### Paste this prompt into Claude Desktop

On a brand-new Windows PC, the quickest way is to let Claude Desktop do it. Install
[Claude Desktop](https://claude.ai/download) and open it once. Then open a **new chat in the Code tab or any other
session that can run commands on your computer**, paste the prompt below and answer its questions. Claude shows you each
command and asks before it changes anything.

````text
You are helping me install FastExec on this brand-new Windows PC. FastExec is a free, open MCP server from
https://github.com/ElvisCSouza/fastexec. Work step by step. Before each step tell me in one sentence what you are
about to do. Ask me before anything that changes the computer. Never run anything as administrator, never download from
anywhere other than the releases page of that repository, and if anything looks different from what I describe here,
stop and ask me.

STEP 0 - What can you do here?
Check whether you have a tool that can run PowerShell commands on this computer (a terminal, shell or command tool).
If you do not, say so and give me these manual steps instead, then stop:
  1. Open https://github.com/ElvisCSouza/fastexec/releases/latest and download fastexec-setup-windows-amd64.exe and
     SHA256SUMS.
  2. In PowerShell run: (Get-FileHash .\fastexec-setup-windows-amd64.exe -Algorithm SHA256).Hash
     and check that it appears next to that file name in SHA256SUMS.
  3. Double-click the installer, press Install, wait for "FastExec is ready", then quit and reopen Claude Desktop.

STEP 1 - Look at the computer (read only).
Run: $PSVersionTable.PSVersion; [Environment]::OSVersion.Version;
     Test-Path "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe";
     Test-Path "$env:LOCALAPPDATA\Claude-Data"; Test-Path "$env:APPDATA\Claude"
Tell me what you found. If FastExec is already installed, tell me and ask whether to update it.

STEP 2 - Download and verify.
Show me this script and ask for my OK, then run it:

  $ErrorActionPreference = 'Stop'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  $base = 'https://github.com/ElvisCSouza/fastexec/releases/latest/download'
  $dir  = Join-Path $env:LOCALAPPDATA 'Programs\FastExec'
  $tmp  = Join-Path $env:TEMP 'fastexec-download'
  New-Item -ItemType Directory -Force $dir, $tmp | Out-Null
  Invoke-WebRequest "$base/fastexec-windows-amd64.exe" -OutFile "$tmp\fastexec.exe" -UseBasicParsing
  Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$tmp\SHA256SUMS" -UseBasicParsing
  $line = Get-Content "$tmp\SHA256SUMS" | Where-Object { $_ -match 'fastexec-windows-amd64\.exe\s*$' } | Select-Object -First 1
  $want = ($line -split '\s+')[0]
  $got  = (Get-FileHash "$tmp\fastexec.exe" -Algorithm SHA256).Hash
  if ($got -ne $want) { throw "SHA256 mismatch: expected $want, got $got" }
  Copy-Item "$tmp\fastexec.exe" "$dir\fastexec.exe" -Force
  "Verified and copied to $dir\fastexec.exe"

If the download fails or the hash does not match, stop, show me the error and do not try another source. If antivirus
blocks the file, tell me; do not disable it.

STEP 3 - Preview.
Run: & "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" setup --dry-run
Show me the output and explain in plain words which AI clients FastExec will be registered in. If it finds no client,
tell me (Claude Desktop must have been opened at least once) and stop.

STEP 4 - Register.
Ask for my OK, then run: & "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" setup
It keeps my other settings and writes a backup named <file>.fastexec-backup next to each file it changes.

STEP 5 - Check.
Run: & "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" doctor
and: & "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" exec "echo FastExec works"
Explain every WARN or FAIL in plain words and fix what you safely can (ask first).

STEP 6 - Restart Claude Desktop.
Tell me to quit Claude Desktop completely (right-click its icon in the notification area and choose Quit; closing the
window is not enough) and open it again. Then tell me to start a new chat and ask: "Use the fastexec tool to run echo
hello". If it answers, we are done.

STEP 7 - Optional.
Offer, without doing it unless I say yes: (a) the savings icon: run
& "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" tray ; (b) "FastExec-only mode" for Claude Code, which routes every
command and file operation through FastExec: run & "$env:LOCALAPPDATA\Programs\FastExec\fastexec.exe" setup --ui
and I will press Install in the window that opens.

Finish with a short summary of what was installed and where, and how I undo it (run the installer's Undo, or follow the
Uninstall section of the README).
````

### Install with the installer (about one minute)

Get the installer for your system from the [Releases](../../releases) page:

| System | File |
|---|---|
| Windows 10/11 (64-bit) | `fastexec-setup-windows-amd64.exe` |
| Linux x86-64 / ARM64 | `fastexec-setup-linux-amd64` / `fastexec-setup-linux-arm64` |
| macOS Intel / Apple silicon | `fastexec-setup-darwin-amd64` / `fastexec-setup-darwin-arm64` |

Check the download against `SHA256SUMS` from the same release:

```powershell
# Windows
(Get-FileHash .\fastexec-setup-windows-amd64.exe -Algorithm SHA256).Hash
```
```sh
# Linux / macOS
sha256sum fastexec-setup-linux-amd64
```

1. Run the installer. On Windows, double-click it. On Linux and macOS run it from a terminal
   (`chmod +x fastexec-setup-*` first).
2. A small window opens on your computer. It talks only to `127.0.0.1` and closes when you press **Close**.
3. Review the options and press **Install**. The window shows every step, a live log and a list of checks.
4. Wait for **"FastExec is ready"**. Then restart your AI clients so they load it.

You do not need administrator rights. Nothing is changed until you press **Install**.

#### What the installer does

| Step | Detail |
|---|---|
| Copies the program | To `%LOCALAPPDATA%\Programs\FastExec` (Windows) or `~/.local/bin` (Linux, macOS), or a folder you choose |
| Registers FastExec | As an MCP server named `fastexec` in every supported client it finds |
| Keeps your settings | Other servers and options in those files are left as they are. A backup named `<file>.fastexec-backup` is written before each change, and a file that is not valid JSON is never touched |
| Checks the result | Starts FastExec the way a client would and runs real requests (`initialize`, `tools/list`, a test command). "Ready" only appears when every required check is green |

You can run the installer again at any time: it only changes what is different. **Undo** removes FastExec from your
clients and restores the options it changed.

#### FastExec-only mode

With this option on (recommended), the installer adds the assistant's own shell and file tools to Claude Code's
deny list (`~/.claude/settings.json`): `Bash`, `PowerShell`, `Monitor`, `Read`, `Edit`, `Write`, `Glob`, `Grep`,
`NotebookEdit` and the terminal panel of Claude Desktop (`mcp__terminal`). Every command and file operation then goes
through FastExec, which means fewer tokens and fewer quoting, encoding and shell-confusion errors.

This applies to Claude Code and to the Code tab of Claude Desktop. Other assistants keep the tools they have. Turn the
option off, or press **Undo**, to allow the built-in tools again.

## See what FastExec saves you

```
fastexec monitor      # opens the savings window
fastexec tray         # puts an icon in the notification area (Windows); click it to open the window
```

The window updates live and answers two questions: how much you saved (tokens, estimated cost, time, failed calls
avoided) and why. It splits the savings by cause (cleaner output, huge output cut, file slices, fewer retries, rescued
timeouts, a smaller tool list) and shows a "without FastExec" comparison for the same work. Sizes are measured; tokens
are estimated as bytes ÷ 3.5; items based on an assumption are marked **estimate**. The icon's tooltip shows today's
total, and its menu can start it with Windows. The window is served only on `127.0.0.1` and reads the local statistics
database; nothing leaves your computer.

## Does it really save tokens? The benchmark
We measured it instead of guessing. The same model gets the same tasks twice, once with only Claude Code's native tools
(Bash, Read, Glob, Grep, Edit, Write) and once with only FastExec, and we compare what the account is billed for
(input + output tokens, prompt cache off). The method, the task kinds and every raw result are in
[`benchmark/`](benchmark/).
**Latest round (build `core-f5d1719`, 68 tasks, 180 valid pairs):**
| | Native tools | FastExec |
|---|---|---|
| Tokens, geometric mean of the per-pair ratio | 1.00 | **0.725** (-27.5%, 95% CI -32.7% to -21.8%) |
| Same, only pairs where both arms got the answer right | 1.00 | **0.721** (-27.9%) |
| Correct answers | 169 of 199 (85%) | 173 of 199 (87%) |
| Fixed cost per turn (system prompt + tool schemas) | 4,884 tokens | 3,559 tokens |
| Median wall time per task | 6.95 s | 5.10 s |
| Peak memory (whole process tree) | 461 MB | 520 MB |
Where the saving comes from, by kind of task (ratio of tokens, lower is better):
| Task kind | Change |
|---|---|
| Code navigation with the code graph (who calls this?) | -61% |
| Data files (one-line JSON, big logs) | -57% |
| Reading and searching documentation | -39% |
| Terminal work (processes, git, logs) | -35% |
| Multi-file edits | -22% |
| C# | -11% |
| Large-output commands, and a software-engineering subset | -10%, -8% |
It is not a win everywhere. A few tasks cost more with FastExec than without it (for example one C# bug fix by
reading and verifying took about 2.8 times the tokens because the model ran many extra build and test steps), and the
gain is smallest on short tasks, where there is little to cut.
**Read the numbers with these limits in mind.** One model (DeepSeek V4.1 Flash, through Together AI), synthetic and
open-source fixtures, 2 to 5 repetitions per task, cache off. The confidence interval exists because the sample is
small. Other models, other repositories and real sessions will differ; the figure to trust is the one you measure on
your own work with `fastexec monitor`. This round measured build `core-f5d1719`; the release adds one later change
(compact `dotnet build` and `dotnet test` output, including localized messages), which the round did not measure. A smaller C# follow-up on later builds (only the FastExec arm re-run, against the
recorded native mean) is described in
[`benchmark/`](benchmark/README.md#c-follow-up-13-november-2026-build-core-6544137-and-later).## Tools

The assistant sees eight compact tools, plus modules it loads only when needed.

| Tool | What it does |
|---|---|
| `exec` | Run a shell line or a program with arguments (no shell), with a working folder and stdin |
| `read_file` | Read a whole file or a slice; decodes Windows-1252 and UTF-16 text itself |
| `edit_file` | Exact text replacement, one or several edits, all or nothing; keeps line endings and encoding |
| `write_file` | Create a file (refuses to overwrite a non-empty file unless asked) |
| `code_outline` | Declarations of a file, or where a symbol is declared across a folder |
| `code_graph` | Callers, definitions and impact of a change, from an incremental code graph |
| `job` | Background jobs that outlive the call: start, wait, status, tail, stop |
| `tool_manage` | Loads extra modules on demand: file operations, session notes, SQL, hosts, Docker |

## Check your installation

```
fastexec doctor
```

It prints `OK`, `INFO`, `WARN` or `FAIL` for the program, the data folder, each client, and any remote host, with what
to do about each problem.

## Remote machines (optional)

By default FastExec works on the local machine only. To run commands on other machines (a server, a Raspberry Pi, a
VM, a router), you run a small daemon called `server-exec` on each of them and list them in FastExec's config.
A command then costs one round trip on the network (about 2-4 ms on a LAN) plus the time to start the process.

There is nothing to install on the machine you work from beyond FastExec itself.

### 1. Get `server-exec` for the remote machine

Download the file that matches the remote machine from the [latest release](../../releases/latest) and check it
against `SHA256SUMS`:

| File | For |
|---|---|
| `server-exec-linux-amd64` | Linux servers, VMs, WSL |
| `server-exec-linux-arm64` | Raspberry Pi 4/5, ARM64 routers (OpenWrt) |
| `server-exec-linux-armv7` | 32-bit ARM (older routers, TV boxes) |

Each is a single static file of a few hundred KB, with no dependencies. On Linux:

```sh
# on the remote machine
curl -LO https://github.com/ElvisCSouza/fastexec/releases/latest/download/server-exec-linux-amd64
curl -LO https://github.com/ElvisCSouza/fastexec/releases/latest/download/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
sudo install -m 755 server-exec-linux-amd64 /usr/local/bin/server-exec
```

### 2. Create a token

On the machine you work from:

```
fastexec token
```

It prints a random 32-byte token. Save it on the remote machine in a file only the daemon's user can read:

```sh
# on the remote machine
mkdir -p ~/.config/server-exec
echo 'PASTE_THE_TOKEN_HERE' > ~/.config/server-exec/token
chmod 600 ~/.config/server-exec/token
```

### 3. Start the daemon

```sh
server-exec --token-file ~/.config/server-exec/token
```

It listens on TCP port 9142 on all interfaces. Options: `--port N`, `--bind ADDR` (for example a VPN or LAN
address, or `127.0.0.1`), `--token-file PATH`. The token is never accepted on the command line, so it does not show
up in the process list. Commands run as the user who started the daemon, with that user's permissions.

To keep it running across reboots on a systemd machine, create `/etc/systemd/system/server-exec.service`:

```ini
[Unit]
Description=FastExec remote daemon
After=network-online.target

[Service]
User=me
WorkingDirectory=/home/me
ExecStart=/usr/local/bin/server-exec --token-file /home/me/.config/server-exec/token
Restart=always

[Install]
WantedBy=multi-user.target
```

then `sudo systemctl daemon-reload && sudo systemctl enable --now server-exec`.

### 4. Add the host to FastExec

On the machine you work from, run `fastexec config init` (writes a sample to `~/.fastexec/config.json`) and edit it:

```json
{
  "default_host": "local",
  "hosts": {
    "server": { "addr": "192.0.2.10", "token": "PASTE_THE_TOKEN_HERE", "cwd": "/home/me/projects", "aliases": ["srv"] },
    "pi":     { "addr": "192.0.2.20:9142", "token": "PASTE_THE_TOKEN_HERE" },
    "work":   { "ssh": "work-vm", "hidden": true }
  }
}
```

| Key | Meaning |
|---|---|
| `addr` | `ip` or `ip:port` of a `server-exec` daemon (port 9142 if omitted). |
| `token` | The token the daemon requires. Use a different one per machine if you like. |
| `cwd` | Default working directory on that host. |
| `aliases` | Other names for the same host. |
| `ssh` | Alternative to `addr`: an alias from your `~/.ssh/config`. Needs no daemon, but is slower. |
| `hidden` | Keep the host out of the tool descriptions (saves tokens). It still works by name. |
| `default_host` | Host used when a call names none. |

Each host needs exactly one of `addr` or `ssh`. A host can also be reached without a config entry: `ssh:<alias>`,
an IP, or `ip:port`.

### 5. Check the connection

```
fastexec doctor
```

It reports each remote host: reachable or not, and whether the token was accepted. Then ask your assistant to run
something on it ("run `uname -a` on server"). Restart your AI client after editing the config.

| Message | Meaning |
|---|---|
| `... refused the token (wrong token)` | The token in the config differs from the daemon's. |
| `... requires a token: set 'token' for this host` | The daemon has a token, the config does not. |
| connection refused / timed out | The daemon is not running, the port is blocked by a firewall, or the address is wrong. |

### Security

**Anyone who can reach the port can run commands as the daemon's user.** Use at least one of: a token, `--bind` to a
private interface (loopback or VPN), or a firewall rule limited to your own machines. The daemon warns at start when
it runs without a token on a non-loopback address. Traffic is **not encrypted**: across untrusted networks, put it
inside a VPN (WireGuard, Tailscale) or an SSH tunnel.

## Privacy

- FastExec makes no network connections on its own.
- It records usage statistics (tool, duration, size of output, and how much each technique removed) in a local database
  inside its data folder. Nothing is sent anywhere. See `fastexec stats`.
- To turn the statistics off, set `FASTEXEC_TELEMETRY=0` or `"telemetry": false` in the config file.

## Uninstall

Press **Undo** in the installer, or remove the `fastexec` entry from each client's configuration (the
`.fastexec-backup` files keep the previous versions). Then stop the background process with `fastexec daemon stop`
and delete the program folder. To remove all data as well, delete `~/.fastexec`.

## Requirements

Windows 10/11, Linux or macOS, 64-bit (x86-64 or ARM64). On Windows, PowerShell 7 is used when present and Windows
PowerShell 5.1 otherwise. Edge or Chrome is used for the installer window when present; any other browser works too.

## Support and license

Questions and bug reports: open an [issue](../../issues). Please run `fastexec doctor` and include its output.

FastExec is free to use under the terms in [LICENSE.md](LICENSE.md).
