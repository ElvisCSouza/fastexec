# Notes for AI agents working on this repository

Read [CONTRIBUTING.md](CONTRIBUTING.md) first: layout, tests, golden files and how to add a tool.

- Run `go vet ./...` and `go test ./...` before finishing. If a golden test fails, decide whether the change to
  what the model sees is intended; only then regenerate with `-update`, and mention it.
- Tool schemas live in `internal/mcp/tool_schemas.go` as data. Keep descriptions short: they are sent with every
  request.
- Never put hostnames, IP addresses, user names or local paths in code, tests or docs. Tests use the sample config
  (`config.Sample()`, addresses in 192.0.2.0/24) through `config.Isolate()`.
- Comments may be in Portuguese (history of the project); anything a user or a model reads is English.
- The remote daemon is Zig 0.13 in `server/`; build with `server/build.sh`.
