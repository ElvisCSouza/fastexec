const std = @import("std");
const builtin = @import("builtin");
const is_windows = builtin.os.tag == .windows;
const net = std.net;
const posix = std.posix;
const fs = std.fs;

const MAGIC_EXEC: u16 = 0x5345; // 'SE' - Server Exec (linha de shell; formato legado, inalterado)
const MAGIC_PUT: u16 = 0x5350; // 'SP' - Server Put (Streaming File Upload)
const MAGIC_GET: u16 = 0x5347; // 'SG' - Server Get (Streaming File Download)
const MAGIC_X: u16 = 0x5358; // 'SX' - Server eXec v2: argv direto (sem shell), cwd e stdin como campos
const MAGIC_AUTH: u16 = 0x5341; // 'SA' - Server Auth: token, uma vez por conexão

// SA, pedido: magic(2) token_len(2) token. Resposta: magic(2) status(2), 0 = aceito; recusado = status 1 e a
// conexão fecha. Pedido sem SA num daemon com token: resposta SA status 2 (token exigido) e a conexão fecha. Com token configurado no daemon, o SA tem de ser a primeira mensagem da conexão; sem token, o SA
// é aceito e ignorado (cliente com token funciona com daemon aberto). O cliente manda o SA colado no primeiro
// pedido, sem esperar a resposta: o custo é zero ida e volta extra, e só na abertura da conexão.
const MAX_TOKEN_LEN: usize = 256;

const DEFAULT_PORT: u16 = 9142;
const MAX_CMD_LEN: usize = 1024 * 1024; // 1MB max command length
const MAX_OUTPUT_LEN: usize = 16 * 1024 * 1024; // 16MB max output
const MAX_STDIN_LEN: usize = 16 * 1024 * 1024; // 16MB max stdin do SX
const STREAM_BUF_SIZE: usize = 64 * 1024; // 64KB stream chunk buffer
const IDLE_TIMEOUT_S: i64 = 600; // conexão sem pedido por 10 min é fechada
const THREAD_STACK: usize = 512 * 1024;

// SX, pedido: magic(2) flags(2) cwd_len(2) argc(2) body_len(4) stdin_len(4) = 16 bytes, depois cwd, body, stdin.
//   flags bit0 (FLAG_DIRECT): body = argc strings terminadas em NUL, executadas sem shell.
//   sem FLAG_DIRECT: body = linha de shell (bash -c; no Windows, PowerShell -EncodedCommand), argc ignorado.
// SX, resposta: magic(2) flags(2) exit(4, com sinal) stdout_len(4) stderr_len(4) = 16 bytes, depois stdout e stderr.
const FLAG_DIRECT: u16 = 1;

const DEFAULT_ENV = [_][2][]const u8{
    .{ "CI", "1" },
    .{ "DEBIAN_FRONTEND", "noninteractive" },
    .{ "PAGER", "cat" },
    .{ "GIT_PAGER", "cat" },
    .{ "GIT_TERMINAL_PROMPT", "0" },
    .{ "TERM", "dumb" },
    .{ "NO_COLOR", "1" },
};

fn getShell() []const u8 {
    if (is_windows) return "powershell.exe";
    if (fs.accessAbsolute("/bin/bash", .{})) |_| {
        return "/bin/bash";
    } else |_| {
        return "/bin/sh";
    }
}

// Options: argumentos ganham das variáveis de ambiente. O token nunca vai na linha de comando (apareceria no ps):
// --token-file, SERVER_EXEC_TOKEN_FILE ou SERVER_EXEC_TOKEN.
const Options = struct {
    port: u16 = DEFAULT_PORT,
    bind: []const u8 = "0.0.0.0",
    token: ?[]const u8 = null,
};

const usage =
    \\server-exec: FastExec remote daemon
    \\  --port N           listen port (default 9142; env SERVER_EXEC_PORT)
    \\  --bind ADDR        listen address (default 0.0.0.0; env SERVER_EXEC_BIND), e.g. 127.0.0.1 or a VPN address
    \\  --token-file PATH  require this token from clients (env SERVER_EXEC_TOKEN_FILE, or SERVER_EXEC_TOKEN)
    \\
;

var auth_token: ?[]const u8 = null;

