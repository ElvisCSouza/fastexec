# Changelog

## Unreleased

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
