package daemon

import "testing"

func TestResolveAddr(t *testing.T) {
	cases := map[string]string{
		"":                 defaultAddr,
		"127.0.0.1:9199":   "127.0.0.1:9199",
		"localhost:9199":   "localhost:9199",
		"[::1]:9199":       "[::1]:9199",
		"0.0.0.0:9199":     defaultAddr, // never listen beyond loopback
		"192.168.1.5:9199": defaultAddr,
		"9199":             defaultAddr, // malformed
		"evil.example:80":  defaultAddr,
	}
	for in, want := range cases {
		if got := resolveAddr(in); got != want {
			t.Errorf("resolveAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
