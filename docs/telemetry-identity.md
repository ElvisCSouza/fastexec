# telemetry identity — spec

> For the chat to discuss and write directives without opening the code. Natural language, no pasted code.
> Maintainer: the developer who changes the behaviour, the contract or an invariant of build/client identity.

## Responsabilidade

Records, on each row of the `audit` table, which FastExec build wrote the row, which process mode ran it, and which
MCP client made the call, split into name and pid. It also lets `fastexec stats` isolate one build or one client.
It does NOT record provider usage, model, token or cost data, and it does NOT backfill rows written before these
columns existed.

## Fluxo

1. `cli.Main` calls `telemetry.SetBuildVersion(cli.Version)` and `telemetry.SetInvokeMode("cli")`. The MCP branch sets
   `"proxy"`; `daemon.RunDaemon` sets `"daemon"`. `mcp.NewServer` captures `telemetry.BuildVersion()` right after, so
   the MCP `initialize` announces the same version the telemetry rows record.
2. The MCP `initialize` request carries `clientInfo.name` and `clientInfo.version`. The server keeps the sanitized name
   (through the existing caller id) and the sanitized version, capped at 32 characters.
3. On `tools/call` the proxy injects two hidden arguments: `_caller` (`name#pid`) and `_client_version`. Existing hidden
   values are not replaced.
4. The daemon receives them through `daemon.CallTool`; the in-process fallback is `mcp.DispatchTool`. Hidden keys are
   treated as metadata by the argument normalizer: the model never sees them, they are excluded from the args hash
   and from bytes_in, and a typed handler that does not declare them simply ignores them.
5. `telemetry.Record` calls `withIdentity`, which fills the build fields, splits `Client` into `ClientName` and
   `ClientPID` (split at the last `#`), and sets `ClientVersion`.
6. `writeEntry` stores the row. Every unknown value is written as an explicit sentinel, never guessed.
7. `fastexec stats --build X` and `--client Y` add a `WHERE` condition built by `periodWhere`. The output starts with a
   `Filter:` line and omits the SAVINGS block.

## Contratos

Columns added to `audit` (all `ALTER TABLE ... ADD COLUMN` with a constant default):

| Column | Type | Default | Source |
|---|---|---|---|
| `app_version` | TEXT NOT NULL | `'unknown'` | `cli.Version` (ldflags), default `dev` |
| `app_build` | TEXT NOT NULL | `'unknown'` | VCS revision from build info: 12 hex chars, plus `+dirty` if modified |
| `client_name` | TEXT NOT NULL | `'unknown'` | `clientInfo.name`, text before the last `#` of `client` |
| `client_pid` | INTEGER NOT NULL | `-1` | number after the last `#` of `client`; -1 when missing, zero, or not numeric |
| `client_version` | TEXT NOT NULL | `'unknown'` | `clientInfo.version`, sanitized, max 32 chars |
| `invoke_mode` | TEXT NOT NULL | `'unknown'` | `proxy`, `daemon`, `cli`, or `unknown` |

Unchanged: `client` keeps the full `name#pid` text.

Hidden MCP arguments: `_caller`, `_conversation`, `_client_version`. They never reach the model and are excluded
from hashing and byte counting; a typed handler that does not declare them ignores them.

Stats flags: `--build <v>` and `--client <name>`, each also accepted as `--flag=value`. A flag with no value exits with
status 2. `--build` matches `app_version = v OR app_build = v`. `--client` matches `client_name = v`. Values are SQL
string literals with single quotes doubled.

## Invariantes

- Unknown is written explicitly: `'unknown'` for text, `-1` for integers. No value is inferred from other fields
  (why: a reader must be able to tell "not sent by the client" from a real value).
- No backfill. Old rows read as unknown. Columns are added with a constant default so the table is not rewritten
  (why: migrations must be safe on a live WAL database).
- `client_name` never contains the `#pid` suffix; `client` is unchanged (why: old queries and the existing `client`
  column keep their meaning).
