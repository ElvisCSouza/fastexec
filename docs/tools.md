# Tools

Every tool takes an optional `host`: `local`, a host from the config, `ssh:<alias>` or an IP. File tools default to
`local` (a Linux path such as `/var/log/x` goes to the default host); `exec` and `job` default to the config's
`default_host`. A path can also carry its host: `server:/etc/hosts`.

## exec

Runs a command and returns as soon as it exits.

- `command`: a shell line (`cmd.exe` on Windows `local`, `bash -c` on Linux, Windows PowerShell on a Windows
  daemon). `argv`: a program and its arguments with no shell at all, so nothing needs quoting.
- `cwd`: working directory; relative paths resolve from the host's configured `cwd`.
- `input`: sent to stdin. Long scripts go here: `argv: ["python", "-"]` or
  `argv: ["pwsh", "-NoProfile", "-Command", "-"]` with the script in `input`, no temp files.
- `timeout_ms`: default 45 000, at most 50 000, to stay under the 60 s limit of MCP clients. On timeout the partial
  output is returned and the whole process tree is killed; on Windows `local` (from an MCP client) the command is
  not killed but moved to a background job, with the id to `wait` on, so a long build or deploy is never cut in
  the middle. Longer work belongs in `job` from the start.
- `powershell` is promoted to PowerShell 7 (`pwsh`) when it is installed.

Every command runs non-interactively: `CI=1`, `TERM=dumb`, `NO_COLOR=1`, `PAGER=cat`, `GIT_PAGER=cat`,
`GIT_TERMINAL_PROMPT=0`, `DEBIAN_FRONTEND=noninteractive`, and similar. Nothing waits for a pager or a `[y/n]`.

### Output cleaning

Applied to stdout and stderr before the model sees them:

1. Carriage-return overwrites resolve to their final state (progress bars of curl, docker, pip, npm).
2. ANSI escape sequences go away.
3. Valid JSON is compacted.
4. In tabular output (`docker ps`, `df`, `netstat`) runs of 3+ spaces become 2; code, YAML and diffs keep their
   indentation.
5. Repeated lines fold into `<line> ×N`; known noise (package-manager update notices, git hints) is dropped.
6. A line longer than 500 characters keeps its start and end, or 150 characters around the searched term.
7. Output over 64 KB keeps the first 16 KB and says so. The capture itself is bounded (first 3 MiB, last 1 MiB).

A search that finds nothing (exit 1, no output) answers `no result (exit 1)` instead of looking like a failure.
A non-zero exit is reported with its code and the first meaningful error line.

### Guards and corrections

Each one tells the model what it did and what to do next time.

| Pattern | What happens |
|---|---|
| `cat file`, `type file`, `Get-Content file` | Served by `read_file` (no process) |
| `ls`, `dir` of a folder | Served by `file_ops list` |
| `sleep N` / `timeout N` | Up to 15 s: done with a native timer. Longer: refused, with a pointer to `job` |
| `cd <host default cwd> && ...` | The redundant `cd` is removed |
| `grep`, `rg`, `findstr`, `Select-String` (also inside `pwsh -Command`) | Treated as searches by the rows below |
| recursive `grep` | Gets `--exclude-dir` for `.git`, `node_modules`, `dist`, `build`, `vendor`, caches... |
| search output over 40 lines | Cut to 40 with a hint to narrow it or use `code_outline` |
| search for a declaration (`void Foo`, `struct Row`, `func X`) | Hint to use `code_outline` |
| search for a function's call sites | Hint to use `code_graph` |
| recursive scan of `C:\`, `C:\Users`, `/`, `/home` | Refused (it would take minutes and flood the context) |
| PowerShell cmdlet in `cmd.exe` | Hint to use `pwsh` |
| child processes left running after the command | Killed, with a warning to use `job` |
| edit by script (`Get-Content ... -replace ... Set-Content`, `sed -i`) | Hint to use `edit_file` with `edits` |

`FASTEXEC_NO_SEARCH_GUARD=1` disables the search and broad-root guards.

## read_file

Reads a file in-process: whole, by bytes (`offset`, `limit`) or by lines (`start_line`, `end_line`). Remote files
come over the daemon, no shell involved.

## edit_file

Exact text replacement: `target` → `replacement`, or several changes at once in `edits` (applied in order, all or
nothing). LF/CRLF line endings are matched to the file. Use it for any change to an existing file.

## write_file

Creates a file, optionally `atomic` (temp file + rename). An existing non-empty file is refused unless
`overwrite: true`, so a model cannot wipe a file by accident when it meant to edit it.

## file_ops

`list`, `copy`, `move`, `delete`, `mkdir`, local or remote, with no shell. Lists are compact.

## job

Background work that outlives the call: dev servers, watchers, builds, tunnels.

- Actions: `start`, `wait`, `status`, `list`, `tail`, `stop`.
- `wait` blocks up to 35 s and returns the progress and the log so far; it tells the model not to poll back to back.
- Windows (`local`): a named Job Object per job, log file in `<data_dir>/jobs`, at most 12 running, 12 h lifetime,
  finished jobs kept 7 days; `stop` kills the whole tree.
- Linux (local or remote): `systemd-run --user` units; logs from `journalctl`. Needs a systemd user instance.
- macOS: not supported yet.

## code_outline

Declarations of a source file as `L<line>: <signature>`, or, for a folder, where a `symbol` is declared (skips
dependency and build folders). Go, C/C++, TypeScript/JavaScript, Python, shell, PowerShell. Use it before reading a file
to jump to the right lines.

## code_graph

A code graph of a local Git repository, built on first use and refreshed incrementally on every call (only changed
files are reparsed):

- `callers`: where a symbol is called or referenced;
- `def`: where it is declared, with line range and signature;
- `impact`: files that use what a file or symbol defines (check before refactoring);
- `index`: full rebuild.

Go is parsed with `go/ast`; C/C++, TypeScript/JavaScript, Python, shell and PowerShell with line scanners,
matched by name without type resolution (in C/C++: function and method definitions, classes, structs, unions,
enums, `#define`, `using` aliases, `#include`; prototypes are not declarations).
Only repositories (a `.git` folder) are indexed, each in isolation, up to 10 000 files. Index:
`<data_dir>/telemetry/graph.db`. CLI: `fastexec graph clean` and `fastexec graph repos`.

## handoff

Session notes for the next session, built from the telemetry (files touched, commands that failed) plus the
model's own summary: `save`, `latest` (a ~300-token briefing for a cold start), `search`, `list`. Stored as
Markdown in the handoff folder.

## tool_manage and modules

Modules stay out of the schema until needed: `search` and `list` show them, `call` runs one of their tools without
loading it, `enable`/`disable` add or remove them from the tool list (the client is notified).

| Module | Tools |
|---|---|
| `sql` | `sql`: DuckDB (straight on CSV/JSON/Parquet files, or a named session) and SQLite files, JSON output |
| `hosts` | `ping` (latency to the configured hosts), `get_file`, `put_file` (binary transfer over the daemon) |
| `docker` | `docker_ps`, `docker_logs` |

## Command line

The same binary is a CLI: `fastexec exec [host] <command>`, `get`, `put`, `sql`, `job`, `ping`, `stats`, `graph`,
`handoff`, `config`, `token`, `setup`, `doctor`, `daemon <status|start|stop>`, `version`. Run with no arguments
(or `mcp`) it is the MCP server.
