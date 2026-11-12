// build.zig — server-exec, o daemon remoto do FastExec (Zig 0.13). Mesmo padrão dos outros projetos Zig do homelab.
//   zig build                      -> zig-out/bin/server-exec-<alvo> para os 3 alvos Linux abaixo
//   zig build linux-amd64          -> só um alvo (linux-amd64 | linux-arm64 | linux-armv7 | windows-amd64)
//   zig build -Dmode=ReleaseFast   -> outro modo (padrão ReleaseSafe: mantém checagem de limites e overflow;
//                                     medido no mesmo nível do ReleaseFast, porque o custo por chamada é criar o processo)
// Alvos (iguais ao build.sh, que continua valendo):
//   linux-amd64    x86_64-linux-musl        servidores, VMs, WSL
//   linux-arm64    aarch64-linux-musl       Raspberry Pi 4/5, roteadores ARM (OpenWrt aarch64)
//   linux-armv7    arm-linux-musleabihf     ARM 32 bits hard-float (roteadores antigos, TV boxes Android)
//   windows-amd64  x86_64-windows-gnu       Windows (comandos rodam no PowerShell)
// Estático e com strip.
// ATENÇÃO: windows-amd64 NÃO compila desde o commit 804fb86 (08/nov): main.zig:447 usa `[_]posix.pid_t{0} ** 8`, e no Windows
// pid_t é um HANDLE (*anyopaque). O build.sh antigo tem o mesmo defeito. Por isso o `zig build` padrão só instala os alvos Linux;
// quando o main.zig for corrigido, troque `padrao` para true no alvo windows-amd64.
const std = @import("std");

const Alvo = struct { nome: []const u8, query: std.Target.Query, padrao: bool = true };

const alvos = [_]Alvo{
    .{ .nome = "linux-amd64", .query = .{ .cpu_arch = .x86_64, .os_tag = .linux, .abi = .musl } },
    .{ .nome = "linux-arm64", .query = .{ .cpu_arch = .aarch64, .os_tag = .linux, .abi = .musl } },
    .{ .nome = "linux-armv7", .query = .{ .cpu_arch = .arm, .os_tag = .linux, .abi = .musleabihf } },
    .{ .nome = "windows-amd64", .query = .{ .cpu_arch = .x86_64, .os_tag = .windows, .abi = .gnu }, .padrao = false },
};

pub fn build(b: *std.Build) void {
    const optimize = b.option(std.builtin.OptimizeMode, "mode", "Debug | ReleaseSafe | ReleaseFast | ReleaseSmall (padrao: ReleaseSafe)") orelse .ReleaseSafe;
    inline for (alvos) |a| {
        const exe = b.addExecutable(.{
            .name = "server-exec-" ++ a.nome,
            .root_source_file = b.path("main.zig"),
            .target = b.resolveTargetQuery(a.query),
            .optimize = optimize,
            .strip = true,
        });
        const inst = b.addInstallArtifact(exe, .{});
        if (a.padrao) b.getInstallStep().dependOn(&inst.step);
        const passo = b.step(a.nome, "compila so o alvo " ++ a.nome);
        passo.dependOn(&inst.step);
    }
}
