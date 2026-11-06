# Remote daemon: server-exec

`server-exec` is the small daemon (Zig, 130-370 KB, static) that runs commands for FastExec on other machines.
It listens on TCP (port 9142 by default), speaks a binary protocol, and keeps connections open so each command
costs one round trip plus the time to start the process.

## Build

Zig 0.13:

```
cd server
./build.sh                     # every target
./build.sh linux-arm64         # or just some
```

| Target | For |
|---|---|
| `linux-amd64` | servers, VMs, WSL |
| `linux-arm64` | Raspberry Pi 4/5, ARM64 routers (OpenWrt) |
| `linux-armv7` | 32-bit ARM hard-float (older routers, TV boxes) |
| `windows-amd64` | Windows (commands run in Windows PowerShell) |

Output goes to `server/dist/`. The release files include prebuilt daemons when built with Zig available.

## Run

```
server-exec [--port N] [--bind ADDR] [--token-file PATH]
```

| Option | Environment | Default |
|---|---|---|
| `--port` | `SERVER_EXEC_PORT` | `9142` |
| `--bind` | `SERVER_EXEC_BIND` | `0.0.0.0` (all interfaces); use `127.0.0.1` or a VPN address to narrow it |
| `--token-file` | `SERVER_EXEC_TOKEN_FILE` (or the token itself in `SERVER_EXEC_TOKEN`) | no token |

The token is never taken from the command line, where other users could see it in the process list.

### Linux (systemd)

```ini
# /etc/systemd/system/server-exec.service
[Unit]
Description=FastExec remote daemon
After=network-online.target

[Service]
User=me
WorkingDirectory=/home/me
ExecStart=/usr/local/bin/server-exec --token-file /home/me/.config/server-exec/token
Restart=always

[Install]
WantedBy=multi-user.target
```

Commands run as that user, with its permissions, in `WorkingDirectory` unless the client sends a `cwd` (the host's
`cwd` in the client config).

### Windows

Register a scheduled task that starts `server-exec.exe` at boot and restarts it if it stops, and open the port in
the firewall only for the machines that use it. Shell lines run in Windows PowerShell (passed with
`-EncodedCommand`, so no quoting rules apply) with UTF-8 output; `argv`, `cwd` and `input` work as on Linux.

## Security

**Anyone who can reach the port can run commands as the daemon's user.** Choose at least one:

- a token (below);
- `--bind` to a private interface (loopback, VPN);
- a firewall rule limited to your client machines.

The daemon prints a warning at start when it runs without a token on a non-loopback address. Traffic is not
encrypted; across untrusted networks, put it inside a VPN (WireGuard, Tailscale) or an SSH tunnel.

### Tokens

1. `fastexec token` prints a new random token (32 bytes, hex).
2. Save it in a file only the daemon's user can read (`chmod 600`) and start the daemon with `--token-file`.
3. Put the same value in the host's `token` in the client config.

The token is checked once per connection, in constant time. The client sends it in the same packet as the first
command and the daemon answers both together, so a token costs no extra round trip, and pooled connections pay
nothing. Measured on the same machine and binary: no difference within noise.

| Situation | What the model sees |
|---|---|
| wrong token | `... refused the token (wrong token)` |
| daemon wants a token, client has none | `... requires a token: set 'token' for this host` |
| client has a token, daemon is too old for tokens | `... closed the connection at auth` |

## Protocol

All integers little-endian. Each message starts with a 2-byte magic.

| Magic | Name | Request | Response |
|---|---|---|---|
| `SA` 0x5341 | auth | `len u16, token` | `magic, status u16` (0 ok, 1 refused, 2 token required) |
| `SX` 0x5358 | exec | `flags u16, cwd_len u16, argc u16, body_len u32, stdin_len u32, cwd, body, stdin` | `magic, flags u16, exit i32, out_len u32, err_len u32, stdout, stderr` |
| `SE` 0x5345 | exec (legacy) | `len u32, shell line` | `magic, exit i32, out_len u32, stdout, err_len u32, stderr` |
| `SP` 0x5350 | put file | `path_len u16, size u64, path, bytes` | `magic, status i32, msg_len u32, msg` |
| `SG` 0x5347 | get file | `path_len u16, path` | `magic, status i32, size u64, msg_len u32, msg, bytes` |

`SX` flag bit 0 (direct): the body is `argc` NUL-terminated arguments run without a shell; otherwise the body is a
shell line (`bash -c`, `sh -c` where bash is missing, Windows PowerShell on Windows). `SX` runs with a non-interactive environment (`CI=1`,
`TERM=dumb`, `PAGER=cat`, `GIT_TERMINAL_PROMPT=0`, ...). If the client disconnects while a command runs, the
command is killed. Files are written to a temporary name and renamed into place. Idle connections close after
10 minutes.
