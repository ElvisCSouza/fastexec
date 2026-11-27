# Changelog

## Unreleased

## v0.3.0 - 2026-11-27

- **Lossless structured shrinking (`shrink`, Measured):** automatically compacts homogeneous JSON arrays (≥ 4 scalar objects) into pipe-delimited tables, strips `(use "git ...")` boilerplate from `git status`, removes redundant `index`/`diff --git` headers from `git diff`, compacts verbose `git log` commit headers, shortens 64-char IDs in `docker ps`/`images`, drops 100%-empty CSV columns, and folds consecutive identical-template log bursts while preserving errors/warnings and code/diffs.
- **Built-in BM25 section search (`read_file(query=...)`):** ranks Markdown sections (`#`/`##`/`###`) or 30-line blocks with BM25 (including accent folding) within an 8 KB budget, returning exact line ranges; `read_cap` footers now suggest `query='keywords'` directly.
- **Symbol-relative editing (`edit_file(symbol=..., position='before'|'after')`):** inserts new functions, methods or fields directly before or after an existing AST declaration without sending surrounding context blocks.
- **Shell file-read guard expanded:** `powershell -Command "Get-Content '...'"` and `cmd /c type "..."` pure file reads are now unwrapped and routed to native `read_file` (with `read_cap` and `read_dedup`), leaving pipelines untouched.
- **Optional embedded concise-output mode (`concise_output`):** when enabled in `~/.fastexec/config.json` (or `FASTEXEC_CONCISE_OUTPUT=1`), FastExec emits terse response rules in the standard MCP `initialize` `instructions` field across all MCP clients (Claude Desktop, Claude Code, Antigravity, OpenCode, Cursor) without external plugins.
- **More accurate savings panel:** prevents `CatSlice` double-counting when opening spill files, exposes `Measured` vs `Estimate` split and `Cut spills` recovery summary in `SavingsReport` and the monitor UI, adds the `Structured shrink` category, and shows an optional separate Caveman output card when `~/.claude/.caveman-history.jsonl` is present.

## v0.2.0 - 2026-11-27

- **Recoverable output cuts with secret redaction.** Truncated command/tool outputs now persist the redacted full text to a local spill file and return the exact `read_file` call (line range or regex pattern) to inspect any omitted section without re-running the command.
- **Full background job lifecycle on Windows and Linux.** `job` adds idempotent starts, race-safe local job metadata, byte-safe log cursors and replay, Linux `systemd` lifecycle support, and 12-second auto-wait for short commands. Long pure pauses (`sleep`) and silent loopback ping waits are promoted to background jobs automatically.
- **Session-wide read and command deduplication.** Identical repeated `read_file` and `exec` outputs (including spilled large outputs) are replaced with a compact stub for the entire session, cutting redundant context growth.
- **Multi-language syntax checking after edits.** Post-edit syntax checks now cover JSON (with duplicate-key and format hints), TypeScript (`tsc --noEmit` plus project-level `tsc` fallback when no language server is attached), JavaScript, Zig, Shell, and Python (best-effort by installed toolchain), with column pointers.
- **Better `code_graph` and actionable edit validation.** `code_graph` exposes resolution basis and separates interface implementers; `edit_file` validates parameters up front and returns actionable refusal diagnostics instead of opaque misses.
- **Privacy, telemetry and MCP metadata.** Credentials in telemetry, handoffs and spill files are masked before persistence; UTF-8 boundaries are preserved on cuts; `file_ops` `list` caps at 400 entries; `handoff save` returns a short confirmation; MCP `initialize` reports the actual build version; telemetry tracks build, client, invocation identity and explicit outcomes, plus switchback A/B measurement (`stats --measure`).
- **Local SQLite and Windows transport fixes.** SQLite queries on local Windows run in-process; drive paths without an explicit host route locally; older Windows `server-exec` receives native PowerShell lines.

## v0.1.2 - 2026-11-13

- **Tests that cover the file.** The first edit of a code file ends with one line naming the test files that cover it and
  the command that runs only them (`dotnet test --filter ...`), so a model does not run the whole suite or write its own
  harness. Found by file name; when no test carries the name, FastExec follows who uses the type (code index first, a
  bounded text scan otherwise). Same language only, and a generic name such as `main` or `index` matches only in the same
  folder. Silent when nothing is found.
- **Numbered slices.** `read_file` with `start_line`/`end_line`, or `ranges` of at most 200 lines, returns each line
  numbered, so an answer such as `file.cs:305` quotes the line it was shown instead of counting from the top of the slice.
- **`code_outline` suggests names.** Asking for a symbol that is declared nowhere lists up to five declarations of the
  index with a similar name (`LoggerLevelOverrideMap` points at `LevelOverrideMap`) instead of only "no declaration".
- **Rename by script.** A `sed`/`perl` command that renames a member (`s/Old(/New(/g`) is answered with the language
  server's rename plan, and a rename reports `✓ compila` only when every changed code file was checked by the compiler.
- **Windows quoting.** `cmd /c` command lines with quotes (for example `findstr /C:"x"`) reach `cmd.exe` untouched, which
  removes a false "no match".
- **Daemon on Windows.** `server-exec` for Windows builds again with Zig 0.13 (a type that only exists on POSIX broke
  the build since 8 Nov); the release now ships `server-exec-windows-amd64.exe` next to the Linux ones.
- C# benchmark follow-up in `benchmark/` (see the README there).

## v0.1.1 - 2026-11-12

- Exact code navigation through language servers (Go and C#): definitions, callers, impact of a change and rename, with a clear fallback to the by-name index when the project is not loaded. `fastexec lsp doctor` checks the .NET SDK, `global.json` and restore; `fastexec config set dotnet_sdk <folder>` points to a portable SDK.
- Build and test output (`go`, `dotnet`, `npm`, `cargo`, `tsc`) is compacted: errors and warnings stay, progress and passing tests go; repeated warnings collapse into a count (also for localized `dotnet` messages).
- Folder listings leave out `.git`, `obj` and `bin` with a note; `edit_file` answers with a short summary instead of echoing the file.
- The code index is built in the background; the savings window shows the cumulative curve.
- Benchmark method and raw results in `benchmark/`.
- Installer with a live window: progress, log, checks, FastExec-only mode and Undo.
- `read_file` decodes Windows-1252 and UTF-16 text; `edit_file` writes it back in the original encoding.
- `code_graph` callers: names the calling class and separates same-name methods of different types.
- File operations and session notes moved to on-demand modules, so the default tool list is smaller.
- On Windows, PowerShell command lines with escaped quotes run without a `cmd.exe` layer in between.
