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
8. Build and test output (`go build|test|vet`, `dotnet build|test|...`, `npm|yarn|pnpm|bun test|build|lint`, `npx tsc`,
   `cargo build|test|check|clippy`) is compacted on top of the above: build progress leaves, and every error and
   warning with its `file:line` stays, including the context lines right below it (the `-->` of rust, the code frame
   of jest). Full success becomes one summary line (`go test: 3 packages ok, 1 without tests, 0 failures.`); a failure
   keeps all errors and only the scores. The compaction never runs if it would save fewer than 2 lines, if a failure
   carries no recognisable error signal, or if any error line of the original would be missing from the result — in
   those cases the full text is returned untouched.
   It always announces itself in the reply: `[FastExec] output compacted (N lines → M); use --raw for the full text.`
   `--raw` after a recognised build/test command (never after a `--`) and the `FASTEXEC_NO_OUTPUT_FILTER` environment
   variable (`FASTEXEC_NO_BUILD_COMPACT` as an alias) turn the whole output filter back off. `npm start`, `vite --watch`,
   `go run`, `dotnet run` and anything outside the list are never touched.

### Elapsed time

A command that takes 3 s or more ends with a short `[elapsed 50.3s]` line, past 2 minutes `[elapsed 2m05s]`. The line
comes last: after `[exit N]`, after the guard/remediation notes and after any spilled output. The model no longer has to
repeat the command wrapped in `date +%s%N` to learn how long it took (measured in the `P3` task of bench ciclo1b: two
execs, 106 s and 12.9 k–28 k tokens, against one call and 54 s for the same work). Below 3 s nothing changes — the reply
is byte for byte the one from before. A timeout keeps its own `[timeout after Xs]`, and a command promoted to a
background job already says how long it ran.

A search that finds nothing (exit 1, no output at all) answers `no result (exit 1)` instead of looking like a failure.
If the same search wrote anything to stderr, that message is shown instead: `no result` is only for real silence.
A non-zero exit is reported with its code and the first meaningful error line. When the command wrote to **both**
channels, the stdout is kept as well, under `[stdout before the failure]` with a `[FastExec]` line saying it came
before the failure: in a compound line (`grep … && python …`) the first stage may have printed exactly what was asked
and only the second one failed — dropping it made the model re-run the whole command just to read output it already had.

### Guards and corrections

Each one tells the model what it did and what to do next time.

