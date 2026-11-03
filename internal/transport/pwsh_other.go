//go:build !windows

package transport

// ResolveShellArgv no Linux/macOS não altera argv.
func ResolveShellArgv(argv []string) []string {
	return argv
}
