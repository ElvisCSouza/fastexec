package lsp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestStderrCauseReadsTheLastThingTheServerSaid: a language server that fails
// explains itself on stderr, and that explanation is the only thing FastExec can
// honestly repeat when the project does not load. The tail is what counts —
// everything before the cause is a banner, a download line or progress chatter.
func TestStderrCauseReadsTheLastThingTheServerSaid(t *testing.T) {
	chatter := "downloading package Newtonsoft.Json 13.0.1\nloading workspace\n"
	cause := "2026/11/12 09:00:09 error MSB4236: The SDK 'Microsoft.NET.Sdk' specified could not be found\n"

	tests := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "cause after the banner",
			log:  "2026/11/12 09:00:00 starting csharp-ls\n" + cause,
			want: "error MSB4236: The SDK 'Microsoft.NET.Sdk' specified could not be found",
		},
		{
			name: "last cause wins",
			log:  "error: solution not found\n" + cause,
			want: "error MSB4236: The SDK 'Microsoft.NET.Sdk' specified could not be found",
		},
		{
			name: "a bracketed clock is not part of the cause",
			log:  "[09:00:09] failed to open project App.csproj\n",
			want: "failed to open project App.csproj",
		},
		{
			name: "chatter alone is silence",
			log:  chatter,
			want: "",
		},
		{
			name: "an empty log is silence",
			log:  "",
			want: "",
		},
		{
			// The window is bounded on purpose: a cause from the first seconds of
			// a ten-minute session describes a start that has since been retried.
			name: "a cause inside the window is still read",
			log:  "error: no such file\n" + strings.Repeat("progress\n", stderrTailLines-1),
			want: "error: no such file",
		},
		{
			name: "a cause outside the window is not",
			log:  "error: no such file\n" + strings.Repeat("progress\n", stderrTailLines),
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StderrCause(tc.log); got != tc.want {
				t.Errorf("StderrCause(%q) = %q, want %q", tc.log, got, tc.want)
			}
		})
	}
}

// TestStderrCauseStaysShort: the phrase rides on an MCP answer, so it is paid
// for in tokens on every degraded query, and it must not end mid-rune.
func TestStderrCauseStaysShort(t *testing.T) {
	long := "error " + strings.Repeat("x", 500) + "\n"
	got := StderrCause(long)
	if len(got) > stderrCauseMax+3 {
		t.Errorf("StderrCause clipped to %d bytes, want at most %d", len(got), stderrCauseMax+3)
	}
	if !strings.HasPrefix(got, "error x") {
		t.Errorf("StderrCause = %q, want the beginning of the cause kept", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("StderrCause = %q, want a visible cut", got)
	}

	// Multi-byte text: a byte-bounded cut must not leave half a rune behind.
	got = StderrCause("exception: " + strings.Repeat("é", 300) + "\n")
	if !utf8.ValidString(got) {
		t.Errorf("StderrCause = %q, want valid UTF-8", got)
	}
}

// TestStderrCauseCollapsesWhitespace: a server that indents its log would
// otherwise spend the whole note on spaces. The whole line is taken as it is —
// the continuation of a stack trace is not glued on, because the last line of a
// trace is the least informative part of it.
func TestStderrCauseCollapsesWhitespace(t *testing.T) {
	got := StderrCause("fatal:\t\tthe   project\n\t\t could not be loaded\n")
	if got != "fatal: the project" {
		t.Errorf("StderrCause = %q", got)
	}
}
