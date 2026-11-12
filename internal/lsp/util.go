package lsp

import (
	"path/filepath"
	"strconv"
	"strings"
)

func itoa(n int) string { return strconv.Itoa(n) }

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