fn envOwned(allocator: std.mem.Allocator, name: []const u8) ?[]u8 {
    return std.process.getEnvVarOwned(allocator, name) catch null;
}

fn readToken(allocator: std.mem.Allocator, path: []const u8) ![]const u8 {
    const raw = try fs.cwd().readFileAlloc(allocator, path, 4096);
    const t = std.mem.trim(u8, raw, " \t\r\n");
    if (t.len == 0 or t.len > MAX_TOKEN_LEN) return error.BadToken;
    return t;
}

// parseOptions: o que é alocado aqui vive o processo inteiro (não é liberado de propósito).
fn parseOptions(allocator: std.mem.Allocator) !Options {
    var o = Options{};
    var token_file: ?[]const u8 = envOwned(allocator, "SERVER_EXEC_TOKEN_FILE");
    if (envOwned(allocator, "SERVER_EXEC_PORT")) |v| o.port = try std.fmt.parseInt(u16, v, 10);
    if (envOwned(allocator, "SERVER_EXEC_BIND")) |v| o.bind = v;
    if (envOwned(allocator, "SERVER_EXEC_TOKEN")) |v| {
        const t = std.mem.trim(u8, v, " \t\r\n");
        if (t.len > 0 and t.len <= MAX_TOKEN_LEN) o.token = t;
    }

    const args = try std.process.argsAlloc(allocator);
    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        const a = args[i];
        const has_val = i + 1 < args.len;
        if (std.mem.eql(u8, a, "--port") and has_val) {
            i += 1;
            o.port = try std.fmt.parseInt(u16, args[i], 10);
        } else if (std.mem.eql(u8, a, "--bind") and has_val) {
            i += 1;
            o.bind = args[i];
        } else if (std.mem.eql(u8, a, "--token-file") and has_val) {
            i += 1;
            token_file = args[i];
        } else {
            std.io.getStdErr().writeAll(usage) catch {};
            std.process.exit(if (std.mem.eql(u8, a, "--help") or std.mem.eql(u8, a, "-h")) 0 else 2);
        }
    }
    if (token_file) |p| o.token = try readToken(allocator, p);
    return o;
}

// tokenEql compara em tempo constante para o tamanho do maior (não vaza quantos bytes casaram).
fn tokenEql(a: []const u8, b: []const u8) bool {
    var diff: u8 = if (a.len != b.len) 1 else 0;
    const n = @max(a.len, b.len);
    var i: usize = 0;
    while (i < n) : (i += 1) {
        const x: u8 = if (i < a.len) a[i] else 0;
        const y: u8 = if (i < b.len) b[i] else 0;
        diff |= x ^ y;
    }
    return diff == 0;
}

// handleAuth lê o SA e responde; devolve se a conexão pode seguir.
fn handleAuth(reader: anytype, writer: anytype) !bool {
    const len = try reader.readInt(u16, .little);
    if (len > MAX_TOKEN_LEN) return false;
    var buf: [MAX_TOKEN_LEN]u8 = undefined;
    try reader.readNoEof(buf[0..len]);
    const ok = if (auth_token) |t| tokenEql(buf[0..len], t) else true;
    try writer.writeInt(u16, MAGIC_AUTH, .little);
    try writer.writeInt(u16, if (ok) 0 else 1, .little);
    return ok;
}

// encodePowerShell monta o script (prefixo de UTF-8 + comando + código de saída) e devolve o Base64
// de UTF-16LE que o -EncodedCommand espera. O comando passa por Out-String antes do exit: sem isso, a
// tabela que o PowerShell ainda estava montando (Format-Table automático) se perde quando o script sai.
// Erros vão como texto para o stderr: o powershell.exe com -EncodedCommand os mandaria em CLIXML.
fn encodePowerShell(allocator: std.mem.Allocator, cmd: []const u8) ![]u8 {
    const script = try std.fmt.allocPrint(allocator,
        "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $OutputEncoding=[Text.Encoding]::UTF8; $ErrorActionPreference='Continue'; $ProgressPreference='SilentlyContinue'\n. {{\n{s}\n}} 2>&1 | ForEach-Object {{ if ($_ -is [System.Management.Automation.ErrorRecord]) {{ [Console]::Error.WriteLine($_.ToString()) }} else {{ $_ }} }} | Out-String -Stream -Width 4096\nif ($LASTEXITCODE) {{ exit $LASTEXITCODE }}",
        .{cmd});
    defer allocator.free(script);
    const utf16 = try std.unicode.utf8ToUtf16LeAlloc(allocator, script);
    defer allocator.free(utf16);
    const bytes = std.mem.sliceAsBytes(utf16);
    const out = try allocator.alloc(u8, std.base64.standard.Encoder.calcSize(bytes.len));
    _ = std.base64.standard.Encoder.encode(out, bytes);
    return out;
}

