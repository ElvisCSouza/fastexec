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
4. Windows, optional: for scripts and the terminal, also put `fastexec-cli-windows-amd64.exe` in the same folder as
   `fastexec-cli.exe` and call that one. `fastexec.exe` has no console (so nothing flashes when a client starts it), and
   PowerShell neither waits for nor captures the output of such a program; the console twin behaves normally. It
   starts the daemon from the sibling `fastexec.exe`.

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

## Language servers (optional)

The last step of `fastexec setup` is optional and installs language servers. It is skipped with `--no-lsp`, and with
`--lang go,cs,ts` you can name the languages up front (`--lang` accepts short spellings: `go`, `cs`/`c#`, `ts`/`js`,
`py`, `rs`, `zig`). In a terminal it shows one line per server — name, pinned version, approximate size, origin and
licence — and asks for each one. Without a terminal nothing is installed unless you pass `--yes` together with
`--lang`.

Six languages have a reproducible download (see the table in the README). The rest — C/C++ (clangd), Java (jdtls),
Kotlin, and whatever else FastExec can drive — are reported as *manual*: `lsp install` prints the command that
installs them and downloads nothing, because a real install would mean a multi-hundred-MB SDK.

The same step is available later:

```
fastexec lsp install              # asks for the languages detected in your repositories
fastexec lsp install go,csharp    # start from these instead
fastexec lsp install --root ~/work --dry-run
fastexec lsp update               # reinstall the servers whose pinned version changed
fastexec lsp remove gopls
fastexec lsp doctor               # what is installed, what the prerequisites are, licences
fastexec lsp doctor ~/work/app    # ...and whether a C# project there would load
```

- **Detection.** `setup` and `lsp install` scan the repositories FastExec already knows plus any `--root` you give,
  count file extensions, and pre-select those languages. Everything else stays unchecked.
- **Prerequisites are checked first**, and explained in one line: C# needs the .NET SDK; TypeScript/JavaScript and
  Python need Node and npm (pyright comes from npm); Go needs the Go toolchain. If one is missing FastExec prints the
  command that installs it and continues without that language. It never installs an SDK by itself.

  A portable .NET SDK — one unpacked in a folder of its own, not on PATH — is found through `FASTEXEC_DOTNET_SDK`,
  then `DOTNET_ROOT`, then the folder kept in the config, then PATH, then the folders FastExec looks for by default.
  To keep it for every later run, including the daemon's, write it to the config:

  ```
  fastexec config set dotnet_sdk W:\Data\dotnet-sdk\sdk   # "-" clears it
  fastexec config show                                   # also reports whether that folder really holds an SDK
  ```

- **`lsp doctor` checks a C# repository without starting anything.** Run it with no argument for the server listing,
  or with a folder (or from inside a repository) to see whether the C# server would load that project: the same
  checks the launch makes — the SDK, the `global.json` pin, and the restore. Each problem comes as one line with the
  command that fixes it, and a repository that would load prints `C#: project ready`. Nothing is downloaded, no
  server is started, and no `global.json` is ever edited.
- **Pinned and verified.** Each server has an exact version (never "latest"); downloads come from the origins listed
  in the README and the checksum or integrity check offered by that origin is used. Servers live in
  `<data_dir>/lsp/<server>/`, they are not added to PATH, and no administrator rights are needed.
- **Nothing is installed silently.** A failed download is reported with the command it ran and leaves no half-written
  server behind; the temporary staging folder is discarded. The already-installed servers are untouched.
- **Third-party components.** FastExec ships none of them in the binary. They keep their own licences (BSD-3-Clause
  for gopls, MIT for csharp-ls and pyright, MIT/Apache-2.0 for rust-analyzer, MIT for zls and
  typescript-language-server, Apache-2.0 for the TypeScript compiler); `fastexec lsp doctor` prints them.

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

On Windows add `-ldflags="-H=windowsgui"` so no console window flashes when a client starts it, and build the same
package again without that flag as `fastexec-cli.exe` for the terminal (see Install, step 4).
`go run ./tools/release -version vX.Y.Z` builds every release file and `SHA256SUMS` into `dist/`.
