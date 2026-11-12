# Tools

Every tool takes an optional `host`: `local`, a host from the config, `ssh:<alias>` or an IP. File tools default to
`local` (a Linux path such as `/var/log/x` goes to the default host); `exec` and `job` default to the config's
`default_host`. A path can also carry its host: `server:/etc/hosts`.

An explicit `host` always wins. Without one, if the `default_host` is remote (Linux) and the call carries a Windows
drive path (`W:\...`, `C:/...`) in `command`, `argv` or `cwd`, `exec` and `job` run on `local` and the output opens
with a `[FastExec Auto-Remediation]` note saying so. The path does not exist on the Linux host, and the alternative
was a failed turn plus a retry with `host='local'` (measured in the `ds-full2` benchmark). Detection is
conservative: a drive letter followed by `:` and a slash at the start of a token or after a shell separator — URLs
(`http://`) and loose text never trigger it.

## Tool descriptions

The `tools/list` schema is charged on **every turn**, so it is written as a budget, not as documentation. A tool
description carries only what changes the model's decision: what the tool does, when to use it instead of another
one, and the parameter whose name is not enough. Examples, long explanations and warnings stay here and come back
in the response only when they are relevant, through the mechanisms that already existed:

- `[FastExec FastHint]` on a call that used the wrong tool (`internal/tools/search_guard.go: cat`, `grep`, `find`)
- `[FastExec Auto-Remediation]` on a call that was fixed or refused (wrong host, wrong parameter, refusing shell)
- `tool_manage search`, whose match uses the module tools' own descriptions

A parameter whose name already says it all goes **without** a `description` (`omitempty` in `PropertyDef`). Tool
and parameter names never change for size, and no guard or remediation was removed: the protected facts stay in the
schema — the default `host` may be remote, `{job_over}` is where `exec` stops and `job` begins, `read_file` returns
at most 32 KB, `write_file` refuses an existing non-empty file, and `edit_file` is the tool for changing a file.

`internal/mcp/tools_list_size_test.go` serializes the real `tools/list` and fails if it passes the ceiling (bytes
and tokens = bytes/4); `go test ./internal/mcp -run TestToolsListSize -v` prints the per-tool table. In 2026-11-12
the core list went from 8796 to 6060 bytes (2199 -> 1515 tokens, -31%); the on-demand modules (`sql`, `hosts`,
`docker`) are not part of the per-turn tax and have their own ceiling.

## Parameter names

Before a tool runs, its arguments are matched against the tool's own fields (`internal/mcp/argnorm.go`); no
parameter is ever dropped in silence.

| Sent | What happens |
|---|---|
| a known spelling: `StartLine`, `ByteOffset`, `file_path`, `range`, `cmd`, `op`, `target` on `exec`/`job` (= `host`) or on `read_file`/`write_file` (= `path`) | Read as the canonical name; the result ends with a line saying what was read as what |
| metadata such as `description`, `reason`, `intent` | Ignored, and said so |
| anything else | Refused with the list of valid names (and a close match); nothing runs |

An alias never overrides a parameter that was also sent. `FASTEXEC_STRICT_ARGS=0` turns the refusal into a notice
(the unknown key is dropped). `get_file`, `put_file` and module tools keep their own decoding.

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

