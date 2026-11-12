package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// uriKey is the identity of a document: every map lookup and every comparison
// between "our" URI and one that came back from the server goes through here.
//
// The two spellings are not the same string. typescript-language-server uses
// vscode-uri, which on Windows lowercases the drive letter and percent-encodes
// the colon ("file:///c%3A/dir/file.ts"), while PathToURI writes
// "file:///C:/dir/file.ts". Comparing them byte for byte makes the published
// diagnostics of a document invisible to the client that opened it — the file
// then looks permanently un-analysed, and `WaitSettled` waits out its whole
// deadline on a server that already answered.
//
// So: percent-decode always, and lowercase on Windows, where two URIs that
// differ only in case are the same file by definition.
func uriKey(uri string) string {
	if unescaped, err := url.PathUnescape(uri); err == nil {
		uri = unescaped
	}
	if runtime.GOOS == "windows" {
		uri = strings.ToLower(uri)
	}
	return uri
}

// PathToURI converts an absolute filesystem path to a file:// URI.
//
// LSP requires a URI, and Windows drive letters make the naive
// "file://" + path wrong ("W:\a" becomes "file:///W:/a"). Percent-encoding
// is applied to the path only, never to the separators or the drive colon.
func PathToURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		// Windows drive-relative or UNC-like path.
		if len(p) >= 2 && p[1] == ':' {
			p = "/" + p
		}
	}
	return "file://" + escapePath(p)
}

// URItoPath is the inverse of PathToURI and returns a path in OS form.
func URItoPath(uri string) string {
	s := uri
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		// Drop an authority component (file://host/share); empty for local files.
		if j := strings.IndexByte(s, '/'); j > 0 {
			s = s[j:]
		}
	}
	if unescaped, err := url.PathUnescape(s); err == nil {
		s = unescaped
	}
	// "/W:/dir" -> "W:/dir"
	if len(s) >= 3 && s[0] == '/' && s[2] == ':' {
		s = s[1:]
	}
	return filepath.FromSlash(s)
}

// escapePath percent-encodes the bytes that are unsafe in a URI path while
// keeping the reserved separators meaningful.
func escapePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		ch := p[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
			b.WriteByte(ch)
		case strings.IndexByte("-._~/!$&'()*+,;=:@", ch) >= 0:
			b.WriteByte(ch)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[ch>>4])
			b.WriteByte(hex[ch&0x0f])
		}
	}
	return b.String()
}
