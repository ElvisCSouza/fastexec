package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

func itoa(n int) string { return strconv.Itoa(n) }

// jsonPresent reports whether a raw capability field carries a value: servers
// announce a capability as `true`, as an options object, or as `false`/absent
// when they do not have it.
func jsonPresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("false"))
}

// utf16Width is how many UTF-16 code units r occupies. The protocol counts
// columns and offsets in those units, so a file with an emoji or an accented
// character in it cannot be measured in bytes.
func utf16Width(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// offsetOfText maps an LSP position to a byte offset in text. false means the
// position is past the end of the file, so a caller never slices out of range on
// an answer it does not control.
func offsetOfText(text string, pos Position) (int, bool) {
	base, line := 0, 0
	for line < pos.Line {
		nl := strings.IndexByte(text[base:], '\n')
		if nl < 0 {
			return 0, false
		}
		base += nl + 1
		line++
	}
	rest := text[base:]
	col, i := 0, 0
	for i < len(rest) {
		if col == pos.Character {
			return base + i, true
		}
		r, size := utf8.DecodeRuneInString(rest[i:])
		if r == '\n' {
			break
		}
		col += utf16Width(r)
		i += size
	}
	if col == pos.Character {
		return base + i, true
	}
	return 0, false
}

// absPath resolves a caller-supplied path to an absolute one. Tool callers pass
// whatever the model wrote, which may be relative to the daemon's working
// directory, so every path entering the LSP layer goes through here.
func absPath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
}

// sameFile compares two paths as locations, tolerating case and separator
// differences on Windows.
func sameFile(a, b string) bool {
	aa, err1 := absPath(a)
	bb, err2 := absPath(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	if aa == bb {
		return true
	}
	return strings.EqualFold(filepath.ToSlash(aa), filepath.ToSlash(bb))
}