// ---------------------------------------------------------------------------------------------
// Execução de filho com vigia de desconexão: se o cliente fecha o socket enquanto o comando roda
// (timeout do lado dele, cancelamento), o filho é morto em vez de ficar órfão.
// ---------------------------------------------------------------------------------------------

// POLLRDHUP (Linux): o par fechou a escrita; o std do Zig 0.13 não expõe a constante.
const POLLRDHUP: i16 = 0x2000;

const Watch = struct {
    sock: posix.socket_t,
    child_id: posix.pid_t,
    stop_r: posix.fd_t,
};

fn watchThread(w: *Watch) void {
    var fds = [2]posix.pollfd{
        .{ .fd = w.sock, .events = posix.POLL.IN | POLLRDHUP, .revents = 0 },
        .{ .fd = w.stop_r, .events = posix.POLL.IN, .revents = 0 },
    };
    while (true) {
        _ = posix.poll(&fds, -1) catch return;
        if (fds[1].revents != 0) return; // o filho terminou normalmente
        const rev = fds[0].revents;
        if (rev & (posix.POLL.HUP | posix.POLL.ERR | POLLRDHUP) != 0) {
            posix.kill(w.child_id, posix.SIG.KILL) catch {};
            return;
        }
        if (rev & posix.POLL.IN != 0) {
            var b: [1]u8 = undefined;
            const n = posix.recvfrom(w.sock, &b, posix.MSG.PEEK | posix.MSG.DONTWAIT, null, null) catch 0;
            if (n == 0) {
                posix.kill(w.child_id, posix.SIG.KILL) catch {};
            }
            return; // dado antecipado do cliente não é suportado: para de vigiar
        }
    }
}

fn feedStdin(f: fs.File, data: []const u8) void {
    f.writeAll(data) catch {};
    f.close();
}

fn exitCodeOf(term: std.process.Child.Term) i32 {
    return switch (term) {
        .Exited => |code| @intCast(code),
        .Signal => |sig| -@as(i32, @intCast(sig)),
        .Stopped => |sig| -@as(i32, @intCast(sig)),
        .Unknown => |code| -@as(i32, @intCast(code)),
    };
}

/// Executa argv, coleta stdout/stderr (até MAX_OUTPUT_LEN) e devolve o código de saída.
/// Erro de spawn volta como erro; nada rodou nesse caso.
fn runChild(
    allocator: std.mem.Allocator,
    sock: posix.socket_t,
    argv: []const []const u8,
    cwd: ?[]const u8,
    stdin_data: []const u8,
    stdout: *std.ArrayList(u8),
    stderr: *std.ArrayList(u8),
    env: ?*const std.process.EnvMap,
) !i32 {
    var child = std.process.Child.init(argv, allocator);
    child.cwd = cwd;
    child.env_map = env;
    child.stdin_behavior = if (stdin_data.len > 0) .Pipe else if (is_windows) .Inherit else .Ignore;
    child.stdout_behavior = .Pipe;
    child.stderr_behavior = .Pipe;
    try child.spawn();

    var stdin_thread: ?std.Thread = null;
    if (stdin_data.len > 0) {
        const f = child.stdin.?;
        child.stdin = null; // a thread de escrita fecha
        if (std.Thread.spawn(.{ .stack_size = THREAD_STACK }, feedStdin, .{ f, stdin_data })) |t| {
            stdin_thread = t;
        } else |_| {
            f.close();
        }
    }

    var watch_thread: ?std.Thread = null;
    var stop_pipe: [2]posix.fd_t = undefined;
    var have_pipe = false;
    var w: Watch = undefined;
    if (!is_windows) {
        if (posix.pipe()) |p| {
            stop_pipe = p;
            have_pipe = true;
            w = .{ .sock = sock, .child_id = child.id, .stop_r = p[0] };
            watch_thread = std.Thread.spawn(.{ .stack_size = THREAD_STACK }, watchThread, .{&w}) catch null;
        } else |_| {}
    }

    child.collectOutput(stdout, stderr, MAX_OUTPUT_LEN) catch |err| {
        std.log.err("Collect output error: {s}", .{@errorName(err)});
    };

    if (!is_windows) {
        if (watch_thread) |t| {
            _ = posix.write(stop_pipe[1], "x") catch {};
            t.join();
        }
        if (have_pipe) {
            posix.close(stop_pipe[0]);
            posix.close(stop_pipe[1]);
        }
    }
    if (stdin_thread) |t| t.join();

    const term = try child.wait();
    return exitCodeOf(term);
}