| Pattern | What happens |
|---|---|
| `cat file`, `type file`, `Get-Content file` | Served by `read_file` (no process) |
| `ls`, `dir` of a folder | Served by `file_ops list` — except the recursive forms (`ls -R`, `dir /s`), which run and come back grouped (see *Grouped listings*) |
| `sleep N` / `timeout N` | Up to 15 s: done with a native timer. Longer: refused, with a pointer to `job` |
| `cd <host default cwd> && ...` | The redundant `cd` is removed |
| `grep`, `rg`, `findstr`, `Select-String` (also inside `pwsh -Command`) | Treated as searches by the rows below |
| recursive `grep` | Gets `--exclude-dir` for `.git`, `node_modules`, `dist`, `build`, `vendor`, caches... |
| search output over 40 lines | Cut to 40 with a hint to narrow it or use `code_outline` |
| search for a declaration (`void Foo`, `struct Row`, `func X`) | Hint to use `code_outline` |
| search for a function's call sites | Hint to use `code_graph` |
| simple search for one identifier, in a repo that has the code-graph index | Answered from the graph, in the shape of the question — see *Graph-first search answers* |
| recursive scan of `C:\`, `C:\Users`, `/`, `/home` | Refused (it would take minutes and flood the context) |
| PowerShell cmdlet in `cmd.exe` | Hint to use `pwsh` |
| child processes left running after the command | Killed, with a warning to use `job` |
| edit by script (`Get-Content ... -replace ... Set-Content`, `sed -i`) | Hint to use `edit_file` with `edits` |
| command that deletes code files (`rm`, `del`/`erase`, `Remove-Item`, `git rm`), on a repository that already has a graph index | After a successful run, one `[graph] <file> deleted: ... still used in N files: a.go:12, ...` line naming the symbols of the deleted file that other files still use — the index consulted is the one from **before** the deletion. Silent without an index, on a remote host, or when nothing uses those symbols (see `edit_file`) |
| no `host`, remote `default_host`, Windows drive path in `command`/`argv`/`cwd` | Runs on host `local`, with a `[FastExec Auto-Remediation]` note telling the model to pass `host='local'` next time |
| Unix-style line (`grep`/`wc`/`sort`/`sed`/`awk`/`cut`/`ls -la`, `$(...)`, single quotes, `/dev/null`, `find` with Unix flags, Git Bash path) with no cmd signal, on a Windows `local` host that has `bash` (Git for Windows) | `bash -lc <line>` is chosen **before** running: one execution, same cwd, same timeout, same guards. The output carries a short `[FastExec Auto-Remediation] Unix-style line ran in bash (<path>) instead of cmd.exe (Windows host)` note with the next step, plus the path translation when one happened |
| Line that starts with a POSIX assignment (`start=$(date +%s%N); …`), with a Unix signal, on a Windows `local` host | Goes to `bash -lc` even when the line also carries a `%NAME%`-looking token (`+%s%N` is not a cmd variable) or a program both shells know (`pwsh`). No valid `cmd.exe` line starts with `VAR=`, and the first token would become the `START` builtin, which swallows the rest of the line: the call would only answer on the timeout (measured: 300 s for a 50 s script) |
| Unix tool missing on a Windows `local` host (`grep`, `rg`, `wc`, `head`, `tail`, `sed`, `awk`, `xargs`, `cat`, `ls`, `python`) on a command that failed with no stdout | `[FastExec Hint]` naming the tool, saying it does not exist here (commands run through `cmd.exe`) and giving the native alternative (`read_file`, `code_outline`, `file_ops`, or `pwsh` with `Select-String`/`Get-ChildItem` through `argv`+`input`); when `bash` exists on the host it also offers `argv ['bash','-lc','<the whole line>']` |
| `find`/`sort` from System32 used with Unix flags (`-printf`, `-name`, `-rn`) | Same hint, saying the binary that ran is the Windows one and takes different flags |
| Git Bash path (`/w/Data/x`, `/c/tmp/a.sh`) in `command` or `cwd` on the Windows `local` host | Translated to the Windows drive (`W:\Data\x`) before running, with a `[FastExec Auto-Remediation]` note showing what changed; skipped when the line hands the text to `bash`/`sh`/`wsl`, and skipped for the `command` when the line is bash-bound (there the POSIX path is the right one and only `cwd` is translated) |
| Windows drive path (`W:\Data\x`) in a bash-bound line | Translated the other way (`/w/Data/x`) before running, because unquoted `W:\x` loses its backslashes in bash; noted on the output |
| Line with unambiguous PowerShell syntax (`$env:X=...`, `$x = ...` starting a stage, the call operator `& '...'`/`& "..."`, `-ErrorAction` and friends, or a cmdlet like `Get-*`/`Set-*`/`Select-String`/`ForEach-Object`) on the Windows `local` host | `pwsh -NoProfile -Command <line>` is chosen **before** running — and before the bash plan, because `$env:X=1; & "C:\...\x.exe" build` looks Unix enough to die in bash with `syntax error near unexpected token '&'`. Same cwd, timeout, exit code and stderr. The output carries `[FastExec Auto-Remediation] PowerShell syntax detected: ran in pwsh. Next time pass argv ['pwsh','-NoProfile','-Command','<line>'].` A mixed line (any stage using a Unix-only tool, like `grep x . \| Select-Object -First 5`) is left on its old path. `powershell` is used when `pwsh` is not installed; with neither, nothing changes |
| Recursive file listing (`dir /s /b <glob>`, `ls -R`, `find . -name '<glob>'`) of more than 80 files | Regrouped, one line per directory with its count and every name of code kept — `src\Serilog\Core\ (12): A.cs B.cs ...` — plus the footer `[FastExec] listing grouped by directory; --raw returns the original lines.` Inside `.git`, `node_modules`, `bin`, `obj`, `__pycache__`, `.vs`, `target` and `dist` the paths are counted instead of printed, and the footer names them and the count: `4218 item(s) omitted in .git, node_modules (--raw returns everything).` Nothing is omitted when the listing was asked *inside* one of those folders, or when it would leave the answer empty. See *Grouped listings* |

`FASTEXEC_NO_SEARCH_GUARD=1` disables the search and broad-root guards.

### Reading the command, per tool

Every text search the graph can answer is read the same way, whatever the tool: `grep`/`egrep`/`fgrep`, `rg`, `git grep`,
`findstr` and `Select-String`/`sls` each have their own flag reader and all of them feed one internal query, so the
answer above is produced by one path instead of one per tool. Things that do not survive the reading fall back to the
real command, on purpose:

- `findstr` without `/s` (a single directory), or with `/i`, `/v`, `/x` or `/g:` — a different set of lines than the
  identifier;
- a wildcard that is not exactly `*.<extension the index covers>` (`*.cs` in a Go repository, `Foo*.go`, `*Bar*`): the
  answered set could be smaller than the asked set;
- a path or glob outside the indexed part of the repository, `rg` with a regex, or a repository over the file limit.

`--raw` (or a `raw:` prefix) skips all of it and is stripped from the command *and* from `argv`, so the footer hint
works verbatim.

### Grouped listings

Listing a directory recursively is the other expensive way of asking where the code is: `dir /s /b src\Serilog\*.cs`
answered 8 KB in one call, and in the same benchmark run `ls -R`, `find . -name '*.ext'` and 15 plain `ls` did the same
in smaller pieces. No file of code is dropped and no file of code is summarised — the original text is regrouped, one
line per directory:

```
src\Serilog\Core\ (12): Casing.cs LevelOutputFormat.cs Logger.cs PropertyBag.cs ...
src\Serilog\Sinks\ (7): ConsoleSink.cs FileSink.cs SeqSink.cs ...
```

followed by `[FastExec] listing grouped by directory; --raw returns the original lines.` The regrouping only happens on
a command that is unambiguously a recursive listing (`dir` with both `/s` and `/b`, `ls` with `-R`/`--recursive`,
`find` with `-name`/`-iname` and no `-exec`/`-delete`), on more than 80 files, and only if **every** line of the output
looks like a plain path — one line in columns, with a tab or with padded spaces aborts it and the original text is
returned untouched. `--raw` in the line, or `FASTEXEC_NO_LISTING_GROUP=1`, returns the original lines.

**Generated and dependency folders are left out.** In a real repository the bulk of a recursive listing is not code:
`.git` internals (`hooks`, `objects`, `refs`), `node_modules`, `bin`, `obj`, `__pycache__`, `.vs`, `target`, `dist`.
Those paths are counted, not printed, and the footer says what was left out:

```
src\Serilog\Core\ (12): Casing.cs LevelOutputFormat.cs Logger.cs PropertyBag.cs ...
[FastExec] listing grouped by directory; 4218 item(s) omitted in .git, node_modules (--raw returns everything).
```

- The list is matched by **segment**: a path is dropped when one of its folders is named like that, so `.git` is
  dropped wherever it sits, not only at the requested root.
- Asking for the inside of one of them is asking for exactly that listing: `dir /s /b node_modules`,
  `dir /s /b node_modules\*.js`, `find node_modules -name '*.js'` omit nothing and print every name.
- When **everything** found is inside those folders, nothing is omitted — an empty answer is worse than the full list.
- A listing below the 80-file threshold is untouched, and so is any listing with no such folder in it: the footer is
  byte-identical to before, and `--raw` (or `FASTEXEC_NO_LISTING_GROUP=1`) returns the complete output.

### Graph-first search answers

A simple search line that only asks *where a code identifier is declared and who uses it* is answered from the code
graph, in one call, in the shape of the question: no raw lines to re-check and no follow-up `read_file`.

| The line | What comes back |
|---|---|
| `grep -rn Foo .` / `-rw`, `rg -n Foo`, `git grep -n Foo`, `findstr /s /n Foo *.go`, `Select-String -Pattern Foo` | One block: `def:` file, line range, kind and signature; the **body** when the name is declared once and the body is ≤ 40 lines (otherwise the signature plus the `read_file(path, start_line, end_line)` call that fetches it); then `callers:` grouped by file with counts, headed by `non-test: N file(s) \| tests: M file(s)` |
| `-l` / `--files-with-matches` | Only the file list, non-test first |
| `-c` | Count per file, non-test first; a file that only declares the name still appears, with `0` and `(declares it)` |
| `find . -name 'store.go'`, `ls -R . \| grep store`, `git ls-files \| grep store` | The indexed files matching the name, non-test first |
| Name declared in more than one place | `ambiguous (N definitions)` plus the short definitions — no body is picked for you |
| Pattern with regex, alternation, anchor or class, or `-i` | Untouched: a real search (`-i` and `^Foo$` find a different set of lines than the identifier does) |
| Name not declared in the code, or repo with no graph | Untouched: the real search runs |

The answer is capped at 3 KB and ends with what the index covers and how to escape it:

`index covers code identifiers only (comments, strings and docs are not listed). For the raw search: <line> --raw`

Freshness comes first: before answering, the index is compared with the repo (HEAD + `git status --porcelain`) and a
stale index is reindexed on the spot — incrementally, only the changed files, inside a 900 ms budget. Over budget the
partial pass is thrown away (its recorded state is deleted, so the next call cannot skip what is missing) and the real
search answers. **Repeating the exact same line** also runs the real search, and `--raw` (anywhere in the line) or a
`raw:` prefix forces it on the first call; the escape is stripped from the command *and* from `argv` before running, so
the hint in the footer works verbatim. An automatic reindex announces itself with `[FastExec Graph] index`, like every
other auto-remediation.

Any local Git repo within the ≤ 10 k files limit is answered this way, even one that never used `code_graph`: the
first search builds the index on the spot (announced) and answers from it, and only a build that does not fit in the
900 ms budget falls back to the real search — the note then says whether the index did not exist yet or was simply
behind. Remote hosts and repos over the file limit keep getting the plain search. `FASTEXEC_NO_GRAPH_SEARCH=1`
disables these answers.

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

A folder is not a file: the call is refused saying which tool to use instead (`file_ops` `list`, `code_outline`, or a
search line through `exec`) — `pattern` looks inside a single file only. Before, the raw OS error came back, and on
Windows that is the localized `Função incorreta`, which tells the model nothing.

**A path that does not exist suggests candidates.** The error keeps its own message and gains up to five existing
paths, one per line, after `not found; did you mean:`. Candidates come from two places: the file's folder and its
ancestors up to the repository root (or three levels up when there is no repository), matched by same base name
ignoring case, same stem with another extension, or a small edit distance (names shorter than 6 characters only match
on the name itself, or on the same stem); and, when a graph index already exists for that repository, the base name
looked up as a symbol — which is what finds `LevelOutputFormat.cs` when the ask was `Casing.cs`, whose file name looks
like nothing in the folder:

```
... Casing.cs: The system cannot find the file specified.
not found; did you mean: src/Formatting/Display/LevelOutputFormat.cs (declares Casing)
```

Nothing is read in place of what was asked: only paths are offered, and the content always comes from the file that
was requested. With no candidate the message is exactly the one from before. The symbol lookup never reindexes —
a read that already failed does not pay for a freshness pass — so a stale index can only suggest an extra file, and
every candidate is checked on disk before being offered.

**Types of what was read.** When the excerpt contains a declaration that leaves the type to the compiler
(`var x = ...`, `x := ...`, `const y = ...`, an inferred lambda parameter) and a language server for that file is
**already warm**, the answer ends with one compact line:

```
types: pedido: Reader, total: int
```

That is the one thing the editor gives for free and the model does not: without it, knowing the type of `pedido`
costs opening another file — exactly the cost this line removes. The type is never guessed: it comes from the
server's inlay hints, or from hover when the server has no hints, always at the exact column the declaration was
found in the excerpt (in UTF-16 units, which is how servers count columns, so an accent earlier in the line cannot
shift the annotation onto another symbol). Reading a file never starts a server: cold server, `FASTEXEC_LSP=off`,
language without a server, no answer within ~1 s, or the read cap cutting the excerpt — and the response is
byte-identical to before, with no line and no note. At most 8 names (`typeLineMaxNames`) and 400 bytes
(`typeLineMaxBytes`); a declaration that writes its type is not annotated, because that would spend a request to
repeat the text. `code_outline` appends the same line for the bodies of the declarations it shows.

## edit_file

Exact text replacement: `target` → `replacement`, or several changes at once in `edits` (applied in order, all or
nothing). LF/CRLF line endings are matched to the file. Use it for any change to an existing file.

The reply proves the edit, so there is no reason to re-read the file to check it:

```
[edit_file] W:\src\Config.cs: +1 -1 lines, 1 hunk(s).
L305: MaxRetries = 5;
line endings kept: CRLF
```

The first line is the whole summary of the call — file, how many lines the replacements put in and took out, and how
many hunks were applied — followed by the automatic notes (`[graph]`, `[lsp]`, `line endings kept: …`). Neither the
file nor the diff comes back, and the reply does not grow with the size of the file: 500 lines or 5, the summary line
is the same shape. A **failure** is the one case that still returns the full message (the target that was not found,
the closest text, `nothing was written`), because that is where the text is needed to fix the call.

`L<n>` is the 1-based line of the final content where the replacement landed, followed by that line (cut at ~120
characters). One line per change, up to 5; beyond that the count says `(and N more)`. The last line names the line
ending style kept by the file (`CRLF`, `LF`, `mixed` or `none`), plus the encoding when it is not plain UTF-8
(`encoding kept: Windows-1252`, `UTF-16LE`, `UTF-8 BOM`). Nothing about what is written changes.

### Callers of a symbol whose signature changed

When the edit changes the **header** of a declared symbol (its signature) or removes/renames it, and other files
use that name, the reply adds one line naming them — so the model edits the callers in the same turn instead of
spending a turn finding out who calls it:

```
[edit_file] W:\src\Fetch.cs: +1 -1 lines, 1 hunk(s).
L3: public static async Task<Item> Fetch(int id, CancellationToken ct) {
[graph] Fetch signature changed: 3 use(s) in 3 file(s) may need update: a.go:12, b.go:40, +4 more (tests: 2). By name, not type-checked.
```

- Fires only on signature change or removal/rename. A body-only edit, a comment, formatting or a **new** symbol
  changes nothing in the reply.
- The name is matched by name, not by type: treat the list as "look here", not as a compile error. A name declared
  in more than one place is not listed at all — the line says `name is ambiguous (K definitions): check callers with
  code_graph` instead, because the graph cannot tell which one the use belongs to.
- When a **language server** for the edited file is already warm (see `code_graph`), the same line comes from the
  type-checked answer instead, and says so — `Exact, gopls.` replaces `By name, not type-checked.`, the ambiguity
  caveat disappears (each homonym is resolved separately) and an empty answer means "no other file uses it", so the
  line is simply absent:

  ```
  [graph] Fetch signature changed: 3 use(s) in 3 file(s) may need update: a.go:12, b.go:40, +4 more (tests: 2) Exact, gopls.
  ```

  A server that is not already running is **not** started for this: the edit is already written, the reply may lose
  precision but never time (~120 ms). Off, cold, still loading or erroring, the line stays exactly as above.
- The edited file itself is never in the list; test files are counted apart (`tests: N`), and at most 5 locations
  are quoted before `+K more`. One warning per symbol, at most 3 per call (a batch edit can touch several).
- Requires the repository to already have a graph index (a `.git` and a `graph.db` from a previous `code_graph`
  use). Nothing is indexed on demand and no database is created: without an index the reply is exactly what it was
  before. The query is capped at ~150 ms; past that the line is dropped rather than delay the edit.
- Files whose language has no parseable declarations (`.json`, `.md`, images …) never trigger it.

The same list comes back for `write_file` with `overwrite: true` over an existing file, and with `exec` when the
command deletes code files (`rm`, `del`/`erase`, `Remove-Item`, `git rm`):

```
[graph] old/parser.go deleted: Fetch, Parse still used in 2 files: b.go:12, c.go:40, +2 more.
```

### The edit repaired by the language server

Above is the answer when there is no server. When a language server exists for the edited file **and it owns the
project** (it starts and loads the repository), the same edit is completed the way the editor would complete it —
automatically, without asking, and the reply says what was done. Every line is marked `(exact)`; the by-name warning
of the previous section is not repeated for names the server already answered for.

```
[edit_file] W:\src\a.go: +1 -1 lines, 1 hunk(s).
L3: func ParseRecord() {}
DONE ParseLine -> ParseRecord: all 8 uses (declaration + 7 call sites) in 5 files updated by the compiler's rename (exact; same-named members of other types untouched): a.go:3, b.go:40, +3 more. Nothing else to change: do not grep or re-check. undo: exec 'fastexec undo'
[lsp] files changed: callers.go, main.go; re-read before editing them
[lsp] kept on purpose (another type's member, not this symbol): a.go:12 `r.ParseLine(x)`
[lsp] still named ParseLine in non-code files (not renamed): docs/api.approved.txt:9 `func ParseLine(s string) Record`
✓ compila: 0 erros neste arquivo e nos outros 4 tocados (código verificado pelo servidor; texto não)
```

The reply is written to be *believed*, so the model does not re-run the checks it would otherwise run itself: the
count is reconciled (declaration + call sites), the same-named member it would grep for is named as kept, the text
file that still needs a hand edit comes with its line (no need to open it), and the final line states how many files
were checked and that text files were not.

- **Rename started from a call site.** An `edit_file` whose only change is one member name swapped for another after a
  dot (`SelfLog.WriteLine(` -> `SelfLog.Write(`) is recognised as the rename of that member: the compiler's rename
  runs from that use, so the declaration and every other use change in the same call. It stays silent and leaves the
  model's edit alone when the new name already exists next to the declaration (`Log.Info` -> `Log.Debug` is a choice,
  not a rename), when the server cannot rename the symbol, or when the edit changes anything else. Files the model never
  opened are not named in the reply (naming them only invites reading them); files it did open keep the "re-read" line.
  If the model had already grepped the old name, the check line answers that same search ("your grep ... found N lines
  in M files; same search now: 0").
- **Rename, also with a rewritten comment.** `edit_file symbol='ParseLine' replacement='ParseRecord'` (the replacement
  is a bare identifier) renames the declaration instead of replacing its whole body. A rename edit that also rewrites
  the comment right above the declaration (one contiguous hunk, even if it adds or removes lines) is still a rename:
  the server's text is applied and the model's hunk is put back on top. After the rename, a
  `[lsp] still named ParseLine in non-code files (not renamed): ...` line lists `.txt/.md/.json/.xml/.yml`
  files (API approval files, docs) that keep the old name; the scan is capped at 400 ms.
- **Rename.** Only the identifier changed: `prepareRename` + `rename` are asked, and the resulting `WorkspaceEdit`
  is **written** into every file it covers, not merely listed. The acceptance rule is the safety rule: the server's
  version of the edited file must be the text being written once both names are forgotten, so an edit that changes
  anything else (the wrong homonym included) is refused and the by-name warning takes over.
- Above **100 files** nothing is written and the reply says how many would change: a rename that size is a decision
  to take explicitly, not a side effect of `edit_file`.
- With git, files that were **already dirty** outside FastExec are written anyway and listed at the end (`already
  modified outside FastExec: ...`) — someone else's work in progress is not a reason to stop, but it is a reason to
  say so.
- **Undo, without git and without a backup:** FastExec keeps in memory only the **last** write (the files it changed
  and the hash of what it wrote). `exec 'fastexec undo'` restores them; a file changed since is left alone and named.
  A new file is removed. Restarting the daemon empties it. The files that changed are named, because the model's copy
  of them is stale.
- **Signature.** The header changed: if the server offers a ready-made refactor it is applied (same patch/undo
  line, `undo: exec 'fastexec undo'`); otherwise the exact callers come back as `Save: signature changed; exactly 3 caller(s) need the new arguments
  (complete list, no grep needed): a.go:12, b.go:40` — the callers are **not** rewritten by guessing (FastExec cannot
  choose the value of a new parameter). Overloads are paired by order, so a same-named overload is not blamed.
- **Imports.** A new unresolved-symbol error is offered to the server's quickfix, and only import-shaped actions are
  accepted: `[lsp] added import "fmt"` (or `removed unused import`, `organized the imports`) — and only when the
  server classifies it that way. Arbitrary quickfixes are never applied.
- **Diagnostics.** See *Validation of the written code* below: errors only, never warnings/info/hints.
- **Formatting.** Only when the file was already formatted and the server's edits fall inside the range that was
  edited: `[lsp] formatted the edited range (2 edit(s))`. A whole-file reformat is not a side effect of a two-line
  edit.
- **Removal.** A declaration that disappeared and is still used comes back as `removed but still used 3 time(s)
  (exact): a.go:12, b.go:40`, which is what `exec_delete_impact` guessed before when a server is around.

No step can ever lose the edit, which is already on disk by then (it is never reverted; the undo patch stays).
`FASTEXEC_LSP=off` turns all of it off, validation included.

#### Validation of the written code

Every `edit_file`, `write_file` (new files too) and `exec` that writes a `.go`/`.cs` file ends with a footer of
**errors only**, never silence:

```
✗ Foo.cs:30 CS1002 ; expected │ var x = 1
✗ Bar.cs:44 CS7036 no argument given for 'ct' (effect of your edit)
```

- **Layer 1, exact syntax, in ms, no project needed:** Go by `go/parser`; C# by `fx-roslyn`, a helper built once from
  the SDK's Roslyn and kept resident. With a syntax error the semantic layer stays quiet (it would be cascade noise).
- **No git needed.** The project is recognised by its markers (`.sln`/`.csproj`, `go.mod`, `package.json`,
  `Cargo.toml`, `pom.xml`...), so validation, rename, signature and undo work in any folder of a known technology.
- **Layer 2, semantics, by the language server:** only a diagnostic of the right document version is accepted
  (stale publishes are discarded); gopls runs in pull mode. A new file is validated whole, every error counts as
  new; a file outside any project gets syntax only.
- Statuses: `✓ compila` (types verified) or `✓ sintaxe · tipos pendentes (servidor carregando)`. In the second case
  the result arrives **once** on the next FastExec reply as `⏱ diagnóstico tardio Foo.cs: ...`, if the file did not
  change meanwhile.
- Up to 3 errors per file, 2 other files; the code line is trimmed to 80 characters. Dependents: when a signature was
  changed, removed or renamed, new errors in up to 5 dependent files appear as `(efeito da sua edição)`.
- The language server is pre-warmed in the background on the first read/outline/graph/edit in a repo.
- **exec:** the files a command line mentions are compared by mtime/size before and after; only the ones it changed
  are validated as new files. Code written by a program the line merely runs (a generator) is not mentioned and is not
  seen.

## write_file

Creates a file, optionally `atomic` (temp file + rename). An existing non-empty file is refused unless
`overwrite: true`, so a model cannot wipe a file by accident when it meant to edit it. Overwriting a file that
exists can bring the caller list above.

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
- A finished job says how long it lived: `ran 50.3s` in the `status`/`tail` header and in `list` (`LOCAL`) or in the
  `[job] EXITED` line of a Linux job that ended after the fast-failure window. Before, `status`/`list` showed `up ...`
  counted from the creation — still growing long after the process was gone — and the raw `systemctl` text never named
  the duration. `start`/`wait` keep their existing `after Xs` wording.
- Windows (`local`): a named Job Object per job, log file in `<data_dir>/jobs`, at most 12 running, 12 h lifetime,
  finished jobs kept 7 days; `stop` kills the whole tree.
- Linux (local or remote): `systemd-run --user` units; logs from `journalctl`. Needs a systemd user instance.
- macOS: not supported yet.

## code_outline

Declarations of a source file as `L<line>: <signature>`, or, for a folder, where a `symbol` is declared (skips
dependency and build folders). Go, C/C++, C#, TypeScript/JavaScript, Python, shell, PowerShell, CSS/SCSS/LESS, HTML
and Razor (`.razor`/`.cshtml`). In a stylesheet a declaration is a class or id selector (one per name, so `.a, .b`
gives two) and each `--var`; in HTML/Razor it is an `id`, and in JSX/TSX a component or a custom hook. Use it before
reading a file to jump to the right lines.

Markdown is accepted too: the output is its sections, one per heading, named by the heading path (`README.md > Install >
Linux`) and rendered as `L<start>-L<end>: <heading>`. When the file has a reference that no longer lands — a link to a
file that does not exist, or an anchor no section of the target matches — a last line
`docs: N broken refs: README.md:12 -> setup.md (not found)` lists up to 5 (then `(+K more)`). That is how stale
documentation surfaces without reading the file. A whole folder still lists code only; Markdown reaches you through
`code_graph` (next section).

## code_graph

A code graph of a local Git repository, built on first use and refreshed incrementally on every call (only changed
files are reparsed):

- `callers`: where a symbol is called or referenced;
- `def`: where it is declared, with line range and signature;
- `impact`: files that use what a file or symbol defines (check before refactoring);
- `index`: full rebuild.

### a folder that is not a repository

Outside Git the folder itself is indexed as a **standalone folder**, in the same `graph.db`, under its own key (the
normalized path). There is no `.gitignore` there, so the scan reads the disk and is capped at 3000 code files or 64 MB
of them; a folder above either limit is refused exactly as before (`not inside a git repository`), with the path the
caller asked for. Junk directories are still skipped (`bin`, `obj`, `node_modules`, `.git`, ...).

Freshness does not depend on Git there: with no HEAD and no `git status`, every call compares `(mtime, content hash)`
per file, which is what the incremental pass already does — editing a file is picked up by the next call. Every answer
from a standalone folder starts with one line so the model knows what it is reading:

```
[graph] not a git repository: indexed the folder as standalone (42 files); results reflect the files on disk now.
```

Results from a standalone folder come from the files on disk in that moment, untracked and uncommitted included. The
line is a warning, not a promise: there is no revision to name and no history to fall back on. Inside a Git
repository nothing changes and no such line appears.

### the first query does not pay for the index

The full scan of a repository that has no index yet (0.2–0.4 s on a ~250-file repo, more on a large one) used to run
inside the first `code_graph` call, with the model waiting for it. It now starts earlier: any tool call that carries a
local path inside a Git repository — `exec` with `cwd`, `read_file`, `edit_file`, `code_outline`, `code_graph` — kicks
one pass for that repository, in parallel with the call itself, and the answer is ready by the time the graph is asked.

One pass per repository (concurrent calls join the one already in flight), one pass at a time in the whole process,
running below normal priority, cancellable, and nothing is written outside `<data_dir>/telemetry/graph.db`. A folder
outside Git is never scanned on its own: to index one, call `code_graph` on it explicitly. `FASTEXEC_BG_INDEX=off`
disables all of it.

A `code_graph` query that arrives while the pass is running waits for it, up to 8 s, and answers complete. Past that
deadline it answers with whatever the index already has, plus one line — never a dry error, never a hang:

```
[FastExec] the index is still being built in the background (37/120 files); partial answer. Ask again in a few seconds.
```

On shutdown the pass is cancelled and awaited briefly, so a half-written index is not left behind.

### exact or by name

`callers`, `def` and `impact` (of a symbol) answer from the repository's own **language server** when there is one
for the language and it starts and loads: `=== callers of 'Fetch' (exact, gopls) ===`. The answer is then
type-checked — two methods with the same name on different types are two different symbols, and the header says
which server answered. The name-based graph remains the answer for everything else (`depth` > 1, `impact` of a file,
a language with no server) and stays the fallback when the server cannot answer, in which case the old output comes
back with one extra line naming the reason:

```
lsp unavailable (server not installed); by name
lsp unavailable (project did not load); by name
lsp unavailable (project did not load: <the last thing the server said>); by name
lsp unavailable (timed out); by name
```

When the project fails to load, the last line the server wrote on its own stderr — kept in memory, the last ~40
lines, never in a log file — rides along in the note, because the server already knows why it gave up. Queries that
arrive while the server is still starting wait for the same load gate instead of failing early: two exact queries at
once both get the exact answer, not one failure and one success.

`def` answers the whole symbol, not just where its signature starts (`344-526`, not `344-344`), and the exact `impact`
names each use the way `callers` does (`- src/app.go:12 in ParseRecord (call)`, at most three uses per file and then
`... N more use(s) in this file`) — a line saying `1 refs` would make you open the file to find out where.

That line is the whole honesty rule: a **partial** answer is never presented as exact — if one of the declarations
of a homonymous name fails to resolve, the whole exact result is withheld and the by-name answer is given instead.
The note is silent when there was no server to begin with (no server installed for the language, no declaration of
that name in the index, `FASTEXEC_LSP=off`), because the by-name answer is then simply the answer. Servers are
started on demand, kept warm for 15 minutes and never downloaded; `fastexec lsp doctor` shows what was found.

What each language gets, and what it needs installed to get it:

- **exact once the server is installed** — Go (`gopls`), C# (`csharp-ls`), TypeScript/JavaScript
  (`typescript-language-server`), Python (`pyright-langserver`, or `pylsp` as the fallback for a machine without
  npm), C/C++ (`clangd`), Rust (`rust-analyzer`), Java (`jdtls`), Kotlin (`kotlin-language-server`), Zig (`zls`).
  All of these are reported by `lsp doctor` when missing, because for them the by-name answer really is a fallback.
- **exact when the server is installed, silence when it is not** — JSON (`vscode-json-language-server`), YAML
  (`yaml-language-server`), Markdown (`marksman`, optional: the documentation graph already navigates it), Shell
  (`bash-language-server`) and PowerShell (PowerShellEditorServices, which has no installer of its own — it is the
  `.ps1` launcher inside the editor bundle, started through `pwsh`). These are already indexed by the name-based
  graph, so a missing server is not a degradation and adds no note; `lsp doctor` still lists the install command.
- **always by name** — every language with no entry in the table (Ruby, PHP, Lua, Haskell, CSS, HTML, Razor, a
  Makefile): nothing is started, no note is printed, and the answer is the graph's.

Two servers are honestly less exact than the rest even when they start, and `lsp doctor` says so: C# needs
`dotnet restore` first, and `clangd` guesses its compile flags unless a `compile_commands.json` exists (generate it
with `cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON ..`, `bear -- make` or `compiledb -n make`). `jdtls` is a launcher
script with no version flag and keeps a private index directory per project outside the repository.

#### C# (csharp-ls)

C# is answered by `csharp-ls` (Roslyn), which is the only server that resolves a C# symbol exactly: it separates two
overloads, two types' same-named methods, an interface from its implementations, a `partial` class's halves, an
extension method from the method it extends, and `nameof`. It needs two things of the machine and never provides them
itself: a **.NET SDK** (found on `PATH`, or through `FASTEXEC_DOTNET_SDK`/`DOTNET_ROOT`, or in `W:\Data\dotnet-sdk\sdk`)
and a **restored project** — a project with no `obj/project.assets.json` answers empty reference lists, which would read
as "no callers", so it is refused instead:

```
lsp unavailable (no .NET SDK found (install one, or set FASTEXEC_DOTNET_SDK to its folder)); by name
lsp unavailable (global.json pins SDK 7.0.102; installed: 8.0.404 (set rollForward or install 7.0.102)); by name
lsp unavailable (restore needed: run dotnet restore (src/App/App.csproj)); by name
```

A `global.json` that pins an SDK the machine does not have is named before anything else is tried: the file (in the
root or above it) is read and compared against the installed SDKs, honouring the `rollForward` it declares, so the
answer names the version instead of blaming the restore. That `global.json` is only read — the file belongs to the
team and is never rewritten.

FastExec never runs `dotnet restore` — that downloads packages, and downloading on someone's behalf is not its
decision. The server loads the nearest `.sln` above the file (falling back to the nearest `.csproj`), because references
are only exact across projects when the whole solution is loaded. That load is slow on a large solution (tens of
seconds), so the first query in that root answers `lsp loading C# solution…; by name` and falls back to the name graph;
later queries use the server. Files whose name ends in `.g.cs` (generated) are ignored. C# multiple `TargetFramework`s,
Razor and Blazor are out of scope: `.razor`/`.cshtml` stay **by name only**.

Two defects of `csharp-ls` 0.16 are worked around inside the client rather than trusted: its `textDocument/rename`
returns edits for **every overload** sharing the name (so a rename is built from the reference list of the one symbol
under the cursor, each edit verified against the file on disk before it is offered), and its
`textDocument/codeAction` expects the diagnostics of the range in the request — a null list kills the process, so the
diagnostics of the last pull are cached and sent. Hover, diagnostics, the `using` quickfix, formatting and
`documentSymbol` are exposed as library calls and covered by tests, but are not yet tools; only `callers` and `def`
reach the LSP.

`callers` takes an optional `depth` (1 = direct callers, the default and the same output as always; up to 4). With
`depth` > 1 the answer is the transitive closure, deduplicated: one line per symbol, grouped by level (`depth 1:`,
`depth 2:`, ...), each with `file:line` and the enclosing symbol. So "what reaches this function, directly or
indirectly" is one call instead of climbing the graph hop by hop. A cycle (`A` calls `B` calls `A`) dries out: the
starting symbol never comes back. `max` limits the lines shown in total (the footer always carries the real total),
and the levels past the frontier cap stop there. When the result contains test files (`_test.go`, `.test.ts`,
`test_*.py`, a `tests/`/`spec/` folder), a line `tests: N files: a, b, c` comes first, which answers "which tests
exercise this" without another call. At `depth` 1, when the direct callers are few, none of them is a test and the
closure reaches further, one line `transitive: depth=3 reaches N more symbols.` points at `depth`; nothing is added
in the other cases. Homonyms reuse the same treatment as `depth` 1: a name declared in more than one type shows the
call's receiver and the type it points at (`via x -> Type`, `-> ?` when undecided) and is listed in a final `NOTE:`
line; no link is invented.

### What the server adds to def and callers

An exact answer carries what the text of one file cannot say. On `def`:

```
- src/Order.cs:11-11 (struct)
  class Order : Reader
  implements: IComparable (src/IComparable.cs:3)
  inherits: Base (src/Base.cs:1)
  members (3):
  - 12: ID (field) int
  - 13: Total (field) int
  - 16: Sum (method) int Sum()
```

- the **signature** is the server's (hover), which is where a resolved generic or a return type the index does
  not keep shows up; a type's hover is the whole body, and a body is not a signature, so the indexed header is
  kept instead;
- `implements` / `inherits` / `derived` / `extends` name the relation **in the direction the question was
  asked**: an interface answers who implements it (`implemented by:`), a type answers what it implements and
  what it extends, a member answers which contract it fulfils (its base, in C#). In Go none of that is written
  anywhere — a type satisfies an interface structurally — and the concrete type that satisfies it does not
  reference the interface's name at all, so without this line "who implements Reader" would answer with the one
  parameter that mentions it;
- `members (N):` lists a **type** as one line per member, no body, capped at 24 (`relMaxMembers`), each with the
  line it starts at, the name, the kind and the detail. Methods declared outside the type (Go) are attached to it,
  because that is what they are.

`callers` and `impact` carry only the relation, above the references. Every target is `Name (file:line)`; a
relation line lists at most 8 (`relMaxNames`) and then `... +N`. Relations are a complement, never a reason to
fail: a server that answers references but not the relation still gives an exact list. `callers` of an interface
answers with `implemented by:` first, since that is what someone about to change the contract needs.

The same graph carries the **documentation**: inside an indexed repository, every Markdown file becomes
documentation nodes and edges, by deterministic rule, never by the model. Each section (a heading) is a node, named by
the heading path, and the references a section makes are edges: `[x](other.md#anchor)` links between Markdown files
(resolved relative to the file's own folder, GitHub-style, anchor included), `[[Wiki links]]`, and backticked
`` `Name` `` / `` `pkg.Name()` `` / `` `path/file.go` `` that match **exactly** an indexed symbol name or an existing
file of the repository (never fuzzy; front matter is ignored, and inside a code fence only paths count, since a bare
identifier there is sample code). So without opening a single Markdown:

- `def` and `callers` end with `docs: README.md#install, _docs/api.md (+K)` — up to 3 documents, each with the section
  that holds the mention. That answers "where is this documented" in the same call that answers "where is it declared".
- `impact` of a file shows the same line for the documents that cite it (link, wiki link or path mention).
- `index` counts the Markdown it tracked, and `code_outline` reports the broken references of one file.

The line is absent — and the rest of the output is byte for byte what it always was — in a repository with no
documentation, so nothing grows for the code-only case. Only Markdown inside a Git repository is indexed
(`node_modules`, `vendor`, `.git` and the like are skipped, as is any file over 512 KB).

Freshness is cheap but never lossy: the recorded repository state (HEAD + `git status --porcelain`) carries the
Markdown count of the pass that produced it, so a call that finds the index already up to date still answers with the
`docs:` line and the tracked-document count without scanning anything, and a document edge is only ever written by a
pass that had the whole index in front of it — the files a budget cut left out are announced, never indexed halfway.
`index` (full rebuild) drops the recorded state along with the rows, so an empty index cannot pass as up to date.

Go is parsed with `go/ast`; C/C++, C#, TypeScript/JavaScript, Python, shell and PowerShell with line scanners,
matched by name without type resolution (in C/C++: function and method definitions, classes, structs, unions,
enums, `#define`, `using` aliases, `#include`; prototypes are not declarations).

Front-end, all deterministic (no LLM, no guessing at a value that is not a literal):

- in `.tsx`/`.jsx`, a function or arrow whose name starts with a capital and whose body returns JSX is a `component`
  (its signature carries the props when they are typed), and `useX` with a capital X is a `hook`; `<Card />`,
  `<Card.Sub />` and `<My.Ns.Widget />`
  create a `renders` edge towards the last name segment, so `callers` of `Card` lists what renders it next to what
  imports it. `className="a b"` / `className={'a b'}` (and `class=` in Preact/SVG) reference the classes; a template
  literal or a `clsx()` call is skipped rather than invented;
- in `.css`/`.scss`/`.less`, every `.class` and `#id` selector is a node (line range = its block) and every `--x` is a
  `css_var` node; `var(--x)` and `className` are the edges. A stylesheet name is global (it is bound by `<link>` or by
  `className`, not by an import), so `impact` of the file lists whoever uses a class it declares even without
  importing it, and `callers` of `card` lists the files using that class;
- in `.html`/`.htm`, ids are nodes, classes are references, and a local `<script src>`/`<link href>` is a `html_ref`
  edge towards the file (URL, `data:` and interpolated paths are dropped), so `impact` of `app.js` names the page
  that loads it;
- in `.razor`/`.cshtml`, `<Component>` tags give `renders` edges (Blazor) and the `@code`/`@functions` body is ignored
  as markup (it is C#).

`node_modules`, `dist`, `build`, `vendor`, `.next` and `*.min.*` are never indexed.

For C# (`.cs`, plus `.csproj` and `.sln`) the scanner reads namespaces (file-scoped and block), classes, structs,
interfaces, records, enums and delegates, methods, constructors, properties and `const`/`static` fields; attributes,
comments, strings (verbatim, raw and interpolated) and expression bodies yield no symbol. Edges: `Name(`/`.Name(`,
`new Type(`, `using` and the base list (`: Base, IFoo` → `inherits`/`implements`). `<ProjectReference>` (and each
`Project` of a `.sln`) is a `projref` edge, so `impact` on a project lists the projects that depend on it. `bin/`,
`obj/`, `*.g.cs`, `*.Designer.cs` and `*.AssemblyInfo.cs` stay out of the index. `impact` on a `.cs` file only brings
a file from another namespace when that file has a `using` for it — the same namespace always counts.

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
`handoff`, `config`, `token`, `setup`, `doctor`, `lsp doctor`, `daemon <status|start|stop>`, `version`. Run with no arguments
(or `mcp`) it is the MCP server.

`fastexec graph callers <symbol> [repo_path] [depth=N]` is the same query as the tool, including `depth` > 1.
