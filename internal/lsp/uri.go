package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

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
