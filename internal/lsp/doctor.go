package lsp

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// versionTimeout bounds the `--version` probe. A server that takes longer than
// this to print its version is reported as found-but-unresponsive rather than
// making `lsp doctor` hang.
const versionTimeout = 5 * time.Second

// DoctorEntry is the diagnostic result for one language.
type DoctorEntry struct {
	Language Language
	// Server is the server that answered the probe, or the preferred one when
	// nothing was found. A language may have several; this is which.
	Server Server
	// Found is whether an executable was located.
	Found bool
	// Path is the executable that was found.
	Path string
	// Version is the first line the server printed for its version flag, empty
	// when it could not be read.
	Version string
	// NoVersion is true for a launcher with no version flag at all (jdtls,
	// PowerShellEditorServices): an empty Version is then expected, not a
	// problem.
	NoVersion bool
	// Problem explains a found server whose version probe failed.
	Problem string
	// Tried lists the places that were searched, in order.
	Tried []string
	// Details are extra honesty lines a language adds about its own install,
	// printed for a server that WAS found. The TypeScript server is the case
	// this exists for: it is a front end that starts and exits immediately when
	// it cannot find a tsserver, which from the outside looks like a broken
	// server rather than a missing second package.
	Details []string
}

// Doctor probes every known language. It only runs the servers' own version
// flag; it installs nothing and prints the exact install command instead.
func Doctor(ctx context.Context) []DoctorEntry {
	out := make([]DoctorEntry, 0, len(Languages))
	for _, lang := range Languages {
		e := DoctorEntry{Language: lang, Server: lang.Primary()}
		srv, path, tried, ok := FindServer(lang, "")
		e.Tried = tried
		if !ok {
			out = append(out, e)
			continue
		}
		e.Server = srv
		e.Found = true
		e.Path = path
		e.Details = langDetails(lang, path)
		if srv.NoVersion {
			e.NoVersion = true
			out = append(out, e)
			continue
		}
		version, err := serverVersion(ctx, srv, path)
		if err != nil {
			e.Problem = err.Error()
		} else {
			e.Version = version
		}
		out = append(out, e)
	}
	return out
}

// langDetails asks the language for its extra honesty lines about a server that
// was found. A language that declares none contributes nothing, which is the
// normal case.
func langDetails(lang Language, bin string) []string {
	if lang.Details == nil {
		return nil
	}
	return lang.Details(bin)
}

// serverVersion runs the server's version flag. This is the only process
// `lsp doctor` ever starts, and it is the server itself, not an installer.
func serverVersion(ctx context.Context, srv Server, bin string) (string, error) {
	args := srv.VersionArgs
	if len(args) == 0 {
		args = []string{"--version"}
	}
	rctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(rctx, bin, args...)
	cmd.SysProcAttr = sysProcAttr()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" && err != nil {
		return "", err
	}
	// Some servers print a banner before the version; the first non-empty line
	// is the one that identifies the build.
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateLine(line, 120), nil
		}
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