A search that finds nothing (exit 1, no output at all) answers `no result (exit 1)` instead of looking like a failure.
If the same search wrote anything to stderr, that message is shown instead: `no result` is only for real silence.
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
| no `host`, remote `default_host`, Windows drive path in `command`/`argv`/`cwd` | Runs on host `local`, with a `[FastExec Auto-Remediation]` note telling the model to pass `host='local'` next time |
| Unix-style line (`grep`/`wc`/`sort`/`sed`/`awk`/`cut`/`ls -la`, `$(...)`, single quotes, `/dev/null`, `find` with Unix flags, Git Bash path) with no cmd signal, on a Windows `local` host that has `bash` (Git for Windows) | `bash -lc <line>` is chosen **before** running: one execution, same cwd, same timeout, same guards. The output carries `[FastExec Auto-Remediation] Unix-style command ran in bash (<path>) instead of cmd.exe`, plus the path translation when one happened |
| Line that starts with a POSIX assignment (`start=$(date +%s%N); …`), with a Unix signal, on a Windows `local` host | Goes to `bash -lc` even when the line also carries a `%NAME%`-looking token (`+%s%N` is not a cmd variable) or a program both shells know (`pwsh`). No valid `cmd.exe` line starts with `VAR=`, and the first token would become the `START` builtin, which swallows the rest of the line: the call would only answer on the timeout (measured: 300 s for a 50 s script) |
| Unix tool missing on a Windows `local` host (`grep`, `rg`, `wc`, `head`, `tail`, `sed`, `awk`, `xargs`, `cat`, `ls`, `python`) on a command that failed with no stdout | `[FastExec Hint]` naming the tool, saying it does not exist here (commands run through `cmd.exe`) and giving the native alternative (`read_file`, `code_outline`, `file_ops`, or `pwsh` with `Select-String`/`Get-ChildItem` through `argv`+`input`); when `bash` exists on the host it also offers `argv ['bash','-lc','<the whole line>']` |
| `find`/`sort` from System32 used with Unix flags (`-printf`, `-name`, `-rn`) | Same hint, saying the binary that ran is the Windows one and takes different flags |
| Git Bash path (`/w/Data/x`, `/c/tmp/a.sh`) in `command` or `cwd` on the Windows `local` host | Translated to the Windows drive (`W:\Data\x`) before running, with a `[FastExec Auto-Remediation]` note showing what changed; skipped when the line hands the text to `bash`/`sh`/`wsl`, and skipped for the `command` when the line is bash-bound (there the POSIX path is the right one and only `cwd` is translated) |
| Windows drive path (`W:\Data\x`) in a bash-bound line | Translated the other way (`/w/Data/x`) before running, because unquoted `W:\x` loses its backslashes in bash; noted on the output |

`FASTEXEC_NO_SEARCH_GUARD=1` disables the search and broad-root guards.

## read_file

Reads a file in-process: whole, by bytes (`offset`, `limit`), by lines (`start_line`, `end_line`), by `ranges`, by
`pattern` or by `symbol`. Remote files come over the daemon, no shell involved, and every selector works the same
on every host.

**Read cap.** Whatever the mode or host, one call returns at most 32 KB. The rest is not lost and not behind a
trick: the footer carries the exact call that continues (`start_line=N`, or `offset=N` for byte reads). There is no
"repeat the call to get everything". A single line longer than the cap is cut on a character boundary and the footer
suggests `pattern` or `offset`. For a whole read of Markdown, YAML, JSON or HTML the footer also lists a short
structure (headings, top-level keys, sections, with line numbers) so the next call can ask for a precise range.
`FASTEXEC_NO_READ_CAP=1` disables the cap.

Big source files asked whole (or with a very large range) answer with their outline first; repeating that exact call
returns the text, still inside the cap.

## edit_file

Exact text replacement: `target` → `replacement`, or several changes at once in `edits` (applied in order, all or
nothing). LF/CRLF line endings are matched to the file. Use it for any change to an existing file.

The reply proves the edit, so there is no reason to re-read the file to check it:

```
[edit_file] 1 change(s) applied (15 -> 15 bytes) in W:\src\Config.cs.
L305: MaxRetries = 5;
line endings kept: CRLF
```

`L<n>` is the 1-based line of the final content where the replacement landed, followed by that line (cut at ~120
characters). One line per change, up to 5; beyond that the count says `(and N more)`. The last line names the line
ending style kept by the file (`CRLF`, `LF`, `mixed` or `none`), plus the encoding when it is not plain UTF-8
(`encoding kept: Windows-1252`, `UTF-16LE`, `UTF-8 BOM`). Nothing about what is written changes.

## write_file

Creates a file, optionally `atomic` (temp file + rename). An existing non-empty file is refused unless
`overwrite: true`, so a model cannot wipe a file by accident when it meant to edit it.

## file_ops

`list`, `copy`, `move`, `delete`, `mkdir`, local or remote, with no shell. Lists are compact.

## job

Background work that outlives the call: dev servers, watchers, builds, tunnels.

- Actions: `start`, `wait`, `status`, `list`, `tail`, `stop`.
- `wait` blocks up to 35 s and returns the progress and the log so far; it tells the model not to poll back to back.
- Fast failure: a job that dies right after starting (missing interpreter, bad flag, file that is not there) answers
  with the exit code and the log in the same call, instead of `started` followed by `status` + `tail`/`read_file`.
  `start` waits ~1.5 s before answering; `status`, `wait` and `tail` do the same for a job that lived less than ~10 s
  and left a short log (< 1 KB). When the exit code says the program does not exist (`python`, `pip`, `node`, `go`…),
  the reply carries the same `[FastExec FastHint]` the `exec` tool already gives. A job that is still running answers
  exactly as before, and `stop`ped jobs keep the old `STOPPED` reply.
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
