#!/bin/sh
# Builds server-exec (the FastExec remote daemon) with Zig 0.13 for every target, or the ones given.
# Usage: ./build.sh [target...]       Output: dist/server-exec-<target>[.exe]
# Targets:
#   linux-amd64    x86_64-linux-musl        servers, VMs, WSL
#   linux-arm64    aarch64-linux-musl       Raspberry Pi 4/5, ARM routers (OpenWrt aarch64)
#   linux-armv7    arm-linux-musleabihf     32-bit ARM hard-float (older routers, Android TV boxes)
#   windows-amd64  x86_64-windows-gnu       Windows (commands run in PowerShell)
# One mode for all: ReleaseSafe keeps bounds and overflow checks (fails loudly instead of corrupting memory);
# measured on par with ReleaseFast, since the cost per call is spawning the process, not the daemon code.
# Static and stripped. ZIG=/path/to/zig overrides the zig in PATH.
set -e
cd "$(dirname "$0")"
ZIG=${ZIG:-zig}
CACHE=${ZIG_CACHE:-${TMPDIR:-/tmp}/fastexec-zigcache}
mkdir -p dist

build() { # name target
  echo "== $1 ($2)"
  $ZIG build-exe main.zig -O ReleaseSafe -fstrip -target "$2" --name "server-exec-$1" \
    --cache-dir "$CACHE" --global-cache-dir "$CACHE" -femit-bin="dist/server-exec-$1$3"
  rm -f "dist/server-exec-$1.o" "dist/server-exec-$1.exe.obj" "dist/server-exec-$1.pdb" \
    "server-exec-$1.o" "server-exec-$1.exe.obj" "server-exec-$1.pdb"
}

targets="${*:-linux-amd64 linux-arm64 linux-armv7 windows-amd64}"
for t in $targets; do
  case "$t" in
    linux-amd64)   build linux-amd64 x86_64-linux-musl ;;
    linux-arm64)   build linux-arm64 aarch64-linux-musl ;;
    linux-armv7)   build linux-armv7 arm-linux-musleabihf ;;
    windows-amd64) build windows-amd64 x86_64-windows-gnu .exe ;;
    *) echo "unknown target: $t" >&2; exit 2 ;;
  esac
done
ls -la dist
