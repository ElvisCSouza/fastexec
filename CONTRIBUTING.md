# Contributing

## Layout

```
cmd/fastexec/        main: just cli.Main()
cli/                 entry point and CLI subcommands (setup, doctor, config, exec, stats, graph...); public API
internal/
  config/            ~/.fastexec/config.json: hosts, default host, data folder, telemetry switch
  mcp/               JSON-RPC server, tool schemas (data only) and the name -> handler table
  daemon/            resident local daemon (127.0.0.1:9146) that runs the tools; replaced when the binary changes
  tools/             the tools: exec and its guards, files, job, code navigation, handoff, modules
  transport/         where commands run: local (Job Objects on Windows), server-exec (SX/SE, pool, auth), ssh
  telemetry/         SQLite audit of tool calls and the stats report
  graph/, codescan/  incremental code graph and the per-language line scanners
  jsonedit/          order-preserving JSON edits (setup)
  analytics/         DuckDB sessions for the sql module
  env/, logger/, golden/
server/              server-exec, the remote daemon (Zig 0.13)
tools/release/       release builder (go run ./tools/release -version vX.Y.Z)
docs/                user documentation
```

## Build and test

```
go build ./cmd/fastexec
go vet ./...
go test ./...
```

- Some tests need `node` and, on Windows, `pwsh` on PATH (process-tree tests).
- Each package's `TestMain` calls `config.Isolate()`: tests use a temporary data folder and a sample config
  (hosts in 192.0.2.0/24), never your real `~/.fastexec`.
- Integration tests against a real daemon run only when asked: `FASTEXEC_TEST_HOST=<host from your config>`
  (tools) or `FASTEXEC_TEST_ADDR=ip:port` (transport). Benchmarks: `FASTEXEC_BENCH=1`.

### Golden files

What the model sees is locked by golden files under `testdata/golden`: the `tools/list` schemas (with and
without a config), the CLI usage messages and host resolution. A change that alters them shows up as a diff;
regenerate on purpose with `-update`:

```
go test ./internal/mcp -run Golden -update
go test ./cli -run Golden -update
go test ./internal/transport -run Golden -update
```

Review the diff: every byte in the schemas is paid by every model on every request.

## Adding things

- **A tool:** handler in `internal/tools`, one line in `internal/mcp/tool_handlers.go`, the schema in
  `internal/mcp/tool_schemas.go`. Use `{hosts}` (or `{hosts_cd}`) in `host` descriptions; it is filled from the
  config. Prefer a module (`tools.RegisterModule`) for anything not needed in most sessions.
- **A CLI subcommand:** a function in `cli/` and one line in the `commands` table.
- **Your own commands without forking:** create a module that imports `homelab/fastexec/cli`, register them and
  call `Main`:

  ```go
  package main

  import "homelab/fastexec/cli"

  func main() {
      cli.Register("deploy", runDeploy)
      cli.Main()
  }
  ```

  Everything else, including the MCP server and all optimizations, stays identical.
- **server-exec:** `server/main.zig`; keep the protocol table in `docs/server.md` in sync and keep old clients
  working (new messages must be optional).

## Principles

- Deterministic first: guards, corrections and output cleaning are rules, not model calls.
- Every token in schemas and outputs is paid on every call; measure before adding text.
- Nothing personal or machine-specific in code, tests or docs: hosts, paths and names come from the config.
- User-facing text (schemas, messages, docs) is English.
