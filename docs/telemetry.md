# Telemetry

FastExec records every tool call in a local SQLite database so you can see where time and tokens go and which
commands keep failing. It is on by default because that is where most of its tuning came from.

**Nothing leaves your machine.** There is no network code in the telemetry: it is a file on your disk.

## What is recorded

One row per tool call:

| Field | Example |
|---|---|
| time, session id | `2026-11-06 21:58:19`, one id per FastExec process |
| tool, host | `exec`, `server` |
| duration | `42` ms |
| bytes in and out, estimated tokens (bytes / 3.5) | `100`, `250` |
| success, exit code, truncated, empty result | |
| command summary | the command line, cleaned, cut at 120 characters |
| error message | the first error line, cut at 300 characters |

File contents and command output are never stored. Be aware that a command line can contain a secret you typed
into it (a password in a `curl` URL, for example); it ends up in the summary like any other text.

## Where

`<data_dir>/telemetry/fastexec.db` (default `~/.fastexec/telemetry/fastexec.db`), in WAL mode. Writes happen on
a background queue and never slow a tool call; if the queue is full a row is dropped and counted. Problems
opening or writing the database go to `telemetry-errors.log` next to it and show at the end of `fastexec stats`.

## Reading it

```
fastexec stats              # all time
fastexec stats 24h          # last 24 hours (also 7d, 30d...)
fastexec stats --today
fastexec stats --errors     # only failures
fastexec stats --tools      # only the per-tool table
```

The report shows totals, success rate, latency (average and p95), traffic and estimated tokens, a per-tool table,
the largest returns and the latest errors. The database is plain SQLite: the `sql` module or any SQLite tool can
query the `audit` table directly.

`handoff` reads it to build session briefings (files touched, failed commands).

## Turning it off

Either:

- `"telemetry": false` in `~/.fastexec/config.json`, or
- `FASTEXEC_TELEMETRY=0` in the environment of the AI client.

Nothing is recorded from then on. Existing data stays until you delete it: `fastexec stats --reset` or remove the
`telemetry` folder.
