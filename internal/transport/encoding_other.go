//go:build !windows

package transport

// decodeMixed no Linux assume que a saída do sistema e de processos já está em UTF-8.
func decodeMixed(b []byte) string {
	return string(b)
}
