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
| duration, and for `exec` the time the command itself took (`cmd_ms`, -1 when not measured) | `42` ms, `30` ms |
| bytes in and out, estimated tokens (bytes / 3.5) | `100`, `250` |
| success, exit code, truncated, empty result | |
| command summary | the command line, cleaned, cut at 120 characters |
| error message | the first error line, cut at 300 characters |

File contents and command output are never stored.

**Invariant:** every new row is written with text that is valid UTF-8 and has recognizable credentials masked
(`[REDACTED]`, `[REDACTED:token]`, `[REDACTED:private-key]`). Text in Windows-1252 or in UTF-16 (with a BOM) is converted
to UTF-8 first. Masking runs before truncation, so a secret cut at the limit is still masked, and truncation counts
runes, never splitting a character. The same masking (`corpus.Sanitize`) is applied when reading old rows in `fastexec stats` (largest returns, latest
errors, per-tool tables and the savings block), in the savings report, in `stats --measure` (call text, mark templates)
and in handoff, so rows written before this change are shown masked too. The database itself is not rewritten.

Masking covers: bearer/basic tokens, known token shapes (GitHub, GitLab, Slack, AWS, Google, Stripe, npm, OpenAI-style
keys, JWTs), PEM private key blocks (including a truncated one), CLI flags such as `--password x` or `--token=x`,
`key=value` / `key: value` / JSON pairs whose key names a credential (`password`, `secret`, `token`, `cookie`,
`credential`, `authorization`, `api_key`, `*_key`, `.key`, `-key`), passwords or tokens in URLs, and the device key
literal of TinyTuya-style constructors (see below).

**Device key pattern.** The third quoted argument of `Device(...)`, `BulbDevice(...)` and `OutletDevice(...)`, as in
`Device("id", "address", "key")`, is masked when it is a quoted literal of 8 or more characters. Any other form is not
matched: a variable, a short or unquoted value, or another library's call. This is a narrow rule chosen to avoid
masking ordinary code; it is written from the documented positional form, and was not checked against real rows.

**Limits (defense-in-depth, not a guarantee):**
- A secret with no recognizable shape and no credential-like key name is not masked. Unknown formats are not caught.
- Key names are matched by name; the device pattern covers only the positional constructor form above.
- Old rows are masked on read, not scrubbed. Anyone with the database file can still read what was written before.
- The masking of read-side text does not cover handoff save, notes and pending items (`handoff save/notes/pending`),
  which still write their own text unmasked. This is a known gap, not handled by this change.
- Do not share the database file as if it were clean.

A command line can still contain a secret you typed into it (a password in a `curl` URL, for example); the masking
above reduces that risk but does not remove it.

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
fastexec stats --help       # short usage
```

The report shows totals, success rate, latency (average and p95), traffic and estimated tokens, a per-tool table,
the largest returns, the latest errors and a short `SAVINGS` block for the same period (tokens read, tokens saved,
"without FastExec" and the percentage — the same numbers as the savings report, see `docs/savings.md`). The database
is plain SQLite: the `sql` module or any SQLite tool can query the `audit` table directly.

`handoff` reads it to build session briefings (files touched, failed commands).

### Measurement report

```
fastexec stats --measure --today    # also 24h, 7d ...
```

Five short sections, computed from the same database (code in `internal/measure`):

1. **Savings, measured vs estimated.** *Measured* is text FastExec removed from an output the call actually
   produced (`clean`, `cut`, `group` for regrouped listings, `dedup` for repeated output, `readcap` for big reads). *Estimate* is what a saving assumes the model would have done without FastExec
   (`slice`, `retry`, `timeout`, `schema`); it was not observed. An unknown category counts as an estimate.
2. **Harm signals**, one detector each: `retry_after_cut` (the same call repeated within 3 calls after a cut
   output), `raw_flag` (`--raw` used), `repeated_hint` (the same hint shown 3 or more times in one channel).
3. **Use per feature**: calls and distinct channels per tool and per notice type. Known notice types with no use
   show as zero.
4. **Overhead of `exec`**: call time minus command time (median, p95, max). Calls recorded before `cmd_ms`
   existed are counted apart.
5. **Tokens per channel** (one channel = one MCP process). The client does not send a conversation id, so the
   channel is the closest unit available; the report says so.

Adding a saving category, a harm detector or a notice type is a registration in `internal/measure`; the report
code does not change.

## Output corpus (off by default)

For comparing filters and other tools offline, with no model calls, `exec` can record real outputs. It is separate
from telemetry and stays on this computer.

- Turn on with `"corpus": true` in `~/.fastexec/config.json` (the reliable way: the background process does not always
  inherit the AI client's environment) or `FASTEXEC_CORPUS=1` in the environment. `FASTEXEC_CORPUS=0` turns it off over
  the file. Nothing is recorded otherwise. Restart the background process after changing it: `fastexec daemon-stop`.
- Each call that ran a command becomes one JSON file in the `corpus` data folder (`FASTEXEC_CORPUS_DIR` overrides):
  host, command line, exit code, command time, the raw output and what FastExec returned.
- Secrets are masked before writing (bearer and API tokens, `password=`-style values, URL passwords, private key
  blocks, and the device key literal described above). Masking is a safety net, not a guarantee: do not share the
  folder. The size cut of a sample is made by bytes, so the last character of a truncated sample may be incomplete.
- Retention has two tracks, because only big outputs (raw output of 10 KB or more) are changed by a filter. Big
  samples are kept 14 days (`FASTEXEC_CORPUS_BIG_TTL_H`) and are the last to go when the folder is full; small ones
  are kept 72 h (`FASTEXEC_CORPUS_TTL_H`), at most 500 of them (`FASTEXEC_CORPUS_MAX_SMALL`). The whole folder stays under
  50 MB (`FASTEXEC_CORPUS_MAX_MB`) and each call keeps at most 256 KB of raw output. Old samples are removed at most
  every 10 minutes while the background process runs. Files are readable only by the user.

`fastexec stats --corpus` shows how many samples the corpus has per command family (git, build_test, listagem, busca,
logs, ...) against the targets for the comparison (200 in all, 20 per family). It looks inside wrappers such as
`bash -lc "..."`, `powershell -Command "..."` and `cd x && ...` to find the program that really ran, leaves out calls
about the corpus itself and counts a repeated command with the same output once. Adding a wrapper or a family is a
table entry in `internal/corpus/family.go`.

## Turning it off

Either:

- `"telemetry": false` in `~/.fastexec/config.json`, or
- `FASTEXEC_TELEMETRY=0` in the environment of the AI client.

Nothing is recorded from then on. Existing data stays until you delete it: `fastexec stats --reset` or remove the
`telemetry` folder.
