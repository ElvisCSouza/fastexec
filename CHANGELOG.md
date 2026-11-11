# Changelog

## Unreleased

- Installer with a live window: progress, log, checks, FastExec-only mode and Undo.
- `read_file` decodes Windows-1252 and UTF-16 text; `edit_file` writes it back in the original encoding.
- `code_graph` callers: names the calling class and separates same-name methods of different types.
- File operations and session notes moved to on-demand modules, so the default tool list is smaller.
- On Windows, PowerShell command lines with escaped quotes run without a `cmd.exe` layer in between.
