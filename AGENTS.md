# Notes for AI agents working on this repository

Read [CONTRIBUTING.md](CONTRIBUTING.md) first: layout, tests, golden files and how to add a tool.

- Run `go vet ./...` and `go test ./...` before finishing. If a golden test fails, decide whether the change to
  what the model sees is intended; only then regenerate with `-update`, and mention it.
- Tool schemas live in `internal/mcp/tool_schemas.go` as data. Keep descriptions short: they are sent with every
  request.
- Never put hostnames, IP addresses, user names or local paths in code, tests or docs. Tests use the sample config
  (`config.Sample()`, addresses in 192.0.2.0/24) through `config.Isolate()`.
- Comments may be in Portuguese (history of the project); anything a user or a model reads is English.
- Every automatic change FastExec makes to a command or its output (a sleep run natively, a `cat` turned into a read,
  output cut to head + tail) must say so in the result with a `[FastExec ...]` line, and name the next step when
  something was withheld. The model must never have to guess what was done for it. Prefer an automatic default over a
  new parameter the model has to remember to use.
- Size caps are for what the model reads. Code that reads files for its own work (`edit_file`, `code_outline`) calls
  `handleReadFileInner`, never `HandleReadFile`, so a cap can never truncate a file that is then written back.
- When output is cut, keep the full text (see `spill.go`) or do not cut.
- Anything that withholds content (outline instead of text, cut, incremental wait) keys its escape by the request,
  never by `identity()`: several conversations can share one MCP channel. A shared channel may cause more text,
  never less. `identity()` is only for hints.
- An exec that only reads whole files (also wrapped in `pwsh -Command` / `bash -c`, with `cd` and separators) is
  answered through `HandleReadFile` (`read_script.go`), so graph first and the cap apply as in `read_file`. Anything
  else in the command (pipe, redirect, variable, line slice, another program) runs in the shell unchanged.
- `write_file` overwriting a local file it mostly kept only warns (`rewrite_hint.go`); the file is always written.
- The remote daemon is Zig 0.13 in `server/`; build with `server/build.sh`.
