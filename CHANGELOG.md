# Changelog

## Unreleased

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
