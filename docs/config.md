# Configuration

FastExec works with no configuration: only the host `local`, data in `~/.fastexec`, telemetry on.
Everything else comes from one JSON file, read once when the process starts. Nothing in it costs tokens except
the host names, which appear in the tool descriptions.

## Where

| What | Default | Override |
|---|---|---|
| FastExec folder | `~/.fastexec` | `FASTEXEC_HOME` |
| Config file | `<FastExec folder>/config.json` | `FASTEXEC_CONFIG` |

`~` is your user profile (`C:\Users\<you>` on Windows). The folder is deliberately outside `AppData`: the Claude
Desktop from the Microsoft Store gives each child process a private copy of `AppData`, so files written there by
one client are invisible to the others.

`fastexec config` shows the effective configuration; `fastexec config example` prints a sample;
`fastexec config init` writes the sample if there is no file yet.

## File

```json
{
  "default_host": "server",
  "hosts": {
    "local":  { "aliases": ["my-pc"] },
    "server": { "addr": "192.0.2.10:9142", "cwd": "/home/me/projects", "aliases": ["srv"] },
    "router": { "addr": "192.0.2.1" },
    "pi":     { "addr": "192.0.2.20", "token": "3f9c...e1" },
    "work":   { "ssh": "work-vm", "hidden": true }
  },
  "data_dir": "~/fastexec-data",
  "handoff_dir": "~/notes/handoffs",
  "telemetry": true
}
```

| Key | Meaning |
|---|---|
| `default_host` | Host used when a call names none. Default `local`. An `exec`/`job` call with no `host` that carries a Windows drive path runs on `local` instead, with a `[FastExec Auto-Remediation]` note in the output. |
| `hosts.<name>.addr` | `ip` or `ip:port` of a `server-exec` daemon (port 9142 when omitted). |
| `hosts.<name>.ssh` | An alias from your `~/.ssh/config`; commands run through `ssh -o BatchMode=yes`. Slower than `addr`. |
| `hosts.<name>.cwd` | Working directory for commands on that host. Relative `cwd` arguments resolve from it, and a redundant `cd <cwd> &&` prefix is removed automatically. |
| `hosts.<name>.aliases` | Other names for the same host. |
| `hosts.<name>.hidden` | Keep the host out of the tool descriptions (fewer tokens); it still works by name. |
| `hosts.<name>.token` | Token the daemon requires (see [server.md](server.md)). Only with `addr`, up to 256 characters. |
| `hosts.local.aliases` | Extra names for this machine (`localhost`, `127.0.0.1` and the computer name always work). |
| `data_dir` | Base folder for `telemetry/`, `jobs/`, `sessions/`, `logs/` and `handoff/`. Default: the FastExec folder. |
| `handoff_dir` | Folder for handoff notes. Default `<data_dir>/handoff`. |
| `telemetry` | `false` stops recording tool calls. Default `true`. |

Each remote host needs exactly one of `addr` or `ssh`. Relative paths in the file resolve from the file's folder;
`~` expands to your profile. Unknown keys are errors.

A host can also be reached without a config entry: `ssh:<alias>`, an IP, or `ip:port`.

## Environment variables

They win over the file.

| Variable | Effect |
|---|---|
| `FASTEXEC_HOME` | FastExec folder (default `~/.fastexec`) |
| `FASTEXEC_CONFIG` | Config file path |
| `FASTEXEC_DEFAULT_HOST` | Default host |
| `FASTEXEC_DATA_DIR` | Data folder |
| `FASTEXEC_TELEMETRY` | `0`, `off`, `false` or `no` turns telemetry off; anything else turns it on |
| `FASTEXEC_NO_SEARCH_GUARD` | Any value disables the search and broad-root guards |
| `FASTEXEC_NO_SX` | Any value forces the older remote protocol (debugging) |
| `FASTEXEC_DEBUG` | `1` writes debug lines to `<data_dir>/logs/fastexec.log` |

## When the file is wrong

FastExec never stops because of the config. An invalid file (bad JSON, unknown key, a host with both `addr` and
`ssh`, a name used twice, a `default_host` that does not exist) is ignored as a whole: only `local` works until it
is fixed, and `fastexec config` and `fastexec doctor` show the error.