fn handleExec(allocator: std.mem.Allocator, reader: anytype, writer: anytype, shell: []const u8, sock: posix.socket_t) !void {
    const cmd_len = try reader.readInt(u32, .little);
    if (cmd_len > MAX_CMD_LEN) {
        std.log.err("Command too large: {d} bytes", .{cmd_len});
        return error.CommandTooLarge;
    }

    const cmd_buf = try allocator.alloc(u8, cmd_len);
    defer allocator.free(cmd_buf);

    try reader.readNoEof(cmd_buf);

    // Linux: shell -c. Windows: PowerShell com -EncodedCommand (Base64 de UTF-16LE), que chega ao
    // processo sem nenhuma regra de aspas; saída em UTF-8 e código de saída do último comando.
    var encoded: []u8 = &[_]u8{};
    defer if (is_windows) allocator.free(encoded);
    const argv: []const []const u8 = if (is_windows) blk: {
        encoded = try encodePowerShell(allocator, cmd_buf);
        break :blk &[_][]const u8{ shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-OutputFormat", "Text", "-EncodedCommand", encoded };
    } else &[_][]const u8{ shell, "-c", cmd_buf };

    var stdout_list = std.ArrayList(u8).init(allocator);
    defer stdout_list.deinit();
    var stderr_list = std.ArrayList(u8).init(allocator);
    defer stderr_list.deinit();

    const exit_code = runChild(allocator, sock, argv, null, "", &stdout_list, &stderr_list, null) catch |err| {
        std.log.err("Exec error: {s}", .{@errorName(err)});
        try writer.writeInt(u16, MAGIC_EXEC, .little);
        try writer.writeInt(i32, -1, .little);
        const err_msg = if (err == error.FileNotFound or err == error.AccessDenied) "spawn_error" else "process_wait_error";
        try writer.writeInt(u32, 0, .little);
        try writer.writeInt(u32, @intCast(err_msg.len), .little);
        try writer.writeAll(err_msg);
        return;
    };

    // Send response: magic + exit_code + stdout_len + stdout + stderr_len + stderr
    try writer.writeInt(u16, MAGIC_EXEC, .little);
    try writer.writeInt(i32, exit_code, .little);

    try writer.writeInt(u32, @intCast(stdout_list.items.len), .little);
    if (stdout_list.items.len > 0) {
        try writer.writeAll(stdout_list.items);
    }

    try writer.writeInt(u32, @intCast(stderr_list.items.len), .little);
    if (stderr_list.items.len > 0) {
        try writer.writeAll(stderr_list.items);
    }
}

fn handleX(allocator: std.mem.Allocator, reader: anytype, writer: anytype, shell: []const u8, sock: posix.socket_t) !void {
    var hdr: [14]u8 = undefined;
    try reader.readNoEof(&hdr);
    const flags = std.mem.readInt(u16, hdr[0..2], .little);
    const cwd_len: usize = std.mem.readInt(u16, hdr[2..4], .little);
    const argc: usize = std.mem.readInt(u16, hdr[4..6], .little);
    const body_len: usize = std.mem.readInt(u32, hdr[6..10], .little);
    const stdin_len: usize = std.mem.readInt(u32, hdr[10..14], .little);
    if (cwd_len > 4096 or body_len > MAX_CMD_LEN or stdin_len > MAX_STDIN_LEN) return error.RequestTooLarge;

    const buf = try allocator.alloc(u8, cwd_len + body_len + stdin_len);
    defer allocator.free(buf);
    try reader.readNoEof(buf);
    const cwd: ?[]const u8 = if (cwd_len > 0) buf[0..cwd_len] else null;
    const body = buf[cwd_len .. cwd_len + body_len];
    const stdin_data = buf[cwd_len + body_len ..];

    var argv_list = std.ArrayList([]const u8).init(allocator);
    defer argv_list.deinit();
    var encoded: []u8 = &[_]u8{};
    defer if (encoded.len > 0) allocator.free(encoded);
    if (flags & FLAG_DIRECT != 0) {
        if (argc == 0) return error.BadArgv;
        var it = std.mem.splitScalar(u8, body, 0);
        while (it.next()) |a| {
            if (argv_list.items.len == argc) break;
            try argv_list.append(a);
        }
        if (argv_list.items.len != argc) return error.BadArgv;
    } else if (is_windows) {
        // linha de shell no Windows: PowerShell com -EncodedCommand, igual ao SE
        encoded = try encodePowerShell(allocator, body);
        try argv_list.appendSlice(&[_][]const u8{ shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-OutputFormat", "Text", "-EncodedCommand", encoded });
    } else {
        try argv_list.append(shell);
        try argv_list.append("-c");
        try argv_list.append(body);
    }

    var stdout_list = std.ArrayList(u8).init(allocator);
    defer stdout_list.deinit();
    var stderr_list = std.ArrayList(u8).init(allocator);
    defer stderr_list.deinit();

    // Ambiente não interativo, o mesmo que o cliente exportava na frente de cada comando via shell.
    var env = try std.process.getEnvMap(allocator);
    defer env.deinit();
    for (DEFAULT_ENV) |kv| try env.put(kv[0], kv[1]);

    var exit_code: i32 = undefined;
    if (runChild(allocator, sock, argv_list.items, cwd, stdin_data, &stdout_list, &stderr_list, &env)) |code| {
        exit_code = code;
    } else |err| {
        // nada rodou: devolve 127 (não achou) ou 126 (não pôde executar), como o shell faria
        exit_code = if (err == error.FileNotFound) 127 else 126;
        const why: []const u8 = blk: {
            if (err == error.FileNotFound) {
                if (cwd) |d| {
                    if (fs.cwd().access(d, .{})) |_| {} else |_| break :blk "cwd not found";
                }
                break :blk "command not found";
            }
            break :blk @errorName(err);
        };
        const name = if (argv_list.items.len > 0) argv_list.items[0] else "?";
        try stderr_list.writer().print("{s}: {s}\n", .{ name, why });
    }

    var rh: [16]u8 = undefined;
    std.mem.writeInt(u16, rh[0..2], MAGIC_X, .little);
    std.mem.writeInt(u16, rh[2..4], 0, .little);
    std.mem.writeInt(i32, rh[4..8], exit_code, .little);
    std.mem.writeInt(u32, rh[8..12], @intCast(stdout_list.items.len), .little);
    std.mem.writeInt(u32, rh[12..16], @intCast(stderr_list.items.len), .little);
    try writer.writeAll(&rh);
    if (stdout_list.items.len > 0) try writer.writeAll(stdout_list.items);
    if (stderr_list.items.len > 0) try writer.writeAll(stderr_list.items);
}

fn handlePut(allocator: std.mem.Allocator, reader: anytype, writer: anytype) !void {
    const path_len = try reader.readInt(u16, .little);
    const file_size = try reader.readInt(u64, .little);

    if (path_len == 0 or path_len > 4096) {
        return error.InvalidPathLength;
    }

    const path = try allocator.alloc(u8, path_len);
    defer allocator.free(path);
    try reader.readNoEof(path);

    // Ensure parent directory exists
    if (fs.path.dirname(path)) |dir| {
        if (dir.len > 0) {
            fs.cwd().makePath(dir) catch {};
        }
    }

    // Atomic temporary target: path.tmp.<timestamp>
    const tmp_path = try std.fmt.allocPrint(allocator, "{s}.tmp.{d}", .{ path, std.time.nanoTimestamp() });
    defer allocator.free(tmp_path);

    var file = fs.createFileAbsolute(tmp_path, .{ .truncate = true }) catch |err| {
        std.log.err("Failed to create file {s}: {s}", .{ tmp_path, @errorName(err) });
        // Drain incoming bytes to avoid socket desync
        var drain_rem = file_size;
        var drain_buf: [STREAM_BUF_SIZE]u8 = undefined;
        while (drain_rem > 0) {
            const to_read = @min(drain_rem, drain_buf.len);
            try reader.readNoEof(drain_buf[0..to_read]);
            drain_rem -= to_read;
        }

        try writer.writeInt(u16, MAGIC_PUT, .little);
        try writer.writeInt(i32, -1, .little);
        const err_msg = "failed_create_file";
        try writer.writeInt(u32, @intCast(err_msg.len), .little);
        try writer.writeAll(err_msg);
        return;
    };
    errdefer {
        file.close();
        fs.deleteFileAbsolute(tmp_path) catch {};
    }

    var remaining = file_size;
    var buf: [STREAM_BUF_SIZE]u8 = undefined;
    while (remaining > 0) {
        const to_read = @min(remaining, buf.len);
        try reader.readNoEof(buf[0..to_read]);
        try file.writeAll(buf[0..to_read]);
        remaining -= to_read;
    }
    file.close();

    // Atomic rename to final path
    fs.renameAbsolute(tmp_path, path) catch |err| {
        std.log.err("Failed to atomic rename {s} -> {s}: {s}", .{ tmp_path, path, @errorName(err) });
        fs.deleteFileAbsolute(tmp_path) catch {};
        try writer.writeInt(u16, MAGIC_PUT, .little);
        try writer.writeInt(i32, -1, .little);
        const err_msg = "failed_rename";
        try writer.writeInt(u32, @intCast(err_msg.len), .little);
        try writer.writeAll(err_msg);
        return;
    };

    // Success response
    try writer.writeInt(u16, MAGIC_PUT, .little);
    try writer.writeInt(i32, 0, .little);
    const ok_msg = "OK";
    try writer.writeInt(u32, @intCast(ok_msg.len), .little);
    try writer.writeAll(ok_msg);
}

fn handleGet(allocator: std.mem.Allocator, reader: anytype, writer: anytype) !void {
    const path_len = try reader.readInt(u16, .little);
    if (path_len == 0 or path_len > 4096) {
        return error.InvalidPathLength;
    }

    const path = try allocator.alloc(u8, path_len);
    defer allocator.free(path);
    try reader.readNoEof(path);

    const file = fs.openFileAbsolute(path, .{}) catch |err| {
        std.log.err("Failed to open file {s}: {s}", .{ path, @errorName(err) });
        try writer.writeInt(u16, MAGIC_GET, .little);
        try writer.writeInt(i32, -1, .little);
        try writer.writeInt(u64, 0, .little); // file_size = 0
        const err_msg = "file_not_found";
        try writer.writeInt(u32, @intCast(err_msg.len), .little);
        try writer.writeAll(err_msg);
        return;
    };
    defer file.close();

    const stat = try file.stat();
    const file_size = stat.size;

    // Send header: magic (2) + status 0 (4) + file_size (8) + msg_len 0 (4)
    try writer.writeInt(u16, MAGIC_GET, .little);
    try writer.writeInt(i32, 0, .little);
    try writer.writeInt(u64, file_size, .little);
    try writer.writeInt(u32, 0, .little);

    // Stream file data
    var remaining = file_size;
    var buf: [STREAM_BUF_SIZE]u8 = undefined;
    while (remaining > 0) {
        const to_read = @min(remaining, buf.len);
        const n = try file.readAll(buf[0..to_read]);
        if (n == 0) break;
        try writer.writeAll(buf[0..n]);
        remaining -= n;
    }
}

const BufWriter = std.io.BufferedWriter(16 * 1024, net.Stream.Writer);

fn handleClient(allocator: std.mem.Allocator, stream: net.Stream) void {
    defer stream.close();

    if (!is_windows) {
        // Enable TCP_NODELAY (Linux; no Windows o ganho não compensa a API diferente)
        const fd = stream.handle;
        var opt_val: i32 = 1;
        _ = posix.system.setsockopt(fd, posix.IPPROTO.TCP, posix.TCP.NODELAY, std.mem.asBytes(&opt_val), @sizeOf(i32));
        // Conexão parada por IDLE_TIMEOUT_S sem novo pedido é fechada.
        const tv = posix.timeval{ .tv_sec = IDLE_TIMEOUT_S, .tv_usec = 0 };
        posix.setsockopt(fd, posix.SOL.SOCKET, posix.SO.RCVTIMEO, std.mem.asBytes(&tv)) catch {};
    }

    // Leitura e escrita com buffer: o cabeçalho + comando chegam num recv só e a resposta sai num send só.
    var br = std.io.bufferedReader(stream.reader());
    const reader = br.reader();
    var bw = BufWriter{ .unbuffered_writer = stream.writer() };
    const writer = bw.writer();
    const shell = getShell();
    var authed = auth_token == null;

    while (true) {
        const magic = reader.readInt(u16, .little) catch |err| {
            if (err == error.EndOfStream) break;
            if (err != error.WouldBlock) std.log.err("Read magic error: {s}", .{@errorName(err)});
            break;
        };

        if (magic == MAGIC_AUTH) {
            // aceito com o pedido já no buffer (o cliente FastExec manda os dois juntos): a resposta do SA sai no
            // mesmo envio da resposta do pedido. Sem pedido esperando, responde já (cliente que espera o SA).
            const ok = handleAuth(reader, writer) catch false;
            if (!ok) {
                bw.flush() catch {};
                std.log.err("Auth refused", .{});
                break;
            }
            authed = true;
            if (br.end == br.start) bw.flush() catch break;
            continue;
        }
        if (!authed) {
            // avisa o cliente (SA status 2 = token exigido) em vez de só fechar: ele mostra o motivo
            std.log.err("Request without auth (magic 0x{x}): closing", .{magic});
            writer.writeInt(u16, MAGIC_AUTH, .little) catch break;
            writer.writeInt(u16, 2, .little) catch break;
            bw.flush() catch {};
            break;
        }

        switch (magic) {
            MAGIC_EXEC => {
                handleExec(allocator, reader, writer, shell, stream.handle) catch |err| {
                    std.log.err("handleExec error: {s}", .{@errorName(err)});
                    break;
                };
            },
            MAGIC_X => {
                handleX(allocator, reader, writer, shell, stream.handle) catch |err| {
                    std.log.err("handleX error: {s}", .{@errorName(err)});
                    break;
                };
            },
            MAGIC_PUT => {
                handlePut(allocator, reader, writer) catch |err| {
                    std.log.err("handlePut error: {s}", .{@errorName(err)});
                    break;
                };
            },
            MAGIC_GET => {
                handleGet(allocator, reader, writer) catch |err| {
                    std.log.err("handleGet error: {s}", .{@errorName(err)});
                    break;
                };
            },
            else => {
                std.log.err("Invalid magic: 0x{x}", .{magic});
                break;
            },
        }
        bw.flush() catch break;
    }
}

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const opts = parseOptions(allocator) catch |err| {
        std.log.err("Bad options: {s}", .{@errorName(err)});
        std.io.getStdErr().writeAll(usage) catch {};
        std.process.exit(2);
    };
    auth_token = opts.token;
    const port = opts.port;
    const address = net.Address.parseIp(opts.bind, port) catch {
        std.log.err("Bad bind address: {s}", .{opts.bind});
        std.process.exit(2);
    };
    var server = try address.listen(.{
        .reuse_address = true,
        .reuse_port = !is_windows,
    });
    defer server.deinit();

    std.log.info("server-exec listening on {s}:{d}, auth {s}", .{ opts.bind, port, if (auth_token != null) "on" else "off" });
    if (auth_token == null and !std.mem.eql(u8, opts.bind, "127.0.0.1")) {
        std.log.warn("no token: anyone who reaches this port can run commands (use --token-file or --bind)", .{});
    }

    while (true) {
        const conn = server.accept() catch |err| {
            std.log.err("Accept error: {s}", .{@errorName(err)});
            continue;
        };

        const thread = std.Thread.spawn(.{ .stack_size = THREAD_STACK }, handleClient, .{ allocator, conn.stream }) catch |err| {
            std.log.err("Thread spawn error: {s}", .{@errorName(err)});
            handleClient(allocator, conn.stream);
            continue;
        };
        thread.detach();
    }
}
