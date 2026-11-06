// Command release builds the FastExec release files into dist/<version>/ and writes SHA256SUMS.
//
//	go run ./tools/release -version v0.1.0
//
// FastExec: windows-amd64 (no console window), linux-amd64, linux-arm64, darwin-amd64, darwin-arm64.
// server-exec (the remote daemon) is built too when Zig 0.13 is available (-zig, default "zig" in PATH);
// otherwise those files are skipped with a note. Runs the same on Windows, Linux and macOS.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type target struct{ goos, goarch string }

var targets = []target{
	{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"},
}

func main() {
	version := flag.String("version", "", "release version, e.g. v0.1.0 (required)")
	zig := flag.String("zig", "zig", "zig 0.13 executable for server-exec; empty to skip")
	flag.Parse()
	if *version == "" || strings.ContainsAny(*version, `/\ `) {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/release -version v0.1.0")
		os.Exit(2)
	}
	root, err := os.Getwd()
	check(err)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		fail("run from the repository root")
	}
	out := filepath.Join(root, "dist", *version)
	check(os.RemoveAll(out))
	check(os.MkdirAll(out, 0o755))

	for _, t := range targets {
		name := fmt.Sprintf("fastexec-%s-%s", t.goos, t.goarch)
		ldflags := "-s -w -X homelab/fastexec/cli.Version=" + *version
		if t.goos == "windows" {
			name += ".exe"
			ldflags = "-H=windowsgui " + ldflags
		}
		fmt.Println("==", name)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", filepath.Join(out, name), "./cmd/fastexec")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		check(cmd.Run())
	}

	if *zig != "" {
		if _, err := exec.LookPath(*zig); err != nil {
			fmt.Printf("note: %q not found, server-exec not built (build it with server/build.sh where Zig 0.13 is installed)\n", *zig)
		} else {
			buildServer(root, out, *zig)
		}
	}

	writeSums(out)
	fmt.Println("release files in", out)
}

// buildServer compila o server-exec para os mesmos alvos do server/build.sh, direto com o zig (sem sh).
func buildServer(root, out, zig string) {
	cache := filepath.Join(os.TempDir(), "fastexec-zigcache")
	for _, t := range [][2]string{
		{"linux-amd64", "x86_64-linux-musl"}, {"linux-arm64", "aarch64-linux-musl"},
		{"linux-armv7", "arm-linux-musleabihf"}, {"windows-amd64", "x86_64-windows-gnu"},
	} {
		name := "server-exec-" + t[0]
		if strings.HasPrefix(t[0], "windows") {
			name += ".exe"
		}
		fmt.Println("==", name)
		cmd := exec.Command(zig, "build-exe", filepath.Join(root, "server", "main.zig"), "-O", "ReleaseSafe", "-fstrip",
			"-target", t[1], "--cache-dir", cache, "--global-cache-dir", cache, "-femit-bin="+filepath.Join(out, name))
		cmd.Dir = out
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		check(cmd.Run())
	}
	// o zig deixa .o/.pdb ao lado do binário
	for _, pat := range []string{"*.o", "*.obj", "*.pdb"} {
		m, _ := filepath.Glob(filepath.Join(out, pat))
		for _, f := range m {
			_ = os.Remove(f)
		}
	}
}

func writeSums(dir string) {
	entries, err := os.ReadDir(dir)
	check(err)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "SHA256SUMS" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		check(err)
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		check(err)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), n)
	}
	check(os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644))
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "release:", msg)
	os.Exit(1)
}
