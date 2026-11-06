# Install and update

## Requirements

- Windows 10/11, Linux or macOS, 64-bit (x86-64 or ARM64). macOS builds are produced but less tested.
- On Windows, PowerShell 7 (`pwsh`) is used when installed; Windows PowerShell 5.1 works as a fallback.
- Nothing else: FastExec is a single static binary (pure Go, no cgo).

## Install

1. Download `fastexec-<os>-<arch>` from the Releases page and check it against `SHA256SUMS`.
2. Put it in a permanent folder and rename it to `fastexec` (`fastexec.exe` on Windows). Suggested places:
   - Windows: `%LOCALAPPDATA%\Programs\FastExec\fastexec.exe`
   - Linux and macOS: `~/.local/bin/fastexec` (make it executable: `chmod +x`)
3. Run `fastexec setup` from that folder, restart your AI clients and run `fastexec doctor`.

Do not run it from Downloads or a temp folder: the clients keep the path you register.

## What `fastexec setup` does

It registers the running executable as the MCP server named `fastexec` in every client whose config file exists:

| Client | File |
|---|---|
| Claude Desktop (Windows, Microsoft Store) | `%LOCALAPPDATA%\Claude-Data\claude_<id>\claude_desktop_config.json` |
| Claude Desktop (Windows, older installer) | `%APPDATA%\Claude\claude_desktop_config.json` |
| Claude Desktop (macOS) | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Claude Desktop (Linux) | `~/.config/Claude/claude_desktop_config.json` |
| Claude Code | user scope, through `claude mcp add -s user` (or `~/.claude.json` when the `claude` CLI is not on PATH) |
| Antigravity | `~/.gemini/config/mcp_config.json` or `~/.gemini/antigravity/mcp_config.json` |
| Cursor | `~/.cursor/mcp.json` |

- It only edits files that already exist (an existing file means the client is installed).
- Before changing a file it saves the previous version as `<file>.fastexec-backup`, then writes atomically.
  Other entries and settings are kept, in order. A file that is not valid JSON is left alone and reported.
- Running it again changes nothing unless something is different.
- `--deny-bash` adds `Bash` to `permissions.deny` in `~/.claude/settings.json`. Without it, Claude Code tends to
  use its own Bash tool and seldom calls FastExec.
- It warns about other servers that also run commands or edit files (desktop-commander, filesystem, shell...).
  With them in the same client, models usually pick those and FastExec is rarely called. It does not remove them.

## Manual registration

Any MCP client that starts stdio servers can use FastExec. The entry is just the executable, no arguments:

```json
{
  "mcpServers": {
    "fastexec": { "command": "C:\\Users\\me\\AppData\\Local\\Programs\\FastExec\\fastexec.exe", "args": [] }
  }
}
```

Claude Code: `claude mcp add -s user fastexec -- /path/to/fastexec`.

## Check the installation

`fastexec doctor` reads everything and prints `OK`, `INFO`, `WARN` or `FAIL` with what to do:

- the executable, the config file, the data folder (writable, and not inside a folder the Microsoft Store
  Claude Desktop virtualizes);
- telemetry, by counting the recorded calls;
- the local daemon (is it this build?);
- each client: registered, same build, overlapping servers, Claude Code's Bash;
- each remote host: reachable, protocol version, token, round-trip time.

It exits with 1 when something failed.

## Update

Replace the executable in the same place (on Windows a running `fastexec.exe` cannot be overwritten, but it can be
renamed: rename it, copy the new one, delete the old later), then restart your AI clients. When a client starts
the new build it replaces the local daemon of the old one by itself, so tools run the new code.

## Uninstall

Remove the `fastexec` entry from each client (the `.fastexec-backup` files hold the previous versions), stop the
local daemon with `fastexec daemon stop`, delete the executable and, if you want the data gone, `~/.fastexec`.

## Build from source

Go 1.26 or newer:

```
go build -o fastexec ./cmd/fastexec
```

On Windows add `-ldflags="-H=windowsgui"` so no console window flashes when a client starts it.
`go run ./tools/release -version vX.Y.Z` builds every release file and `SHA256SUMS` into `dist/`.