- Split at the LAST `#` (why: the caller sanitizer removes `#` from names, so only the separator remains; a name such
  as `my#tool` keeps its own `#` only if it ever appears in `client`).
- Hidden arguments are removed before hashing and before byte counting, so the same call from two channels gives the
  same `args_hash` and the same `bytes_in` (why: the telemetry must compare calls, not transports).
- A filter value can never change the SQL structure (why: values come from the command line).
- Default `fastexec stats` output is unchanged: no `Filter:` line, no segment text (why: existing users and the golden
  usage test depend on it).

## Decisões

- Sentinels `unknown` and `-1` instead of NULL — a single explicit value keeps `WHERE` and `GROUP BY` simple, and the
  NOT NULL constraint holds for every row. Discarded: NULL (needs `IS NULL` in every reader).
- Migrations as `ADD COLUMN` with a constant default; `addAuditColumn` treats "duplicate column" as success — a
  second run of the migration is a no-op. Discarded: a schema version table (not needed for additive columns).
- `app_build` comes from build info at runtime, not from ldflags — binaries built without ldflags still get a revision
  when built from a git checkout. `buildRevision()` is not cached: it runs once per recorded row, which is acceptable
  for the call rate; caching with `sync.Once` is an open option.
- `stats --build` matches either `app_version` or `app_build`, so both a release number and a commit prefix work.
- The SAVINGS block is omitted under any build or client filter because `savingsTotalsForStats` ignores the filter.
  Showing it would mislead (it would describe all builds). Discarded: filtering savings (needs a new query path).
- Filter values are built as SQL literals, not bind args, because `periodWhere` returns a string and takes no args.
  Escaping is covered by a test that tries an injection string.
- The MCP `serverInfo.version` is the process identity version, not a fixed number: `NewServer` captures
  `telemetry.BuildVersion()` (injected by `cli.Main` via `SetBuildVersion`), so the initialize answer and the
  `app_version` column come from one source. A hand-built `Server` (tests only) falls back to the `unknown` sentinel.
  Discarded: `mcp` importing `cli` (import cycle) and keeping the old hard-coded `"1.3.0"` (audit attributed behavior
  to the wrong build).

## Limites e dívidas conhecidas

- `client_version` exists only when the MCP client sends `clientInfo.version`. Calls from the CLI have no client, so
  both `client_name` and `client_version` are `unknown` for them.
- Rows from the proxy fallback path (`DispatchTool` with no caller) and the panic and not-found paths must set the
  client explicitly; a path that forgets it writes `unknown`, and the tests cover only the main paths.
- `buildRevision()` reads build info on each `Record`. Measured cost is not recorded in this repo.
- Savings totals are not filterable by build or client.
- Rows written by a build before this change stay unknown forever; there is no backfill by design.

## Mapa

- `internal/telemetry/identity.go` — sentinels, build version and revision, `SplitCaller`, `withIdentity`.
- `internal/telemetry/audit.go` — `AuditEntry` fields, the migration loop, `writeEntry` (37 columns).
- `internal/telemetry/sanitize.go` — applies the corpus sanitizer to the identity text fields.
- `internal/telemetry/hashes.go` — `ArgsHash` removes the hidden arguments.
- `internal/telemetry/stats.go` — `StatsFilter` Build/Client, `periodWhere`, `sqlLiteral`, the `Filter:` line.
- `internal/mcp/caller.go` — `setClient` (name and version), `withClientVersion`, `clientVersionOf`.
- `internal/mcp/server.go` — hidden argument injection on `tools/call`, `DispatchTool` recording sites.
- `cli/cli.go` and `internal/daemon/daemon.go` — `SetBuildVersion` and `SetInvokeMode` calls.
- `cli/commands.go` — `flagValue` and the `stats` flag parsing.
- Tests: `internal/telemetry/identity_test.go`, `internal/mcp/client_version_test.go`,
  `cli/testdata/golden/cli_usage.txt` (stats usage lines).
- User docs: `docs/telemetry.md`.
