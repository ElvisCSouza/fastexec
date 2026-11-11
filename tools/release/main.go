// Command release builds the FastExec release files into dist/<version>/ and writes SHA256SUMS.
//
//	go run ./tools/release -version v0.1.0
//
// FastExec: windows-amd64 (no console window), linux-amd64, linux-arm64, darwin-amd64, darwin-arm64.
// server-exec (the remote daemon) is built too when Zig 0.13 is available (-zig, default "zig" in PATH);
// otherwise those files are skipped with a note. Runs the same on Windows, Linux and macOS.
package main

import (
	"bytes"
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
		goBuild(out, name, ldflags, t)
		// instalador: o mesmo binário com nome de instalador abre o assistente (cli.looksLikeSetup)
		setup := strings.Replace(name, "fastexec-", "fastexec-setup-", 1)
		copyFile(filepath.Join(out, name), filepath.Join(out, setup))
		if t.goos == "windows" {
			// gêmeo de console para o terminal: o pwsh não espera nem captura a saída de um app sem console
			goBuild(out, "fastexec-cli-"+t.goos+"-"+t.goarch+".exe", strings.TrimPrefix(ldflags, "-H=windowsgui "), t)
		}
	}

	if *zig != "" {
		if _, err := exec.LookPath(*zig); err != nil {
			fmt.Printf("note: %q not found, server-exec not built (build it with server/build.sh where Zig 0.13 is installed)\n", *zig)
		} else {
			buildServer(root, out, *zig)
		}
	}

	scanForbidden(out)
	writeSums(out)
	fmt.Println("release files in", out)
}

func goBuild(out, name, ldflags string, t target) {
	fmt.Println("==", name)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", filepath.Join(out, name), "./cmd/fastexec")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	check(cmd.Run())
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

// forbidden: textos que não podem aparecer em nenhum arquivo publicado (caminhos, hosts e nomes pessoais).
var forbidden = []string{
	"reposhomelab", "noteserver", "192.168.", "elvis", "gruposize", "andressa", "ax3000", "ax6000",
	"firestick", "popcorn", "size-apps", "size-ia", "workspace domo",
}

// scanForbidden falha a release se algum arquivo contém um texto de forbidden (sem diferenciar maiúsculas), em
// UTF-8 ou UTF-16 (strings de Windows). Mostra onde achou, para corrigir na origem.
func scanForbidden(dir string) {
	entries, err := os.ReadDir(dir)
	check(err)
	bad := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		check(err)
		low := asciiLower(data)
		for _, term := range forbidden {
			for _, pat := range [][]byte{[]byte(term), utf16le(term)} {
				if i := bytes.Index(low, pat); i >= 0 {
					bad++
					fmt.Fprintf(os.Stderr, "FORBIDDEN %q in %s at byte %d: %s\n", term, e.Name(), i, context(data, i, len(pat)))
					break
				}
			}
		}
	}
	if bad > 0 {
		fail(fmt.Sprintf("%d personal reference(s) found in the release files; fix them at the source and build again", bad))
	}
	fmt.Println("scan ok: no personal references in", len(entries), "files")
}
func utf16le(s string) []byte {
	b := make([]byte, 0, len(s)*2)
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}
func context(data []byte, i, n int) string {
	from, to := i-30, i+n+30
	if from < 0 {
		from = 0
	}
	if to > len(data) {
		to = len(data)
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return '.'
		}
		return r
	}, string(data[from:to]))
}
func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	check(err)
	check(os.WriteFile(dst, data, 0o755))
}

// asciiLower converte só A-Z: mantém o tamanho (bytes.ToLower reescreve bytes inválidos e desalinha os índices).
func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}
