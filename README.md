# FastExec

FastExec is an MCP server that gives AI assistants (Claude Desktop, Claude Code, Antigravity, Cursor) a fast,
token-frugal way to run commands and work with files, on your machine and on remote Linux/Windows hosts.

It is one static binary. No runtime, no installer, no account.

## Why

Plain shell tools cost AI assistants a lot of context and time:

- **Output noise burns tokens.** ANSI colors, progress bars, update notices, padded tables and huge dumps land in
  the context window. FastExec cleans every output before the model sees it: progress bars collapse to their final
  state, JSON is compacted, table padding shrinks, repeated lines fold, and anything over 64 KB is cut to the first
  16 KB with a note.
- **Clients kill calls at 60 seconds.** FastExec stops a command at 45 s by default, kills the whole process tree
  (Windows Job Objects, systemd scopes), and returns the partial output. Longer work goes to background jobs that
  survive the call.
- **Models repeat bad habits.** `cat` on a file becomes a `read_file` call, `sleep 10` is refused with a better
  plan, recursive `grep` skips `node_modules` and friends, a giant search is cut to 40 lines with a hint, and
  a search for a function's callers points to the code graph. Each correction tells the model what to do next time.
- **Shells and SSH are slow to start.** Local calls go through a resident daemon; remote calls go to a tiny daemon
  (`server-exec`, written in Zig) over a pooled TCP connection: about 2-4 ms per command on a LAN.

## Tools

The model sees ten compact tools (about 8.5 KB of schema in total):

| Tool | What it does |
|---|---|
| `exec` | Run a shell line or an argv (no shell), with `cwd` and `input` (stdin), on any configured host |
| `read_file` | Read a whole file or a slice (bytes or lines), local or remote |
| `edit_file` | Exact text replacement, one or several edits, all or nothing; keeps line endings |
| `write_file` | Create a file (refuses to overwrite a non-empty file unless asked) |
| `file_ops` | copy, move, delete, mkdir, list without a shell |
| `job` | Background jobs that outlive the call: start, wait, status, tail, stop |
| `code_outline` | Declarations of a file, or where a symbol is declared across a folder |
| `code_graph` | Incremental code graph of a repository: callers, definition, impact of a change |
| `handoff` | Session handoff notes built from what actually happened (no LLM) |
| `tool_manage` | Load extra modules on demand (`sql`, `hosts`, `docker`) without paying their schema up front |

Details: [docs/tools.md](docs/tools.md).

## Quick start

1. Download the binary for your system from the Releases page and put it somewhere permanent
   (for example `%LOCALAPPDATA%\Programs\FastExec\fastexec.exe` on Windows or `~/.local/bin/fastexec` elsewhere).
2. Register it in your AI clients:

   ```
   fastexec setup
   ```

   Add `--deny-bash` to make Claude Code use FastExec instead of its built-in Bash tool, and `--dry-run` to see
   the changes first.
3. Restart the clients, then check everything:

   ```
   fastexec doctor
   ```

With no config file FastExec works on the local machine only. To reach other machines, run `server-exec` on them
and list them in `~/.fastexec/config.json` (`fastexec config init` writes a sample):

```json
{
  "default_host": "server",
  "hosts": {
    "server": { "addr": "192.0.2.10", "cwd": "/home/me/projects" },
    "pi":     { "addr": "192.0.2.20", "token": "..." }
  }
}
```

## Language servers (optional)

FastExec can drive a language server (gopls, csharp-ls, typescript-language-server, pyright, rust-analyzer, zls)
for the languages you actually use. They are **not** bundled in the binary: they are third-party components,
downloaded only with your permission, from these origins:

| Language | Server | Origin | License |
|---|---|---|---|
| Go | gopls | proxy.golang.org | BSD-3-Clause |
| C# | csharp-ls | nuget.org | MIT |
| TypeScript/JavaScript | typescript-language-server | registry.npmjs.org | MIT (typescript: Apache-2.0) |
| Python | pyright-langserver | registry.npmjs.org | MIT |
| Rust | rust-analyzer | github.com/rust-lang/rust-analyzer | MIT or Apache-2.0 |
| Zig | zls | github.com/zigtools/zls | MIT |

Nothing is ever downloaded in the background or on first use. `fastexec setup` offers the step at the end, with
the versions and the languages it detected in your repositories pre-selected; `fastexec lsp install` does the same
later. Every server is pinned to an exact version, installed under `<data_dir>/lsp/<server>/` (never on your PATH,
no admin rights), and `fastexec lsp doctor` lists what is installed and its licence. The prerequisite for each
language (the Go toolchain, the .NET SDK, Node/npm) is checked and explained first — FastExec never installs an SDK
for you. Nothing is selected by default: uncheck everything and no download happens.

See [docs/install.md](docs/install.md#language-servers-optional).

## Documentation

- [Install and update](docs/install.md), including manual registration per client
- [Configuration](docs/config.md): hosts, data folder, environment variables
- [Remote daemon](docs/server.md): building and running `server-exec`, tokens, protocol
- [Tools](docs/tools.md): every tool, the output cleaning, guards and corrections
- [Telemetry](docs/telemetry.md): what is recorded, where, and how to turn it off
- [Savings report](docs/savings.md): the savings panel, its report fields and the Without/With curve
- [Contributing](CONTRIBUTING.md): layout, tests, extending FastExec with your own commands

## Telemetry

FastExec records each tool call (tool, host, duration, sizes, exit code, a 120-character command summary) in a
local SQLite file so `fastexec stats` can show where time and tokens go. Nothing is ever sent anywhere.
Turn it off with `"telemetry": false` in the config or `FASTEXEC_TELEMETRY=0`.

## License

[MIT](LICENSE)
